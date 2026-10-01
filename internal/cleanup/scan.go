package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Env is everything the engine needs to know about the machine. Tests build
// it from a sandbox fixture; the CLI builds it from the real system or from
// the simulated sandbox.
type Env struct {
	Locations safety.Locations
	Guard     *safety.Guard
	Elevated  bool
	Now       func() time.Time
	// Disabled reports whether the user's whitelist disables a rule.
	Disabled func(*Rule) bool
	// Running returns lower-case executable names of running processes.
	Running func() map[string]bool
	// Remove deletes one verified item. Defaults to filesystem.RemoveVerified.
	Remove func(path string, fp filesystem.Fingerprint, check filesystem.CheckFunc) error
	// Specials implement non-file targets by Rule.Special name.
	Specials map[string]Special
}

// Special is a cleanup target that is not a set of files under a root, such
// as the Recycle Bin, which must be emptied through the Shell API.
type Special interface {
	// Measure reports what Clean would remove. It must not modify anything.
	Measure(ctx context.Context) (items int, bytes int64, err error)
	// Clean removes the target's contents and reports what was removed.
	Clean(ctx context.Context) (items int, bytes int64, err error)
}

// Special target names.
const (
	SpecialRecycleBin = "recycle-bin"
)

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Status is the state of one rule after scanning.
type Status string

const (
	StatusReady   Status = "ready"   // has items to clean
	StatusEmpty   Status = "empty"   // nothing to clean
	StatusSkipped Status = "skipped" // not scanned (reason given)
	StatusReview  Status = "review"  // could not be confirmed safe; left alone
)

// Item is one file or directory selected for removal.
type Item struct {
	Path        string
	Fingerprint filesystem.Fingerprint
	scope       string
}

// RuleScan is the result of scanning one rule.
type RuleScan struct {
	Rule   *Rule
	Status Status
	Reason string
	Roots  []string

	Files []Item
	Dirs  []Item
	Bytes int64

	// Items kept because they were too recent for the rule's MinAge.
	KeptRecent      int
	KeptRecentBytes int64

	// NeedsAdmin is set when the rule was skipped only for lack of elevation.
	NeedsAdmin bool
	// SpecialItems counts items of a Special target (which has no Files).
	SpecialItems int

	Skipped Tally
}

// ItemCount is the number of items the rule would remove.
func (rs *RuleScan) ItemCount() int { return len(rs.Files) + rs.SpecialItems }

// ScanResult aggregates all rules.
type ScanResult struct {
	Rules     []*RuleScan
	Duration  time.Duration
	Cancelled bool
}

// Bytes is the total reclaimable size across ready rules.
func (s *ScanResult) Bytes() int64 {
	var n int64
	for _, r := range s.Rules {
		if r.Status == StatusReady {
			n += r.Bytes
		}
	}
	return n
}

// Files is the total number of items across ready rules.
func (s *ScanResult) Files() int {
	n := 0
	for _, r := range s.Rules {
		if r.Status == StatusReady {
			n += r.ItemCount()
		}
	}
	return n
}

// Progress is updated concurrently while scanning or cleaning; UIs poll it.
type Progress struct {
	Items   atomic.Int64
	Bytes   atomic.Int64
	current atomic.Value // string
}

// Current returns the name of the rule being processed.
func (p *Progress) Current() string {
	if p == nil {
		return ""
	}
	s, _ := p.current.Load().(string)
	return s
}

func (p *Progress) setCurrent(s string) {
	if p != nil {
		p.current.Store(s)
	}
}

func (p *Progress) add(items int, bytes int64) {
	if p != nil {
		p.Items.Add(int64(items))
		p.Bytes.Add(bytes)
	}
}

// Scan evaluates rules concurrently. It never modifies the filesystem. If ctx
// is cancelled it returns the partial result with Cancelled set.
func Scan(ctx context.Context, env *Env, rules []*Rule, prog *Progress) *ScanResult {
	start := time.Now()
	res := &ScanResult{Rules: make([]*RuleScan, len(rules))}
	var running map[string]bool
	if env.Running != nil {
		running = env.Running()
	}

	sem := make(chan struct{}, max(2, runtime.NumCPU()))
	var wg sync.WaitGroup
	for i, r := range rules {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res.Rules[i] = scanRule(ctx, env, r, running, prog)
		}()
	}
	wg.Wait()
	res.Duration = time.Since(start)
	res.Cancelled = ctx.Err() != nil
	return res
}

func scanRule(ctx context.Context, env *Env, r *Rule, running map[string]bool, prog *Progress) *RuleScan {
	rs := &RuleScan{Rule: r}
	skip := func(s Status, format string, args ...any) *RuleScan {
		rs.Status, rs.Reason = s, fmt.Sprintf(format, args...)
		rs.Files, rs.Dirs, rs.Bytes = nil, nil, 0
		return rs
	}

	if env.Disabled != nil && env.Disabled(r) {
		return skip(StatusSkipped, "disabled in your whitelist")
	}
	if r.RequiresAdmin && !env.Elevated {
		rs.NeedsAdmin = true
		return skip(StatusSkipped, "requires administrator: run %s from an elevated terminal", buildinfo.Name)
	}
	if len(r.DetectPaths) > 0 && !anyExists(env, r.DetectPaths) {
		return skip(StatusEmpty, "not installed")
	}
	for _, exe := range r.AppProcesses {
		if running[strings.ToLower(exe)] {
			app := r.App
			if app == "" {
				app = exe
			}
			return skip(StatusSkipped, "%s is running; close it (including in the background) to clean this", app)
		}
	}

	prog.setCurrent(r.Name)
	if r.Special != "" {
		return scanSpecial(ctx, env, rs)
	}
	cutoff := env.now().Add(-r.MinAge)
	for _, tmpl := range r.Roots {
		paths, err := expandRoot(env, r, tmpl)
		if err != nil {
			return skip(StatusSkipped, "%v", err)
		}
		for _, p := range paths {
			root, status, reason := resolveRoot(env, p)
			if status == StatusEmpty {
				continue
			}
			if status != "" {
				return skip(status, "%s", reason)
			}
			rs.Roots = append(rs.Roots, root)
			if err := scanRoot(ctx, env, r, rs, root, cutoff, prog); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return skip(StatusSkipped, "scan cancelled")
				}
				if errors.Is(err, filesystem.ErrGone) {
					continue
				}
				return skip(StatusReview, "could not scan %s: %v", root, err)
			}
		}
	}
	if len(rs.Files) == 0 {
		rs.Status = StatusEmpty
		rs.Dirs = nil
		return rs
	}
	rs.Status = StatusReady
	// Deepest directories first so parents are attempted after children.
	sort.SliceStable(rs.Dirs, func(i, j int) bool {
		return strings.Count(rs.Dirs[i].Path, `\`) > strings.Count(rs.Dirs[j].Path, `\`)
	})
	return rs
}

func scanSpecial(ctx context.Context, env *Env, rs *RuleScan) *RuleScan {
	sp := env.Specials[rs.Rule.Special]
	if sp == nil {
		rs.Status, rs.Reason = StatusSkipped, "not available on this system"
		return rs
	}
	items, bytes, err := sp.Measure(ctx)
	switch {
	case err != nil:
		rs.Status, rs.Reason = StatusSkipped, "could not measure: "+err.Error()
	case items == 0:
		rs.Status = StatusEmpty
	default:
		rs.Status, rs.SpecialItems, rs.Bytes = StatusReady, items, bytes
	}
	return rs
}

// expandRoot turns a root template into concrete, normalized paths. Without
// `{profile}` that is exactly one path. With it, one path per real profile
// directory: a non-link directory containing the rule's ProfileMarker.
func expandRoot(env *Env, r *Rule, tmpl string) ([]string, error) {
	i := strings.Index(tmpl, `\`+profileToken+`\`)
	if i < 0 {
		p, err := env.Locations.Expand(tmpl)
		if err != nil {
			return nil, err
		}
		return []string{p}, nil
	}
	base, err := env.Locations.Expand(tmpl[:i])
	if err != nil {
		return nil, err
	}
	suffix := tmpl[i+len(profileToken)+1:]
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, nil // no profiles (or unreadable): nothing to clean here
	}
	var out []string
	for _, de := range entries {
		if !de.IsDir() || matchAny(r.ProfileExclude, de.Name()) {
			continue
		}
		dir := filepath.Join(base, de.Name())
		if e, err := filesystem.Lstat(dir); err != nil || e.Reparse {
			continue
		}
		if _, err := filesystem.Lstat(filepath.Join(dir, r.ProfileMarker)); err != nil {
			continue
		}
		p, err := safety.Normalize(dir + suffix)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// resolveRoot proves a concrete root location is safe to clean. It returns
// StatusEmpty when the location does not exist, another status with a reason
// when it must not be cleaned, or "" with the validated root.
func resolveRoot(env *Env, p string) (string, Status, string) {
	e, err := filesystem.Lstat(p)
	switch {
	case errors.Is(err, filesystem.ErrGone):
		return "", StatusEmpty, ""
	case err != nil:
		return "", StatusSkipped, fmt.Sprintf("cannot read %s: %v", p, err)
	case e.Reparse:
		return "", StatusReview, fmt.Sprintf("%s is a link or junction; review required", p)
	case !e.IsDir():
		return "", StatusReview, fmt.Sprintf("%s is not a folder; review required", p)
	}
	final, err := filesystem.FinalPath(p)
	if err != nil {
		return "", StatusSkipped, fmt.Sprintf("cannot resolve %s: %v", p, err)
	}
	if safety.Key(final) != safety.Key(p) {
		return "", StatusReview, fmt.Sprintf("%s resolves to %s; review required", p, final)
	}
	valid, err := env.Guard.ValidateRoot(final)
	if err != nil {
		return "", StatusReview, err.Error()
	}
	return valid, "", ""
}

func scanRoot(ctx context.Context, env *Env, r *Rule, rs *RuleScan, root string, cutoff time.Time, prog *Progress) error {
	prefixLen := len(root) + 1
	if strings.HasSuffix(root, `\`) {
		prefixLen = len(root)
	}
	return filesystem.Walk(ctx, root, func(e filesystem.Entry) bool {
		if len(e.Path) <= prefixLen {
			return false
		}
		depth := strings.Count(e.Path[prefixLen:], `\`) + 1
		if matchAny(r.Exclude, e.Name) {
			return false
		}
		if e.Reparse {
			rs.Skipped.Add(e.Path, reasonReparse)
			return false
		}
		if e.Cloud {
			rs.Skipped.Add(e.Path, reasonCloud)
			return false
		}
		if e.IsDir() {
			descend := r.MaxDepth == 0 || depth < r.MaxDepth
			if !descend {
				return false
			}
			d := env.Guard.Check(safety.Request{Path: e.Path, Purpose: safety.PurposeCleanup, Scope: root})
			switch d.Class {
			case safety.ClassProtected:
				rs.Skipped.Add(e.Path, reasonWhitelist)
				return false
			case safety.ClassSensitive:
				rs.Skipped.Add(e.Path, reasonSensitive)
				return false
			}
			if d.Allowed && len(r.Include) == 0 && !e.Fingerprint.CreationTime().After(cutoff) {
				rs.Dirs = append(rs.Dirs, Item{Path: e.Path, Fingerprint: e.Fingerprint, scope: root})
			}
			return true
		}
		if len(r.Include) > 0 && !matchAny(r.Include, e.Name) {
			return false
		}
		if r.MinAge > 0 && e.Fingerprint.Newest().After(cutoff) {
			rs.KeptRecent++
			rs.KeptRecentBytes += e.Size()
			return false
		}
		if d := env.Guard.Check(safety.Request{Path: e.Path, Purpose: safety.PurposeCleanup, Scope: root}); !d.Allowed {
			switch d.Class {
			case safety.ClassProtected:
				rs.Skipped.Add(e.Path, reasonWhitelist)
				return false
			case safety.ClassSensitive:
				rs.Skipped.Add(e.Path, reasonSensitive)
				return false
			}
			slog.Warn("guard refused scan candidate", "rule", r.ID, "path", e.Path, "reason", d.Reason)
			rs.Skipped.Add(e.Path, reasonPolicy)
			return false
		}
		rs.Files = append(rs.Files, Item{Path: e.Path, Fingerprint: e.Fingerprint, scope: root})
		rs.Bytes += e.Size()
		prog.add(1, e.Size())
		return false
	}, func(path string, err error) {
		rs.Skipped.Add(path, "could not read folder: "+err.Error())
	})
}

func anyExists(env *Env, templates []string) bool {
	for _, t := range templates {
		p, err := env.Locations.Expand(t)
		if err != nil {
			continue
		}
		if _, err := filesystem.Lstat(p); err == nil {
			return true
		}
	}
	return false
}
