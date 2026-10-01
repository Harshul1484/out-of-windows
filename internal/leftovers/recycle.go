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
}

// Recycle moves the candidates to the Recycle Bin after re-verifying each
// one (identity, links, final path, safety guard). Afterwards, publisher
// folders left empty are removed too.
func Recycle(ctx context.Context, env *Env, cands []Candidate, r filesystem.Recycler) *Outcome {
	out := &Outcome{}
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
		switch {
		case err == nil:
			out.Recycled = append(out.Recycled, c)
			out.Bytes += c.Bytes
			removeEmptyParent(c, check)
		case errors.Is(err, filesystem.ErrGone):
			out.Skipped = append(out.Skipped, Skipped{c.Path, "already removed"})
		case errors.Is(err, filesystem.ErrChanged):
			out.Skipped = append(out.Skipped, Skipped{c.Path, "changed since it was scanned"})
		case errors.Is(err, filesystem.ErrInUse):
			out.Skipped = append(out.Skipped, Skipped{c.Path, "in use by another program"})
		case errors.Is(err, filesystem.ErrAccessDenied):
			out.Skipped = append(out.Skipped, Skipped{c.Path, "permission denied"})
		case errors.Is(err, filesystem.ErrNoRecycleBin):
			out.Skipped = append(out.Skipped, Skipped{c.Path, "the drive has no Recycle Bin"})
		case errors.Is(err, filesystem.ErrReparsePoint), errors.Is(err, filesystem.ErrPolicy), errors.Is(err, filesystem.ErrOutsideFence):
			out.Skipped = append(out.Skipped, Skipped{c.Path, "refused by safety policy"})
		default:
			out.Skipped = append(out.Skipped, Skipped{c.Path, err.Error()})
			out.Errors++
		}
	}
	return out
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
