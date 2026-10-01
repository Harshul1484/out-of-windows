package leftovers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Confidence says how strong the ownership evidence is.
type Confidence string

const (
	High   Confidence = "high"
	Medium Confidence = "medium"
)

// Candidate is a folder believed to be left behind by an app.
type Candidate struct {
	Path        string                 `json:"path"`
	App         string                 `json:"app"`
	Source      string                 `json:"source"`
	Location    string                 `json:"location"` // kind of root, e.g. "app data"
	Confidence  Confidence             `json:"confidence"`
	Reasons     []string               `json:"reasons"`
	Bytes       int64                  `json:"bytes"`
	Files       int                    `json:"files"`
	Newest      time.Time              `json:"newest_change"`
	NeedsAdmin  bool                   `json:"needs_admin"`
	Root        string                 `json:"-"`
	Fingerprint filesystem.Fingerprint `json:"-"`
	signals     int
	exact       bool
}

// Kept is a folder that matched an app but is not offered, with the reason.
type Kept struct {
	Path   string `json:"path"`
	App    string `json:"app"`
	Reason string `json:"reason"`
}

// Result is the outcome of Find.
type Result struct {
	Candidates []Candidate `json:"candidates"`
	Kept       []Kept      `json:"kept"`
}

// Bytes is the total size of all candidates.
func (r *Result) Bytes() int64 {
	var n int64
	for _, c := range r.Candidates {
		n += c.Bytes
	}
	return n
}

// Env is what Find needs about the machine.
type Env struct {
	Guard    *safety.Guard
	Claims   *Claims
	Elevated bool
	Now      func() time.Time
	// RecentActivity keeps usage-trace folders changed more recently than this.
	RecentActivity time.Duration
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Find looks for leftovers of the given apps. It never modifies anything.
func Find(ctx context.Context, env *Env, evidences []Evidence) *Result {
	res := &Result{}
	found := map[string]*Candidate{}
	var order []string
	add := func(ev Evidence, path string, root safety.LeftoverRoot, reason string, signals int, exact bool) {
		k := safety.Key(path)
		c := found[k]
		if c == nil {
			c = &Candidate{Path: path, App: ev.Name, Source: ev.Source, Location: root.Kind, Root: root.Path}
			found[k] = c
			order = append(order, k)
		}
		c.Reasons = appendUnique(c.Reasons, reason)
		c.signals += signals
		c.exact = c.exact || exact
	}

	for _, ev := range evidences {
		if ctx.Err() != nil {
			break
		}
		keys := ev.keys()
		pub := ev.publisherKey()
		installKey := ""
		if ev.InstallLocation != "" {
			installKey = apps.NormalizeName(filepath.Base(strings.TrimRight(ev.InstallLocation, `\/`)))
		}

		// The install folder the uninstall entry recorded.
		if loc, err := safety.Normalize(ev.InstallLocation); err == nil && ev.InstallLocation != "" && isDir(loc) {
			if root, ok := env.Guard.LeftoverRootFor(loc); ok {
				reason := "install folder of " + ev.Name + " is still present"
				if ev.Source == SourceTrace {
					reason = ev.Name + " ran from this folder; its program file is gone"
				}
				add(ev, loc, root, reason, 2, ev.Source != SourceTrace)
			}
		}
		if len(keys) == 0 {
			continue
		}

		for _, root := range env.Guard.LeftoverRoots() {
			for _, child := range subdirs(root.Path) {
				name := filepath.Base(child)
				n := apps.NormalizeName(name)
				if contains(keys, n) {
					signals := 1
					reason := fmt.Sprintf("folder name matches %s", ev.Name)
					if n == installKey {
						signals++
					}
					if containsExe(child, ev.Exes) {
						signals++
						reason += " and it contains " + strings.Join(ev.Exes, ", ")
					}
					add(ev, child, root, reason, signals, false)
				}
				if pub != "" && apps.NormalizePublisher(name) == pub {
					// Inside the publisher's own folder a short product name
					// ("Contoso\Studio") is specific enough.
					inner := append([]string(nil), keys...)
					for _, k := range []string{installKey, strings.TrimPrefix(apps.NormalizeName(ev.Name), pub)} {
						if len(k) >= 3 {
							inner = append(inner, k)
						}
					}
					for _, g := range subdirs(child) {
						if gn := apps.NormalizeName(filepath.Base(g)); contains(inner, gn) {
							add(ev, g, root, fmt.Sprintf("%s folder inside the publisher's folder %s", ev.Name, name), 2, false)
						}
					}
				}
			}
		}
	}

	// Drop candidates inside other candidates; the outer one covers them.
	sort.Strings(order)
	var outer []string
	for _, k := range order {
		nested := false
		for _, o := range outer {
			if safety.IsStrictlyWithin(found[k].Path, found[o].Path) {
				nested = true
				break
			}
		}
		if !nested {
			outer = append(outer, k)
		}
	}

	for _, k := range outer {
		c := found[k]
		if kept, why := env.qualify(ctx, c); !kept {
			res.Candidates = append(res.Candidates, *c)
		} else {
			res.Kept = append(res.Kept, Kept{Path: c.Path, App: c.App, Reason: why})
		}
	}
	sort.SliceStable(res.Candidates, func(i, j int) bool {
		a, b := res.Candidates[i], res.Candidates[j]
		if a.App != b.App {
			return strings.ToLower(a.App) < strings.ToLower(b.App)
		}
		return a.Path < b.Path
	})
	return res
}

// qualify applies the safety guard, claims, content and activity checks and
// fills size, confidence and identity. It returns kept=true with a reason when
// the folder must not be offered.
func (env *Env) qualify(ctx context.Context, c *Candidate) (kept bool, reason string) {
	d := env.Guard.Check(safety.Request{Path: c.Path, Purpose: safety.PurposeLeftover, Scope: c.Root})
	if !d.Allowed {
		return true, "protected: " + d.Reason
	}
	if owner, ok := env.Claims.Owner(c.Path, apps.NormalizeName(filepath.Base(c.Path))); ok {
		return true, "still used by " + owner
	}
	e, err := filesystem.Lstat(c.Path)
	if err != nil || e.Reparse || !e.IsDir() {
		return true, "not a regular folder"
	}
	c.Fingerprint = e.Fingerprint
	// Activity is judged by files only: a folder's own timestamp changes
	// whenever something is removed from it (for example by the uninstaller).
	var sensitive string
	err = filesystem.Walk(ctx, c.Path, func(x filesystem.Entry) bool {
		if x.Reparse {
			return false
		}
		if !x.IsDir() {
			if t := x.Fingerprint.Newest(); t.After(c.Newest) {
				c.Newest = t
			}
			c.Bytes += x.Size()
			c.Files++
			if sensitive == "" && safety.IsSensitiveName(x.Name) {
				sensitive = x.Name
			}
		}
		return true
	}, nil)
	if c.Files == 0 {
		c.Newest = e.Fingerprint.ModTime()
	}
	if err != nil {
		return true, "could not be read completely: " + err.Error()
	}
	if sensitive != "" {
		return true, "contains " + sensitive + ", which may be a key, credential store or disk image"
	}
	if c.Source == SourceTrace && env.RecentActivity > 0 && c.Newest.After(env.now().Add(-env.RecentActivity)) {
		return true, "changed recently, so it may still be in use"
	}
	c.Confidence = Medium
	if c.Source != SourceTrace && (c.exact || c.signals >= 2) {
		c.Confidence = High
	}
	if root, ok := env.Guard.LeftoverRootFor(c.Path); ok && root.Admin && !env.Elevated {
		c.NeedsAdmin = true
	}
	return false, ""
}

func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if x, err := filesystem.Lstat(p); err == nil && !x.Reparse {
			if n, err := safety.Normalize(p); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func isDir(p string) bool {
	e, err := filesystem.Lstat(p)
	return err == nil && e.IsDir() && !e.Reparse
}

func containsExe(dir string, exes []string) bool {
	for _, exe := range exes {
		for _, p := range []string{filepath.Join(dir, exe), filepath.Join(dir, "bin", exe)} {
			if apps.FileExists(p) {
				return true
			}
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
