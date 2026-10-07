package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/repair"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type repairOptions struct {
	dryRun bool
	yes    bool
	pause  bool
}

func newRepairCmd(app *App) *cobra.Command {
	var o repairOptions
	cmd := &cobra.Command{
		Use:     "repair",
		Short:   "Fix PATH and startup problems found by doctor (user-level, reversible)",
		GroupID: "system",
		Long: "Offer fixes for problems `" + buildinfo.Name + " doctor` finds that can be undone:\n\n" +
			"  - remove missing, duplicate and empty folders from your user PATH; the old value is\n" +
			"    saved first as a .reg file you can double-click to restore it\n" +
			"  - disable startup entries whose program no longer exists (like Task Manager; the\n" +
			"    entry stays registered and `" + buildinfo.Name + " startup enable` turns it back on)\n\n" +
			"The system PATH is never changed, only reported. Missing folders inside your profile\n" +
			"are not preselected: tools often add such a folder before creating it.",
		Example: "  " + buildinfo.Name + " repair --dry-run\n  " + buildinfo.Name + " repair --yes --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runRepair(cmd.Context(), app, o)
			pauseIf(app, o.pause, err)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show the fixes without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "apply the preselected fixes without asking (required for non-interactive use)")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	return cmd
}

func backupDir(app *App) string { return filepath.Join(app.Dirs.Data, "backups") }

func runRepair(ctx context.Context, app *App, o repairOptions) error {
	if app.ForceDryRun {
		o.dryRun = true
	}
	spin := ui.StartSpinner(app.Err, app.tty(), func() string { return "Checking PATH and startup entries" })
	user, machine := app.pathReports()
	entries, _, err := app.startupStore().List(ctx)
	spin.Stop()
	if ctx.Err() != nil {
		return errCancelled
	}
	if err != nil {
		return withCode(ExitError, "could not read startup entries: %v", err)
	}
	plan := repair.NewPlan(user, machine, entries, app.Elevated)
	doc := map[string]any{"schema": "oow.repair/v1", "dry_run": o.dryRun, "sandbox": app.Sandbox != "",
		"elevated": app.Elevated, "fixes": plan.Fixes, "not_fixed": plan.NotFixed}

	selected, admin := 0, 0
	for _, f := range plan.Fixes {
		if f.Selected {
			selected++
		}
		if f.NeedsAdmin && !app.Elevated {
			admin++
		}
	}
	if !app.JSON {
		app.header("Repair", o.dryRun)
		printFixes(app, plan)
	}
	if o.dryRun || len(plan.Fixes) == 0 {
		if app.JSON {
			return app.printJSON(doc)
		}
		if o.dryRun {
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was changed."))
		}
		return nil
	}

	var chosen []repair.Fix
	switch {
	case app.interactive() && !o.yes:
		var items []ui.CheckItem
		for _, f := range plan.Fixes {
			it := ui.CheckItem{Label: f.Title, Checked: f.Selected, Detail: []string{"Why    " + f.Reason}}
			if f.Review != "" {
				it.Detail = append(it.Detail, "Note   "+f.Review)
			}
			if f.NeedsAdmin && !app.Elevated {
				it.Disabled, it.Note, it.Checked = true, "needs administrator rights", false
			}
			items = append(items, it)
		}
		r, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Choose fixes", ConfirmVerb: "continue"}, items)
		if err != nil {
			return err
		}
		if !r.Confirmed {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
		for _, i := range r.Checked {
			chosen = append(chosen, plan.Fixes[i])
		}
		if len(chosen) == 0 {
			app.printf(" %s\n\n", ui.Muted.Render("Nothing selected. Nothing was changed."))
			return nil
		}
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" Apply %s? Your PATH is backed up first.", ui.Plural(len(chosen), "fix", "fixes")))
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
	case !o.yes:
		return withCode(ExitNeedsConfirm, "refusing to repair without confirmation: pass --yes, or use --dry-run to preview")
	default:
		for _, f := range plan.Fixes {
			if f.Selected {
				chosen = append(chosen, f)
			}
		}
	}
	if len(chosen) == 0 {
		if app.JSON {
			return app.printJSON(doc)
		}
		app.printf(" %s\n\n", ui.Muted.Render("No fix is preselected; nothing was changed. Run `"+buildinfo.Name+" repair` on a terminal to choose."))
		return nil
	}

	out := repair.Apply(ctx, repair.Env{Paths: app.pathStore(), Startup: app.startupStore(), BackupDir: backupDir(app),
		Elevated: app.Elevated, Now: time.Now()}, plan, chosen)
	recordRepair(app, out)
	doc["outcome"] = out
	if app.JSON {
		if err := app.printJSON(doc); err != nil {
			return err
		}
	} else {
		printRepairOutcome(app, out)
	}
	if !o.yes && admin > 0 && app.interactive() && !app.Elevated && app.Sandbox == "" {
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" %s need administrator rights. Review them in an elevated window?",
			ui.Plural(admin, "fix", "fixes")))
		if err == nil && ok {
			code, err := relaunchElevated([]string{"repair", "--pause"})
			reportElevated(app, code, err)
		}
	}
	if out.Failed > 0 {
		return alreadyReported(ExitError, "%s failed", ui.Plural(out.Failed, "fix", "fixes"))
	}
	return nil
}

func printFixes(app *App, plan repair.Plan) {
	width := min(ui.Width(), 110)
	groups := []struct {
		kind  repair.Kind
		title string
	}{{repair.KindPath, "Your PATH"}, {repair.KindStartup, "Startup entries"}}
	for _, g := range groups {
		first := true
		for _, f := range plan.Fixes {
			if f.Kind != g.kind {
				continue
			}
			if first {
				app.printf(" %s\n", ui.Bold.Render(g.title))
				first = false
			}
			mark := ui.Accent.Render(ui.SymItem)
			if !f.Selected {
				mark = ui.Muted.Render(ui.SymSkip)
			}
			app.printf("   %s %s\n", mark, ui.TruncateMiddle(f.Title, width-6))
			app.printf("     %s\n", ui.Muted.Render(ui.TruncateMiddle(f.Reason, width-6)))
			if f.Review != "" {
				app.printf("     %s\n", ui.Warn.Render("not preselected: ")+ui.Muted.Render(f.Review))
			}
		}
		if !first {
			app.println()
		}
	}
	for _, n := range plan.NotFixed {
		app.printf(" %s %s\n", ui.Warn.Render(ui.SymWarn), ui.Wrap(n, width-4, "   "))
	}
	if len(plan.NotFixed) > 0 {
		app.println()
	}
	if len(plan.Fixes) == 0 {
		app.printf(" %s %s\n\n", ui.OK.Render(ui.SymOK), "Nothing to repair.")
		return
	}
	app.printf(" %s\n", ui.Muted.Render(ui.SymItem+" = preselected (applied by --yes)   "+ui.SymSkip+" = review first"))
}

func printRepairOutcome(app *App, out repair.Outcome) {
	width := min(ui.Width(), 110)
	app.println()
	for _, r := range out.Results {
		mark := ui.OK.Render(ui.SymOK)
		switch r.Status {
		case repair.Skipped:
			mark = ui.Warn.Render(ui.SymWarn)
		case repair.Failed:
			mark = ui.Err.Render(ui.SymErr)
		}
		app.printf("   %s %s\n", mark, ui.TruncateMiddle(r.Fix.Title, width-6))
		if r.Reason != "" {
			app.printf("     %s\n", ui.Muted.Render(r.Status+": "+r.Reason))
		}
	}
	app.printf("\n %s %s\n", ui.Bold.Render("Repair:"), out.Summary())
	if out.Backup != "" {
		app.printf(" %s %s\n", ui.PadRight("Backup", 8), out.Backup)
		app.printf(" %s\n", ui.Muted.Render("Double-click it (or run `reg import` on it) to restore your previous PATH."))
	}
	if out.BroadcastError != "" {
		app.printf(" %s %s\n", ui.Warn.Render(ui.SymWarn), "Running programs were not notified ("+out.BroadcastError+"); sign out and in to use the new PATH everywhere.")
	} else if out.Backup != "" {
		app.printf(" %s\n", ui.Muted.Render("Programs started from now on use the new PATH; open terminals keep the old one until restarted."))
	}
	app.println()
}

func recordRepair(app *App, out repair.Outcome) {
	rec := history.Record{Time: time.Now(), Command: "repair", Sandbox: app.Sandbox != "",
		Skipped: out.Skipped, Errors: out.Failed}
	for _, r := range out.Results {
		c := history.Change{ID: r.Fix.ID, Name: r.Fix.Title, Status: "changed"}
		switch r.Fix.Kind {
		case repair.KindPath:
			c.Action, c.Detail = "removed-path-entry", "entry: "+r.Fix.Target
			if out.Backup != "" {
				c.Detail += "; backup: " + out.Backup
			}
		default:
			c.Action, c.Detail = "disabled", "startup entry "+r.Fix.Target
		}
		switch r.Status {
		case repair.Skipped:
			c.Status, c.Error = "skipped", r.Reason
		case repair.Failed:
			c.Status, c.Error = "failed", r.Reason
		}
		rec.Changes = append(rec.Changes, c)
	}
	if len(rec.Changes) > 0 {
		app.record(rec)
	}
}
