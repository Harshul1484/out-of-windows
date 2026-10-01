package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/leftovers"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type leftoversOptions struct {
	dryRun bool
	yes    bool
	pause  bool
}

func newLeftoversCmd(app *App) *cobra.Command {
	var o leftoversOptions
	cmd := &cobra.Command{
		Use:     "leftovers",
		Short:   "Find folders left behind by apps that are no longer installed",
		GroupID: "clean",
		Long: "Look for folders left behind by applications that are gone: apps uninstalled with\n" +
			buildinfo.Name + ", apps still listed but whose uninstaller and program files are missing, and\n" +
			"programs Windows remembers running whose files no longer exist.\n\n" +
			"A folder is only offered with evidence that the app owned it, never because a name\n" +
			"merely looks similar. Folders still used by installed apps, running programs, services\n" +
			"or startup entries are kept. Confirmed folders go to the Recycle Bin.",
		Example: "  " + buildinfo.Name + " leftovers --dry-run\n  " + buildinfo.Name + " leftovers --json --dry-run",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runLeftovers(cmd.Context(), app, o)
			pauseIf(app, o.pause, err)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show leftovers without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "move high-confidence leftovers without asking (required non-interactively)")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	return cmd
}

// leftoverEvidence gathers every source of "this app used to be here".
func (a *App) leftoverEvidence(inv *apps.Inventory) []leftovers.Evidence {
	var evs []leftovers.Evidence
	if recs, err := history.Load(a.Dirs.Data, 0); err == nil {
		for _, r := range recs {
			for _, id := range r.Apps {
				evs = append(evs, leftovers.Evidence{Name: id.Name, Publisher: id.Publisher,
					InstallLocation: id.InstallLocation, Exes: id.Exes, Source: leftovers.SourceHistory, When: r.Time})
			}
		}
	}
	for _, x := range inv.Apps {
		if x.IsBroken() {
			evs = append(evs, leftovers.FromApp(x, leftovers.SourceBroken))
		}
	}
	evs = append(evs, leftovers.EvidenceFromTraces(a.usageTraces(), a.Guard, apps.FileExists)...)
	return evs
}

type leftoversJSON struct {
	Schema   string               `json:"schema"`
	DryRun   bool                 `json:"dry_run"`
	Sandbox  bool                 `json:"sandbox"`
	Elevated bool                 `json:"elevated"`
	Evidence []leftovers.Evidence `json:"evidence"`
	Result   *leftovers.Result    `json:"result"`
	Recycled *leftovers.Outcome   `json:"recycled,omitempty"`
}

func runLeftovers(ctx context.Context, app *App, o leftoversOptions) error {
	if err := app.requireConfig(); err != nil {
		return err
	}
	if app.ForceDryRun {
		o.dryRun = true
	}
	inv, err := app.loadInventory(ctx, "Reading installed apps")
	if err != nil {
		return err
	}
	evs := app.leftoverEvidence(inv)
	env := app.leftoverEnv(inv, func(x apps.App) bool { return x.IsBroken() })
	spin := ui.StartSpinner(app.Err, app.tty(), func() string { return "Looking for leftovers" })
	found := leftovers.Find(ctx, env, evs)
	spin.Stop()
	if ctx.Err() != nil {
		return errCancelled
	}
	if found.Candidates == nil {
		found.Candidates = []leftovers.Candidate{}
	}
	if found.Kept == nil {
		found.Kept = []leftovers.Kept{}
	}
	if evs == nil {
		evs = []leftovers.Evidence{}
	}
	doc := leftoversJSON{Schema: "oow.leftovers/v1", DryRun: o.dryRun, Sandbox: app.Sandbox != "",
		Elevated: app.Elevated, Evidence: evs, Result: found}

	if app.JSON {
		if !o.dryRun && len(found.Candidates) > 0 {
			if !o.yes {
				return withCode(ExitNeedsConfirm, "refusing to move leftovers without confirmation: pass --yes, or use --dry-run to preview")
			}
			out, _ := reviewAndRecycle(ctx, app, env, found, true, false, false)
			doc.Recycled = out
			app.recordLeftovers(out)
		}
		return app.printJSON(doc)
	}

	app.header("Uninstalled application leftovers", o.dryRun)
	byName := map[string]leftovers.Evidence{}
	for _, e := range evs {
		if _, ok := byName[e.Name]; !ok {
			byName[e.Name] = e
		}
	}
	if o.dryRun || len(found.Candidates) == 0 {
		printLeftovers(app, found, byName)
		if o.dryRun {
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was changed."))
		}
		return nil
	}
	printLeftovers(app, found, byName)
	if !app.interactive() && !o.yes {
		return withCode(ExitNeedsConfirm, "refusing to move leftovers without confirmation: pass --yes, or use --dry-run to preview")
	}
	out, err := reviewAndRecycle(ctx, app, env, found, o.yes, false, false)
	app.recordLeftovers(out)
	return err
}

func (a *App) recordLeftovers(out *leftovers.Outcome) {
	if out == nil {
		return
	}
	rec := history.Record{Time: time.Now(), Command: "leftovers", Sandbox: a.Sandbox != "",
		Removed: len(out.Recycled), Recycled: out.Bytes, Skipped: len(out.Skipped), Errors: out.Errors}
	for _, c := range out.Recycled {
		rec.Targets = append(rec.Targets, history.TargetStat{ID: c.Path, Name: c.App, Removed: 1, Reclaimed: c.Bytes})
	}
	a.record(rec)
}
