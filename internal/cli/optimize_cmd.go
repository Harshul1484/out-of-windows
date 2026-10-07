package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/doctor"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/optimize"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// optimizer returns the real task runner, or a simulated one in sandbox mode.
func (a *App) optimizer() optimize.Runner {
	if a.Sandbox != "" {
		return sandbox.Optimizer{Root: a.Sandbox}
	}
	return optimize.System{}
}

type optimizeOptions struct {
	dryRun bool
	yes    bool
	pause  bool
	tasks  []string
}

func newOptimizeCmd(app *App) *cobra.Command {
	var o optimizeOptions
	cmd := &cobra.Command{
		Use:     "optimize",
		Short:   "Run bounded, explained maintenance tasks",
		GroupID: "system",
		Long: "Run a short list of maintenance tasks through Windows' own interfaces: flush the DNS\n" +
			"resolver cache, clear the Delivery Optimization cache (administrator), retrim SSD\n" +
			"volumes (administrator), and clean up the component store (WinSxS) with DISM when DISM's\n" +
			"analysis recommends it (administrator; never /ResetBase). Each task says what it does,\n" +
			"why, and what changes afterwards. None of them makes Windows faster on its own, and none\n" +
			"deletes your files.\n\n" +
			"The component store analysis takes a few minutes and the cleanup can take over an hour.\n" +
			"Ctrl+C stops before the next task; a DISM cleanup that has started is left to finish,\n" +
			"because stopping it midway is not safe.\n\n" +
			"The Windows Update download cache (SoftwareDistribution\\Download) is not cleaned: Windows\n" +
			"Update manages it, and Windows offers no supported way to clear it without stopping the\n" +
			"update services and deleting files.\n\n" +
			"Pending restarts, update settings and low disk space are shown for information; " + buildinfo.Name + "\n" +
			"never restarts Windows or changes update settings.",
		Example: "  " + buildinfo.Name + " optimize --dry-run\n" +
			"  " + buildinfo.Name + " optimize --task dns-flush --yes\n" +
			"  " + buildinfo.Name + " optimize --task component-store --dry-run   # DISM's analysis (administrator)\n" +
			"  " + buildinfo.Name + " optimize --dry-run --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runOptimize(cmd.Context(), app, o)
			pauseIf(app, o.pause, err)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show the plan without running anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "run the ready tasks without asking (required for non-interactive use)")
	f.StringSliceVar(&o.tasks, "task", nil, "only these tasks ("+strings.Join(optimize.ShortIDs(), ", ")+")")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	return cmd
}

// optimizeNotes are informational findings shown with the plan: things
// worth knowing that optimize deliberately does not act on.
func optimizeNotes(app *App) []doctor.Check {
	f := doctor.Collect(app.doctorProbe(), nil)
	notes := []doctor.Check{}
	for _, c := range doctor.Evaluate(f) {
		switch {
		case c.Status == doctor.OK || c.Status == doctor.Unknown:
		case c.ID == "reboot.pending", c.ID == "windows-update", strings.HasPrefix(c.ID, "disk.free"):
			if c.Details == nil {
				c.Details = []string{}
			}
			notes = append(notes, c)
		}
	}
	return notes
}

func runOptimize(ctx context.Context, app *App, o optimizeOptions) error {
	if app.ForceDryRun {
		o.dryRun = true
	}
	tasks, err := optimize.Select(o.tasks)
	if err != nil {
		return withCode(ExitUsage, "%v (tasks: %s)", err, strings.Join(optimize.ShortIDs(), ", "))
	}
	preparing := "Preparing maintenance tasks"
	if app.Elevated && hasTask(tasks, optimize.TaskComponentStore) {
		preparing += " (DISM is analyzing the component store; this can take a few minutes)"
	}
	spin := ui.StartSpinner(app.Err, app.tty(), func() string { return preparing })
	plan := optimize.Plan(ctx, app.optimizer(), app.Elevated, tasks)
	notes := optimizeNotes(app)
	spin.Stop()
	if ctx.Err() != nil {
		return errCancelled
	}
	doc := map[string]any{"schema": "oow.optimize/v1", "dry_run": o.dryRun, "sandbox": app.Sandbox != "",
		"elevated": app.Elevated, "tasks": plan, "notes": notes}

	ready := 0
	for _, it := range plan {
		if it.Selected {
			ready++
		}
	}
	if !app.JSON {
		app.header("Optimize", o.dryRun)
		printOptimizePlan(app, plan, notes)
	}
	if o.dryRun || ready == 0 {
		if app.JSON {
			return app.printJSON(doc)
		}
		switch {
		case o.dryRun:
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was run. Run `"+buildinfo.Name+" optimize` to choose and run tasks."))
		default:
			app.printf(" %s\n\n", ui.Muted.Render("No task can run now."))
			offerElevatedOptimize(ctx, app, plan)
		}
		return nil
	}

	switch {
	case app.interactive() && !o.yes:
		chosen, ok, err := chooseTasks(plan)
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was run."))
			return nil
		}
		plan = chosen
		n := 0
		for _, it := range plan {
			if it.Selected {
				n++
			}
		}
		if n == 0 {
			app.printf(" %s\n\n", ui.Muted.Render("Nothing selected. Nothing was run."))
			return nil
		}
		ok, err = confirmCtx(ctx, app, fmt.Sprintf(" Run %s?", ui.Plural(n, "task", "tasks")))
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was run."))
			return nil
		}
	case !o.yes:
		return withCode(ExitNeedsConfirm, "refusing to run maintenance tasks without confirmation: pass --yes, or use --dry-run to preview")
	}

	running, stopping := "Running maintenance tasks", "Stopping after the current task"
	dism := false
	for _, it := range plan {
		if it.Selected && it.Status == optimize.Ready && it.Task.ID == optimize.TaskComponentStore {
			dism = true
			running += " (the component store cleanup can take over an hour)"
			stopping += "; a DISM cleanup that has started is left to finish, because stopping it midway is not safe " +
				"(Ctrl+C again stops waiting; DISM keeps running)"
		}
	}
	spin = ui.StartSpinner(app.Err, app.tty(), func() string {
		if ctx.Err() != nil {
			return stopping
		}
		return running
	})
	// Without a spinner, say once why Ctrl+C does not return at once.
	finished, noted := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(noted)
		select {
		case <-ctx.Done():
			if dism && !app.tty() {
				fmt.Fprintf(app.Err, "\n %s.\n", stopping)
			}
		case <-finished:
		}
	}()
	start := time.Now()
	results := optimize.Run(ctx, app.optimizer(), plan)
	close(finished)
	<-noted
	spin.Stop()
	recordOptimize(app, results, time.Since(start))
	doc["tasks"] = plan
	doc["results"] = results
	failed := 0
	for _, r := range results {
		if r.Status == optimize.Failed || r.Status == optimize.Partial {
			failed++
		}
	}
	if app.JSON {
		if err := app.printJSON(doc); err != nil {
			return err
		}
	} else {
		printOptimizeResults(app, results)
	}
	if ctx.Err() != nil {
		return errCancelled
	}
	if !o.yes {
		offerElevatedOptimize(ctx, app, plan)
	}
	if failed > 0 {
		return alreadyReported(ExitError, "%s did not complete", ui.Plural(failed, "task", "tasks"))
	}
	return nil
}

func hasTask(tasks []*optimize.Task, id string) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

func optimizeStatusLabel(it optimize.Item) string {
	switch it.Status {
	case optimize.Ready:
		return ui.OK.Render("ready")
	case optimize.NeedsAdmin:
		return ui.Warn.Render("needs administrator")
	case optimize.NotApplicable:
		return ui.Muted.Render("nothing to do")
	}
	return ui.Muted.Render("not available")
}

func printOptimizePlan(app *App, plan []optimize.Item, notes []doctor.Check) {
	width := min(ui.Width(), 100)
	app.printf(" %s\n", ui.Bold.Render("Tasks"))
	for _, it := range plan {
		mark := ui.Accent.Render(ui.SymItem)
		if it.Status != optimize.Ready {
			mark = ui.Muted.Render(ui.SymSkip)
		}
		extra := ""
		if it.Task.ID == optimize.TaskDO && it.BytesBefore >= 0 {
			extra = "  " + ui.Muted.Render("cache "+ui.Bytes(it.BytesBefore))
		}
		if len(it.Volumes) > 0 {
			var roots []string
			for _, v := range it.Volumes {
				roots = append(roots, v.Root)
			}
			extra = "  " + ui.Muted.Render(strings.Join(roots, " "))
		}
		if cs := it.ComponentStore; cs != nil {
			extra = "  " + ui.Muted.Render(fmt.Sprintf("store %s, overhead %s, %s", ui.Bytes(cs.ActualBytes),
				ui.Bytes(cs.OverheadBytes()), ui.Plural(cs.ReclaimablePackages, "reclaimable package", "reclaimable packages")))
		}
		app.printf("   %s %s  %s%s\n", mark, ui.Bold.Render(it.Task.Name), optimizeStatusLabel(it), extra)
		if it.Reason != "" && it.Status != optimize.Ready {
			app.printf("     %s\n", ui.Muted.Render(it.Reason))
		}
		for _, line := range [][2]string{{"What", it.Task.What}, {"Why", it.Task.Why}, {"After", it.Task.Effect}} {
			app.printf("     %s %s\n", ui.Muted.Render(ui.PadRight(line[0], 6)), ui.Wrap(line[1], width-14, "            "))
		}
		app.println()
	}
	if len(notes) > 0 {
		app.printf(" %s\n", ui.Bold.Render("For your information (no action taken)"))
		for _, n := range notes {
			app.printf("   %s %s: %s\n", ui.Muted.Render(ui.SymSkip), n.Title, n.Summary)
			if n.Next != "" {
				app.printf("     %s %s\n", ui.Accent.Render("→"), ui.Wrap(n.Next, width-10, "       "))
			}
		}
		app.println()
	}
}

func chooseTasks(plan []optimize.Item) ([]optimize.Item, bool, error) {
	items := make([]ui.CheckItem, len(plan))
	for i, it := range plan {
		items[i] = ui.CheckItem{
			Label:   it.Task.Name,
			Right:   optimizeStatusLabel(it),
			Checked: it.Selected,
			Detail:  []string{"What   " + it.Task.What, "Why    " + it.Task.Why, "After  " + it.Task.Effect},
		}
		if it.Status != optimize.Ready {
			items[i].Disabled, items[i].Note = true, it.Reason
		}
	}
	r, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Select maintenance tasks", ConfirmVerb: "continue"}, items)
	if err != nil || !r.Confirmed {
		return nil, false, err
	}
	out := make([]optimize.Item, len(plan))
	copy(out, plan)
	for i := range out {
		out[i].Selected = false
	}
	for _, i := range r.Checked {
		out[i].Selected = true
	}
	return out, true, nil
}

func printOptimizeResults(app *App, results []optimize.Result) {
	app.printf(" %s\n", ui.Divider(min(ui.Width(), 100)-2))
	app.printf(" %s\n\n", ui.Title.Render("Maintenance results"))
	for _, r := range results {
		mark := ui.OK.Render(ui.SymOK)
		switch r.Status {
		case optimize.Failed:
			mark = ui.Err.Render(ui.SymErr)
		case optimize.Partial, optimize.Cancelled:
			mark = ui.Warn.Render(ui.SymWarn)
		}
		app.printf("   %s %s %s\n", mark, ui.Bold.Render(r.Name), ui.Muted.Render(r.Status))
		app.printf("     %s\n", r.Message)
	}
	var freed int64
	for _, r := range results {
		freed += r.Freed
	}
	if freed > 0 {
		app.printf("\n %s %s\n", ui.PadRight("Freed", 8), ui.Bold.Render(ui.Bytes(freed)))
	}
	app.println()
}

func recordOptimize(app *App, results []optimize.Result, d time.Duration) {
	rec := history.Record{Time: time.Now(), Command: "optimize", Sandbox: app.Sandbox != "", DurationMS: d.Milliseconds()}
	for _, r := range results {
		c := history.Change{ID: r.ID, Name: r.Name, Action: "ran", Status: "changed", Detail: r.Message}
		switch r.Status {
		case optimize.Failed:
			c.Status, c.Error = "failed", r.Message
			rec.Errors++
		case optimize.Partial:
			c.Status = "partial"
			rec.Errors++
		case optimize.Cancelled:
			c.Status = "skipped"
			rec.Skipped++
			rec.Cancelled = true
		}
		rec.Reclaimed += r.Freed
		rec.Changes = append(rec.Changes, c)
	}
	if len(rec.Changes) > 0 {
		app.record(rec)
	}
}

// offerElevatedOptimize offers to run admin-only tasks in an elevated window.
func offerElevatedOptimize(ctx context.Context, app *App, plan []optimize.Item) {
	if !app.interactive() || app.Elevated || app.Sandbox != "" {
		return
	}
	var ids, names []string
	for _, it := range plan {
		if it.Status == optimize.NeedsAdmin {
			ids = append(ids, it.Task.ID)
			names = append(names, it.Task.Name)
		}
	}
	if len(ids) == 0 {
		return
	}
	app.printf(" %s %s need administrator rights: %s\n", ui.Warn.Render(ui.SymWarn),
		ui.Plural(len(ids), "task", "tasks"), strings.Join(names, ", "))
	ok, err := confirmCtx(ctx, app, " Review and run them in an elevated window? You will see a Windows permission prompt.")
	if err != nil || !ok {
		return
	}
	code, err := relaunchElevated([]string{"optimize", "--task", strings.Join(ids, ","), "--pause"})
	reportElevated(app, code, err)
}
