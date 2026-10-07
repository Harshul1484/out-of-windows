package purge

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// ReasonCount is a skip reason with its count.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// How an artifact was removed.
const (
	MethodDeleted  = "deleted"  // permanently, file by file (preselected artifacts)
	MethodRecycled = "recycled" // as a whole folder to the Recycle Bin (added from review)
)

// ArtifactResult is what happened to one artifact.
type ArtifactResult struct {
	// Kept explains why the whole folder was left alone at deletion time.
	Kept string `json:"kept,omitempty"`
	// Method is MethodDeleted or MethodRecycled ("" when kept).
	Method        string        `json:"method,omitempty"`
	RemovedFiles  int           `json:"removed_files"`
	RemovedDirs   int           `json:"removed_dirs"`
	Reclaimed     int64         `json:"reclaimed_bytes"`
	RecycledFiles int           `json:"recycled_files"`
	RecycledBytes int64         `json:"recycled_bytes"`
	Complete      bool          `json:"complete"`
	Skipped       int           `json:"skipped"`
	SkipReasons   []ReasonCount `json:"skip_reasons"`
	Errors        int           `json:"errors"`
}

// Outcome is the result of Remove.
type Outcome struct {
	Artifacts     []*Artifact
	Removed       int // artifacts deleted completely
	RemovedFiles  int
	Reclaimed     int64
	Recycled      int // artifacts moved to the Recycle Bin
	RecycledBytes int64
	Skipped       int // files and folders left in place
	Errors        int
	Cancelled     bool
	Duration      time.Duration
	FreeBefore    map[string]uint64
	FreeAfter     map[string]uint64
}

// FreedOnDisk is the measured increase in free space on the volumes
// involved (approximate when other programs write at the same time).
func (o *Outcome) FreedOnDisk() int64 {
	var n int64
	for v, before := range o.FreeBefore {
		if after, ok := o.FreeAfter[v]; ok {
			n += int64(after) - int64(before)
		}
	}
	return n
}

// Skip reasons.
const (
	reasonGone     = "already removed"
	reasonInUse    = "in use by another program"
	reasonDenied   = "permission denied"
	reasonChanged  = "changed since it was scanned"
	reasonLink     = "link or junction (not followed)"
	reasonCloud    = "cloud placeholder (not stored locally)"
	reasonPolicy   = "refused by safety policy"
	reasonFence    = "outside the sandbox fence"
	reasonNotEmpty = "folder not empty"
)

// Remove removes the chosen artifacts. Each artifact is checked again first:
// it must be the folder that was scanned, the guard must accept it, a fresh
// walk must find no Git repository, link, cloud-only or sensitive file and
// nothing changed since the scan, and Git must still report no tracked files.
//
// Preselected artifacts (StatusReady: full evidence) are then deleted
// permanently: every file through filesystem.RemoveVerified (identity, link
// and final-path checks through the deleting handle, the guard with the
// artifact as scope), and folders deepest first. Artifacts the user added
// from review (StatusReview: weaker evidence or recent activity) are moved
// to the Recycle Bin as a whole folder through filesystem.RecycleVerified
// with r; without a Recycle Bin they are kept, never deleted. Ctrl+C stops
// after the items in progress.
func Remove(ctx context.Context, env *Env, res *Result, chosen []*Artifact, r filesystem.Recycler, prog *Progress) *Outcome {
	start := time.Now()
	out := &Outcome{FreeBefore: map[string]uint64{}, FreeAfter: map[string]uint64{}}
	volumes := map[string]bool{}
	for _, a := range chosen {
		if v := filesystem.VolumeOf(a.Path); v != "" {
			volumes[v] = true
		}
	}
	for v := range volumes {
		if free, err := filesystem.FreeSpace(v); err == nil {
			out.FreeBefore[v] = free
		}
	}
	for _, a := range chosen {
		if ctx.Err() != nil {
			out.Cancelled = true
			break
		}
		if a.Status == StatusKept {
			continue
		}
		if prog != nil {
			prog.set(a.Path)
		}
		ar := removeOne(ctx, env, res.Started, a, r, prog)
		a.Result = ar
		out.Artifacts = append(out.Artifacts, a)
		switch {
		case ar.Method == MethodRecycled:
			out.Recycled++
			out.RecycledBytes += ar.RecycledBytes
		case ar.Complete:
			out.Removed++
		}
		out.RemovedFiles += ar.RemovedFiles
		out.Reclaimed += ar.Reclaimed
		out.Skipped += ar.Skipped
		out.Errors += ar.Errors
		if ctx.Err() != nil {
			out.Cancelled = true
			break
		}
	}
	for v := range volumes {
		if free, err := filesystem.FreeSpace(v); err == nil {
			out.FreeAfter[v] = free
		}
	}
	out.Duration = time.Since(start)
	return out
}

func removeOne(ctx context.Context, env *Env, scanned time.Time, a *Artifact, recycler filesystem.Recycler, prog *Progress) *ArtifactResult {
	r := &ArtifactResult{SkipReasons: []ReasonCount{}}
	keep := func(why string) *ArtifactResult {
		r.Kept = why
		slog.Info("purge kept artifact", "path", a.Path, "reason", why)
		return r
	}

	// The same folder that was scanned, still acceptable to the guard?
	e, err := filesystem.Lstat(a.Path)
	switch {
	case errors.Is(err, filesystem.ErrGone):
		return keep(reasonGone)
	case err != nil:
		return keep(err.Error())
	case e.Reparse:
		return keep("it is now a link or junction")
	case !e.IsDir() || e.Fingerprint.Created != a.fingerprint.Created:
		return keep(reasonChanged)
	}

	// A fresh walk: nothing new, nothing that must be kept.
	m := walkArtifact(ctx, a.Path, a.kind, true, nil)
	if m.cancelled {
		return keep("cancelled before it was checked")
	}
	if reasons := keepReasons(env, a, m); len(reasons) > 0 {
		return keep(reasons[0])
	}
	if m.newest.After(scanned) {
		return keep(reasonChanged + " (files were added or modified)")
	}
	if a.repo != "" {
		rel, err := relSlash(a.repo, a.Path)
		if err != nil {
			return keep(err.Error())
		}
		tracked, err := env.git().Tracked(ctx, a.repo, []string{rel})
		switch {
		case err != nil:
			return keep(gitFailure(err))
		case tracked[rel]:
			return keep("contains files tracked by Git")
		}
	}

	// Added from review: the whole folder goes to the Recycle Bin, or stays.
	if a.Status != StatusReady {
		if recycler == nil {
			return keep("no Recycle Bin is available")
		}
		err := filesystem.RecycleVerified(a.Path, e.Fingerprint, purgeCheck(env.Guard, a.Path, true), recycler)
		switch {
		case err == nil:
			r.Method, r.Complete = MethodRecycled, true
			r.RecycledFiles, r.RecycledBytes = m.files, m.bytes
			if prog != nil {
				prog.Files.Add(int64(m.files))
				prog.Bytes.Add(m.bytes)
			}
			return r
		case errors.Is(err, filesystem.ErrNoRecycleBin):
			return keep("the drive has no Recycle Bin")
		}
		reason, isErr := classify(err)
		if isErr {
			r.Errors++
			slog.Error("purge: moving to the Recycle Bin failed", "path", a.Path, "err", err)
		}
		return keep(reason)
	}
	r.Method = MethodDeleted

	var files, dirs []filesystem.Entry
	for _, x := range m.entries {
		if x.IsDir() {
			dirs = append(dirs, x)
		} else {
			files = append(files, x)
		}
	}
	reasons := map[string]int{}
	var mu sync.Mutex
	skip := func(path string, err error) {
		reason, isErr := classify(err)
		mu.Lock()
		reasons[reason]++
		r.Skipped++
		if isErr {
			r.Errors++
		}
		mu.Unlock()
		if isErr {
			slog.Error("purge: remove failed", "path", path, "err", err)
		} else {
			slog.Debug("purge: skipped", "path", path, "reason", reason)
		}
	}

	// Files, in parallel.
	work := make(chan filesystem.Entry)
	var wg sync.WaitGroup
	fileCheck := purgeCheck(env.Guard, a.Path, false)
	for i := 0; i < env.workers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				if err := filesystem.RemoveVerified(f.Path, f.Fingerprint, fileCheck); err != nil {
					skip(f.Path, err)
					continue
				}
				mu.Lock()
				r.RemovedFiles++
				r.Reclaimed += f.Size()
				mu.Unlock()
				if prog != nil {
					prog.Files.Add(1)
					prog.Bytes.Add(f.Size())
				}
			}
		}()
	}
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		work <- f
	}
	close(work)
	wg.Wait()

	// Folders, deepest first, then the artifact itself (removed only when
	// empty and only if it is still the folder that was scanned).
	if ctx.Err() == nil {
		sort.SliceStable(dirs, func(i, j int) bool {
			return strings.Count(dirs[i].Path, `\`) > strings.Count(dirs[j].Path, `\`)
		})
		dirCheck := purgeCheck(env.Guard, a.Path, true)
		for _, d := range append(dirs, filesystem.Entry{Path: a.Path, Fingerprint: e.Fingerprint}) {
			if ctx.Err() != nil {
				break
			}
			err := filesystem.RemoveVerified(d.Path, d.Fingerprint, dirCheck)
			switch {
			case err == nil:
				r.RemovedDirs++
				if d.Path == a.Path {
					r.Complete = true
				}
			case errors.Is(err, filesystem.ErrNotEmpty), errors.Is(err, filesystem.ErrGone):
				// Kept because something inside was kept.
			default:
				skip(d.Path, err)
			}
		}
	}
	for reason, n := range reasons {
		r.SkipReasons = append(r.SkipReasons, ReasonCount{reason, n})
	}
	sort.Slice(r.SkipReasons, func(i, j int) bool {
		if r.SkipReasons[i].Count != r.SkipReasons[j].Count {
			return r.SkipReasons[i].Count > r.SkipReasons[j].Count
		}
		return r.SkipReasons[i].Reason < r.SkipReasons[j].Reason
	})
	return r
}

// purgeCheck re-runs the guard on the OS-resolved final path with the
// artifact folder as scope.
func purgeCheck(g *safety.Guard, scope string, dir bool) filesystem.CheckFunc {
	return func(final string) error {
		d := g.Check(safety.Request{Path: final, Purpose: safety.PurposePurge, Scope: scope, Dir: dir})
		if !d.Allowed {
			return errors.New(d.Reason)
		}
		return nil
	}
}

// classify maps a removal error to a reason; isError marks unexpected
// failures as opposed to expected, safe skips.
func classify(err error) (reason string, isError bool) {
	switch {
	case errors.Is(err, filesystem.ErrGone):
		return reasonGone, false
	case errors.Is(err, filesystem.ErrInUse):
		return reasonInUse, false
	case errors.Is(err, filesystem.ErrAccessDenied):
		return reasonDenied, false
	case errors.Is(err, filesystem.ErrChanged):
		return reasonChanged, false
	case errors.Is(err, filesystem.ErrReparsePoint):
		return reasonLink, false
	case errors.Is(err, filesystem.ErrCloudFile):
		return reasonCloud, false
	case errors.Is(err, filesystem.ErrNotEmpty):
		return reasonNotEmpty, false
	case errors.Is(err, filesystem.ErrOutsideFence):
		return reasonFence, false
	case errors.Is(err, filesystem.ErrPolicy):
		slog.Warn("safety policy refused purge deletion", "err", err)
		return reasonPolicy, false
	}
	return err.Error(), true
}
