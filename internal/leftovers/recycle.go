package leftovers

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Skipped is a candidate that was not moved, with the reason.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Outcome is what Recycle did.
type Outcome struct {
	Recycled []Candidate `json:"recycled"`
	Bytes    int64       `json:"bytes"`
	Skipped  []Skipped   `json:"skipped"`
	Errors   int         `json:"errors"`
	// RecycledShortcuts are the broken shortcuts moved with their folders.
	RecycledShortcuts []string `json:"recycled_shortcuts"`
}

// Recycle moves the candidates to the Recycle Bin after re-verifying each
// one (identity, links, final path, safety guard). Afterwards, publisher
// folders left empty are removed too, and the removable broken shortcuts of
// each folder that was moved follow it, each re-checked first.
func Recycle(ctx context.Context, env *Env, cands []Candidate, r filesystem.Recycler) *Outcome {
	out := &Outcome{RecycledShortcuts: []string{}}
	for _, c := range cands {
		if ctx.Err() != nil {
			out.Skipped = append(out.Skipped, Skipped{c.Path, "cancelled"})
			continue
		}
		if c.NeedsAdmin && !env.Elevated {
			out.Skipped = append(out.Skipped, Skipped{c.Path, "needs administrator rights"})
			continue
		}
		check := func(final string) error {
			d := env.Guard.Check(safety.Request{Path: final, Purpose: safety.PurposeLeftover, Scope: c.Root})
			if !d.Allowed {
				return errors.New(d.Reason)
			}
			return nil
		}
		err := filesystem.RecycleVerified(c.Path, c.Fingerprint, check, r)
		if err != nil {
			out.skip(c.Path, err)
			continue
		}
		out.Recycled = append(out.Recycled, c)
		out.Bytes += c.Bytes
		removeEmptyParent(c, check)
		for _, sc := range c.Shortcuts {
			if !sc.Removable {
				continue
			}
			if err := recycleShortcut(env, sc, r); err != nil {
				out.skip(sc.Path, err)
				continue
			}
			out.RecycledShortcuts = append(out.RecycledShortcuts, sc.Path)
		}
	}
	return out
}

// skip records why path was not moved; unexpected errors are counted.
func (out *Outcome) skip(path string, err error) {
	reason := ""
	switch {
	case errors.Is(err, filesystem.ErrGone):
		reason = "already removed"
	case errors.Is(err, filesystem.ErrChanged):
		reason = "changed since it was scanned"
	case errors.Is(err, filesystem.ErrInUse):
		reason = "in use by another program"
	case errors.Is(err, filesystem.ErrAccessDenied):
		reason = "permission denied"
	case errors.Is(err, filesystem.ErrNoRecycleBin):
		reason = "the drive has no Recycle Bin"
	case errors.Is(err, filesystem.ErrReparsePoint), errors.Is(err, filesystem.ErrPolicy), errors.Is(err, filesystem.ErrOutsideFence):
		reason = "refused by safety policy"
	case errors.Is(err, errShortcutKept):
		reason = "left alone: it could not be checked again"
	default:
		reason = err.Error()
		out.Errors++
	}
	out.Skipped = append(out.Skipped, Skipped{path, reason})
}

// removeEmptyParent removes a publisher folder (e.g. Program Files\Contoso)
// that the recycled candidate leaves empty.
func removeEmptyParent(c Candidate, check filesystem.CheckFunc) {
	parent := filepath.Dir(c.Path)
	if safety.Key(parent) == safety.Key(c.Root) {
		return
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		return
	}
	if e, err := filesystem.Lstat(parent); err == nil && e.IsDir() && !e.Reparse {
		_ = filesystem.RemoveVerified(parent, e.Fingerprint, check)
	}
}
