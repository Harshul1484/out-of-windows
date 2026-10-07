// Package repair turns doctor findings into user-level, reversible fixes:
// removing missing, duplicate and empty entries from the current user's PATH
// (after backing up the old value), and disabling startup entries whose
// program no longer exists (through StartupApproved, like Task Manager). It
// never changes the machine PATH or anything else machine-wide except
// startup approvals, which need administrator rights.
package repair

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/startup"
)

// Kind is what a fix changes.
type Kind string

// Kinds.
const (
	KindPath    Kind = "user-path-entry"
	KindStartup Kind = "startup-entry"
)

// Fix is one proposed change.
type Fix struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Title string `json:"title"`
	// Target is the PATH entry as stored, or the startup entry ID.
	Target string `json:"target"`
	Reason string `json:"reason"`
	// Selected is whether the fix is preselected; --yes applies exactly
	// the selected fixes.
	Selected bool `json:"selected"`
	// Review explains why a fix is not preselected.
	Review     string `json:"review,omitempty"`
	NeedsAdmin bool   `json:"needs_admin"`

	pathIndex int
	entry     startup.Entry
}

// Plan is every fix repair can offer, plus findings it will not change.
type Plan struct {
	Fixes    []Fix    `json:"fixes"`
	NotFixed []string `json:"not_fixed"`
	userPath envpath.Report
}

// NewPlan builds fixes from the PATH reports and startup entries.
func NewPlan(user, machine *envpath.Report, entries []startup.Entry, elevated bool) Plan {
	p := Plan{Fixes: []Fix{}, NotFixed: []string{}}
	if user != nil {
		p.userPath = *user
		switch {
		case user.Error != "":
			p.NotFixed = append(p.NotFixed, "Your PATH could not be read: "+user.Error)
		case user.Quoted && user.Issues() > 0:
			p.NotFixed = append(p.NotFixed, "Your PATH contains quoted entries, which may hide a \";\"; edit it by hand "+
				"(Settings > System > About > Advanced system settings > Environment Variables).")
		default:
			for _, e := range user.Entries {
				if e.Problem == envpath.ProblemNone {
					continue
				}
				f := Fix{ID: "path.user:" + strconv.Itoa(e.Index), Kind: KindPath, Target: e.Raw, Selected: true, pathIndex: e.Index}
				switch e.Problem {
				case envpath.ProblemEmpty:
					f.Title, f.Reason = "Remove an empty entry from your PATH", "empty entry"
				case envpath.ProblemDuplicate:
					f.Title = "Remove duplicate " + e.Raw + " from your PATH"
					f.Reason = fmt.Sprintf("the same folder is already entry %d, which Windows searches first", e.DuplicateOf+1)
				case envpath.ProblemMissing:
					f.Title, f.Reason = "Remove missing "+e.Raw+" from your PATH", "the folder does not exist"
					if e.Review {
						f.Selected, f.Review = false, "it is inside your profile; tools often add a folder like this before creating it"
					}
				}
				p.Fixes = append(p.Fixes, f)
			}
		}
	}
	if machine != nil && machine.Issues() > 0 {
		p.NotFixed = append(p.NotFixed, fmt.Sprintf("The system PATH has %d missing, %d duplicate and %d empty entries. "+
			"%s repair never changes it: edit it as administrator (System Properties > Environment Variables).",
			machine.Missing, machine.Duplicates, machine.Empty, buildinfo.Name))
	}
	for _, e := range entries {
		if e.State != startup.Enabled || !e.Broken() || !e.Toggleable {
			continue
		}
		f := Fix{ID: "startup:" + e.ID, Kind: KindStartup, Title: "Disable startup entry " + e.Name, Target: e.ID,
			Reason: "its program is missing: " + e.Target, Selected: true, NeedsAdmin: e.NeedsAdmin, entry: e}
		if e.NeedsAdmin && !elevated {
			f.Selected, f.Review = false, startup.ReasonNeedsAdmin
		}
		p.Fixes = append(p.Fixes, f)
	}
	return p
}

// Env is what Apply changes things through.
type Env struct {
	Paths     envpath.Store
	Startup   startup.Store
	BackupDir string
	Elevated  bool
	Now       time.Time
}

// FixResult is the outcome of one fix.
type FixResult struct {
	Fix    Fix    `json:"fix"`
	Status string `json:"status"` // fixed, skipped, failed
	Reason string `json:"reason,omitempty"`
}

// Result statuses.
const (
	Fixed   = "fixed"
	Skipped = "skipped"
	Failed  = "failed"
)

// Outcome is what Apply did.
type Outcome struct {
	Results []FixResult `json:"results"`
	// Backup is the .reg file holding the user PATH before the change.
	Backup         string `json:"backup,omitempty"`
	BroadcastError string `json:"broadcast_error,omitempty"`
	Fixed          int    `json:"fixed"`
	Skipped        int    `json:"skipped"`
	Failed         int    `json:"failed"`
}

// Apply performs the chosen fixes. The user PATH is changed in one write
// after a verified backup, and only if it is still exactly the value that was
// analyzed; startup entries are disabled and verified one by one.
func Apply(ctx context.Context, env Env, plan Plan, chosen []Fix) Outcome {
	out := Outcome{Results: []FixResult{}}
	var pathFixes, startupFixes []Fix
	for _, f := range chosen {
		switch f.Kind {
		case KindPath:
			pathFixes = append(pathFixes, f)
		case KindStartup:
			startupFixes = append(startupFixes, f)
		}
	}
	if len(pathFixes) > 0 {
		applyPath(ctx, env, plan, pathFixes, &out)
	}
	if len(startupFixes) > 0 {
		var targets []startup.Entry
		byID := map[string]Fix{}
		for _, f := range startupFixes {
			targets = append(targets, f.entry)
			byID[f.entry.ID] = f
		}
		for _, r := range startup.SetEnabled(ctx, env.Startup, targets, false, env.Elevated, env.Now) {
			fr := FixResult{Fix: byID[r.Entry.ID], Reason: r.Reason}
			switch r.Status {
			case startup.StatusChanged:
				fr.Status = Fixed
			case startup.StatusUnchanged:
				fr.Status, fr.Reason = Skipped, "already disabled"
			case startup.StatusSkipped:
				fr.Status = Skipped
			default:
				fr.Status = Failed
			}
			out.Results = append(out.Results, fr)
		}
	}
	for _, r := range out.Results {
		switch r.Status {
		case Fixed:
			out.Fixed++
		case Skipped:
			out.Skipped++
		default:
			out.Failed++
		}
	}
	return out
}

func applyPath(ctx context.Context, env Env, plan Plan, fixes []Fix, out *Outcome) {
	all := func(status, reason string) {
		for _, f := range fixes {
			out.Results = append(out.Results, FixResult{Fix: f, Status: status, Reason: reason})
		}
	}
	if ctx.Err() != nil {
		all(Skipped, "cancelled")
		return
	}
	current, err := env.Paths.Read(envpath.User)
	if err != nil {
		all(Failed, "could not read your PATH: "+err.Error())
		return
	}
	if current != plan.userPath.Value {
		all(Skipped, "your PATH changed after it was checked; run "+buildinfo.Name+" repair again")
		return
	}
	backup, err := envpath.WriteBackup(env.BackupDir, current, env.Now)
	if err != nil {
		all(Failed, "nothing was changed: the old PATH could not be backed up: "+err.Error())
		return
	}
	out.Backup = backup
	var idx []int
	for _, f := range fixes {
		idx = append(idx, f.pathIndex)
	}
	sort.Ints(idx)
	updated := envpath.Remove(current, idx)
	if got, want := len(envpath.Split(updated.Raw)), len(envpath.Split(current.Raw))-len(idx); got != want && updated.Raw != "" {
		all(Failed, "internal check failed: unexpected number of entries; nothing was changed")
		return
	}
	if err := env.Paths.WriteUser(current, updated); err != nil {
		if errors.Is(err, envpath.ErrChanged) {
			all(Skipped, "your PATH changed after it was checked; run "+buildinfo.Name+" repair again")
		} else {
			all(Failed, "could not write your PATH: "+err.Error())
		}
		return
	}
	if err := env.Paths.Broadcast(); err != nil {
		out.BroadcastError = err.Error()
	}
	if back, err := env.Paths.Read(envpath.User); err != nil || back != updated {
		all(Failed, "your PATH was written but reads back differently; restore it from "+backup)
		return
	}
	all(Fixed, "")
}

// Summary describes the outcome in one line.
func (o Outcome) Summary() string {
	parts := []string{fmt.Sprintf("%d fixed", o.Fixed)}
	if o.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", o.Skipped))
	}
	if o.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", o.Failed))
	}
	return strings.Join(parts, ", ")
}
