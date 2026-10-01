package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Skip reasons shown to users and grouped in reports.
const (
	reasonReparse   = "link or junction (not followed)"
	reasonCloud     = "cloud placeholder (not stored locally)"
	reasonPolicy    = "refused by safety policy"
	reasonWhitelist = "protected by your whitelist"
	reasonGone      = "already removed"
	reasonInUse     = "in use by another program"
	reasonDenied    = "permission denied"
	reasonChanged   = "changed since it was scanned"
	reasonFence     = "outside the sandbox fence"
	reasonNotEmpty  = "folder not empty"
)

// SkippedItem is one example of a skipped path.
type SkippedItem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// maxSamples bounds how many example paths a Tally keeps.
const maxSamples = 25

// Tally counts skipped items by reason and keeps a few examples.
type Tally struct {
	counts  map[string]int
	samples []SkippedItem
}

// Add records one skipped path.
func (t *Tally) Add(path, reason string) {
	if t.counts == nil {
		t.counts = map[string]int{}
	}
	t.counts[reason]++
	if len(t.samples) < maxSamples {
		t.samples = append(t.samples, SkippedItem{Path: path, Reason: reason})
	}
}

// Total returns the number of skipped items.
func (t *Tally) Total() int {
	n := 0
	for _, c := range t.counts {
		n += c
	}
	return n
}

// ReasonCount is a reason with its count.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Reasons returns counts sorted by frequency.
func (t *Tally) Reasons() []ReasonCount {
	out := make([]ReasonCount, 0, len(t.counts))
	for r, c := range t.counts {
		out = append(out, ReasonCount{r, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// Samples returns example skipped paths.
func (t *Tally) Samples() []SkippedItem { return t.samples }

// RuleOutcome is what actually happened for one rule.
type RuleOutcome struct {
	Rule        *Rule
	Removed     int
	DirsRemoved int
	Reclaimed   int64
	Skipped     Tally
	Errors      int
}

// Outcome is the result of Execute.
type Outcome struct {
	Rules     []*RuleOutcome
	Removed   int
	Reclaimed int64
	Skipped   int
	Errors    int
	Cancelled bool
	Duration  time.Duration
	// FreeBefore/FreeAfter are measured free bytes per volume, so the report
	// can show the real change rather than only the sum of file sizes.
	FreeBefore map[string]uint64
	FreeAfter  map[string]uint64
}

// FreedOnDisk returns the measured increase in free space across volumes.
// Other programs writing at the same time make this approximate.
func (o *Outcome) FreedOnDisk() int64 {
	var n int64
	for v, before := range o.FreeBefore {
		if after, ok := o.FreeAfter[v]; ok {
			n += int64(after) - int64(before)
		}
	}
	return n
}

// Execute removes the items of the given ready rule scans. Every item is
// re-verified at deletion time (identity, link status, final path, safety
// policy). If ctx is cancelled it stops after the current item.
func Execute(ctx context.Context, env *Env, scans []*RuleScan, prog *Progress) *Outcome {
	start := time.Now()
	remove := env.Remove
	if remove == nil {
		remove = filesystem.RemoveVerified
	}
	out := &Outcome{FreeBefore: map[string]uint64{}, FreeAfter: map[string]uint64{}}

	volumes := map[string]bool{}
	for _, s := range scans {
		for _, r := range s.Roots {
			if v := filesystem.VolumeOf(r); v != "" {
				volumes[v] = true
			}
		}
	}
	for v := range volumes {
		if free, err := filesystem.FreeSpace(v); err == nil {
			out.FreeBefore[v] = free
		}
	}

	for _, s := range scans {
		if s.Status != StatusReady {
			continue
		}
		ro := &RuleOutcome{Rule: s.Rule}
		out.Rules = append(out.Rules, ro)
		prog.setCurrent(s.Rule.Name)

		for _, it := range s.Files {
			if ctx.Err() != nil {
				out.Cancelled = true
				break
			}
			err := remove(it.Path, it.Fingerprint, policyCheck(env.Guard, it.scope))
			if err == nil {
				ro.Removed++
				ro.Reclaimed += it.Fingerprint.Size
				prog.add(1, it.Fingerprint.Size)
				continue
			}
			reason, isErr := classify(err)
			ro.Skipped.Add(it.Path, reason)
			if isErr {
				ro.Errors++
				slog.Error("remove failed", "rule", s.Rule.ID, "path", it.Path, "err", err)
			} else {
				slog.Debug("skipped", "rule", s.Rule.ID, "path", it.Path, "reason", reason)
			}
		}
		if out.Cancelled {
			break
		}
		for _, d := range s.Dirs {
			if ctx.Err() != nil {
				out.Cancelled = true
				break
			}
			err := remove(d.Path, d.Fingerprint, policyCheck(env.Guard, d.scope))
			switch {
			case err == nil:
				ro.DirsRemoved++
			case errors.Is(err, filesystem.ErrNotEmpty), errors.Is(err, filesystem.ErrGone):
				// Kept because it still holds files we did not remove.
			default:
				reason, isErr := classify(err)
				ro.Skipped.Add(d.Path, reason)
				if isErr {
					ro.Errors++
				}
			}
		}
		if out.Cancelled {
			break
		}
	}

	for _, ro := range out.Rules {
		out.Removed += ro.Removed + ro.DirsRemoved
		out.Reclaimed += ro.Reclaimed
		out.Skipped += ro.Skipped.Total()
		out.Errors += ro.Errors
	}
	for v := range volumes {
		if free, err := filesystem.FreeSpace(v); err == nil {
			out.FreeAfter[v] = free
		}
	}
	out.Duration = time.Since(start)
	return out
}

// policyCheck re-runs the safety guard on the OS-resolved final path.
func policyCheck(g *safety.Guard, scope string) filesystem.CheckFunc {
	return func(final string) error {
		d := g.Check(safety.Request{Path: final, Purpose: safety.PurposeCleanup, Scope: scope})
		if !d.Allowed {
			return errors.New(d.Reason)
		}
		return nil
	}
}

// classify maps a removal error to a user-facing reason. isError is true for
// unexpected failures (as opposed to expected, safe skips).
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
		return reasonReparse, false
	case errors.Is(err, filesystem.ErrCloudFile):
		return reasonCloud, false
	case errors.Is(err, filesystem.ErrNotEmpty):
		return reasonNotEmpty, false
	case errors.Is(err, filesystem.ErrOutsideFence):
		return reasonFence, false
	case errors.Is(err, filesystem.ErrPolicy):
		slog.Warn("safety policy refused deletion", "err", err)
		return reasonPolicy, false
	}
	return err.Error(), true
}
