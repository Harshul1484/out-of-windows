package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// startupStore returns the real startup store, or a simulated one in
// sandbox mode.
func (a *App) startupStore() startup.Store {
	if a.Sandbox != "" {
		return sandbox.Startup{Root: a.Sandbox}
	}
	return startup.System{}
}

type startupOptions struct {
	list   bool
	dryRun bool
	yes    bool
	pause  bool
}

func newStartupCmd(app *App) *cobra.Command {
	var o startupOptions
	cmd := &cobra.Command{
		Use:     "startup",
		Short:   "Review and disable programs that start when you sign in",
		GroupID: "system",
		Long: "List programs Windows starts when you sign in (Run and RunOnce registry values, the\n" +
			"Startup folders, and scheduled tasks that run at sign-in or at startup) with their\n" +
			"state, and whether the program they start still exists. On a terminal, choose which\n" +
			"ones run.\n\n" +
			"Disabling works like Task Manager: " + buildinfo.Name + " sets the entry's StartupApproved value, so\n" +
			"the entry itself is never deleted and can be enabled again. A scheduled task is\n" +
			"switched off with its own Enabled flag, like Task Scheduler's Disable; the task is\n" +
			"never edited or deleted. Windows' own tasks are not listed. Entries for all users\n" +
			"(and tasks of other accounts) need administrator rights to change.",
		Example: "  " + buildinfo.Name + " startup\n" +
			"  " + buildinfo.Name + " startup --list --json\n" +
			"  " + buildinfo.Name + " startup disable \"Contoso Agent\" --dry-run\n" +
			"  " + buildinfo.Name + " startup enable hkcu-run:ContosoAgent --yes",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runStartup(cmd.Context(), app, o)
			pauseIf(app, o.pause, err)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.list, "list", false, "only list startup entries (no interactive choice)")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	cmd.AddCommand(newStartupChangeCmd(app, false), newStartupChangeCmd(app, true))
	return cmd
}

func newStartupChangeCmd(app *App, enable bool) *cobra.Command {
	var o startupOptions
	verb, short := "disable", "Stop entries from running at sign-in (reversible)"
	if enable {
		verb, short = "enable", "Let disabled entries run at sign-in again"
	}
	cmd := &cobra.Command{
		Use:   verb + " <name|id>...",
		Short: short,
		Long: short + ". Entries are matched by ID, exact name, or part of the name\n" +
			"(see `" + buildinfo.Name + " startup --list`). Only the StartupApproved value, or a\n" +
			"scheduled task's Enabled flag, changes.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runStartupChange(cmd.Context(), app, o, enable, args)
			pauseIf(app, o.pause, err)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would change without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation (required for non-interactive use)")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	return cmd
}

func (a *App) listStartup(ctx context.Context) ([]startup.Entry, []string, error) {
	spin := ui.StartSpinner(a.Err, a.tty(), func() string { return "Reading startup entries" })
	entries, warnings, err := a.startupStore().List(ctx)
	spin.Stop()
	if ctx.Err() != nil {
		return nil, nil, errCancelled
	}
	if err != nil {
		return nil, nil, withCode(ExitError, "could not read startup entries: %v", err)
	}
	if entries == nil {
		entries = []startup.Entry{}
	}
	return entries, warnings, nil
}

type startupSummaryJSON struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
	RunsOnce int `json:"runs_once"`
	Broken   int `json:"broken"`
}

func summarizeStartup(entries []startup.Entry) startupSummaryJSON {
	s := startupSummaryJSON{Total: len(entries)}
	for _, e := range entries {
		switch e.State {
		case startup.Enabled:
			s.Enabled++
			if e.Broken() {
				s.Broken++
			}
		case startup.Disabled:
			s.Disabled++
		default:
			s.RunsOnce++
		}
	}
	return s
}

func runStartup(ctx context.Context, app *App, o startupOptions) error {
	entries, warnings, err := app.listStartup(ctx)
	if err != nil {
		return err
	}
	if app.JSON {
		return app.printJSON(map[string]any{"schema": "oow.startup/v1", "sandbox": app.Sandbox != "",
			"elevated": app.Elevated, "entries": entries, "summary": summarizeStartup(entries), "warnings": nonNil(warnings)})
	}
	if o.list || !app.interactive() {
		app.header("Startup programs", false)
		printStartup(app, entries, warnings)
		return nil
	}
	return chooseStartup(ctx, app, entries)
}

func startupStateLabel(e startup.Entry) string {
	switch {
	case e.State == startup.Disabled:
		return ui.Muted.Render(ui.SymSkip + " disabled")
	case e.State == startup.RunsOnce:
		return ui.Muted.Render(ui.SymItem + " once")
	case e.Broken():
		return ui.Warn.Render(ui.SymWarn + " broken")
	}
	return ui.OK.Render(ui.SymOK + " enabled")
}

func startupDetail(e startup.Entry) string {
	switch e.TargetState {
	case system.Absent:
		return "program missing: " + e.Target
	}
	return e.Command
}

func printStartup(app *App, entries []startup.Entry, warnings []string) {
	width := min(ui.Width(), 120)
	nameW := 20
	for _, e := range entries {
		nameW = max(nameW, len([]rune(e.Name)))
	}
	nameW = min(nameW, 32)
	for _, group := range []struct {
		scope, title string
	}{{"user", "This user"}, {"machine", "All users"}} {
		var list []startup.Entry
		for _, e := range entries {
			if e.Scope == group.scope {
				list = append(list, e)
			}
		}
		if len(list) == 0 {
			continue
		}
		title := ui.Bold.Render(group.title)
		if group.scope == "machine" && !app.Elevated {
			title += "  " + ui.Muted.Render("(changing these needs administrator rights)")
		}
		app.printf(" %s\n", title)
		for _, e := range list {
			app.printf("   %s %s %s\n", ui.PadRight(startupStateLabel(e), 10), ui.PadRight(ui.TruncateMiddle(e.Name, nameW), nameW),
				ui.Muted.Render(ui.TruncateMiddle(startupDetail(e), max(16, width-nameW-17))))
		}
		app.println()
	}
	if len(entries) == 0 {
		app.printf(" %s\n\n", ui.Muted.Render("No startup entries found."))
	}
	for _, w := range warnings {
		app.printf(" %s %s\n", ui.Warn.Render(ui.SymWarn), w)
	}
	s := summarizeStartup(entries)
	app.printf(" %s\n", ui.Divider(width-2))
	line := fmt.Sprintf("%s %s %d enabled %s %d disabled", ui.Plural(s.Total, "entry", "entries"), ui.SymDot, s.Enabled, ui.SymDot, s.Disabled)
	if s.RunsOnce > 0 {
		line += fmt.Sprintf(" %s %d run once", ui.SymDot, s.RunsOnce)
	}
	if s.Broken > 0 {
		line += " " + ui.SymDot + " " + ui.Warn.Render(fmt.Sprintf("%d broken (program missing)", s.Broken))
	}
	app.printf(" %s\n", line)
	app.printf(" %s\n\n", ui.Muted.Render("Disable one with `"+buildinfo.Name+" startup disable <name>`; "+
		"`"+buildinfo.Name+" startup enable <name>` undoes it."))
}

// chooseStartup shows a checklist (checked = runs at sign-in), confirms the
// difference and applies it.
func chooseStartup(ctx context.Context, app *App, entries []startup.Entry) error {
	var items []ui.CheckItem
	var idx []int
	lastScope := ""
	adminOnly := 0
	for i, e := range entries {
		if e.Scope != lastScope {
			title := "This user"
			if e.Scope == "machine" {
				title = "All users"
			}
			items = append(items, ui.CheckItem{Header: title})
			idx = append(idx, -1)
			lastScope = e.Scope
		}
		it := ui.CheckItem{
			Label:   e.Name,
			Right:   startupStateLabel(e),
			Checked: e.State != startup.Disabled,
			Detail: []string{
				"Runs   " + e.Command,
				"From   " + e.SourceLabel + " " + ui.SymDot + " " + e.Location,
				"Target " + targetLine(e),
			},
		}
		switch {
		case !e.Toggleable:
			it.Disabled, it.Note = true, "runs once; cannot be disabled"
		case e.NeedsAdmin && !app.Elevated:
			it.Disabled, it.Note = true, "needs administrator rights"
			adminOnly++
		}
		items = append(items, it)
		idx = append(idx, i)
	}
	if len(entries) == 0 {
		app.header("Startup programs", false)
		app.printf(" %s\n\n", ui.Muted.Render("No startup entries found."))
		return nil
	}
	res, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Startup programs (checked = runs when you sign in)", ConfirmVerb: "apply"}, items)
	if err != nil {
		return err
	}
	if !res.Confirmed {
		app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
		return nil
	}
	checked := map[int]bool{}
	for _, i := range res.Checked {
		checked[idx[i]] = true
	}
	var disable, enable []startup.Entry
	for i, it := range items {
		if idx[i] < 0 || it.Disabled {
			continue
		}
		e := entries[idx[i]]
		switch {
		case e.State == startup.Enabled && !checked[idx[i]]:
			disable = append(disable, e)
		case e.State == startup.Disabled && checked[idx[i]]:
			enable = append(enable, e)
		}
	}
	if len(disable)+len(enable) == 0 {
		app.printf(" %s\n\n", ui.Muted.Render("No changes."))
	} else {
		app.println()
		for _, e := range disable {
			app.printf("   %s disable %s\n", ui.Accent.Render(ui.SymItem), e.Name)
		}
		for _, e := range enable {
			app.printf("   %s enable  %s\n", ui.Accent.Render(ui.SymItem), e.Name)
		}
		ok, err := confirmCtx(ctx, app, " Apply? Entries stay registered and can be switched back at any time.")
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
		now := time.Now()
		var results []startup.Result
		results = append(results, startup.SetEnabled(ctx, app.startupStore(), disable, false, app.Elevated, now)...)
		results = append(results, startup.SetEnabled(ctx, app.startupStore(), enable, true, app.Elevated, now)...)
		app.recordStartup(results, enable)
		printStartupResults(app, results)
	}
	if adminOnly > 0 && !app.Elevated && app.Sandbox == "" {
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" %s for all users need administrator rights. Review them in an elevated window?",
			ui.Plural(adminOnly, "entry", "entries")))
		if err == nil && ok {
			code, err := relaunchElevated([]string{"startup", "--pause"})
			reportElevated(app, code, err)
		}
	}
	return nil
}

func targetLine(e startup.Entry) string {
	s := string(e.TargetState)
	if e.Target != "" {
		s += ": " + e.Target
	}
	if e.TargetNote != "" {
		s += " (" + e.TargetNote + ")"
	}
	return s
}

func runStartupChange(ctx context.Context, app *App, o startupOptions, enable bool, queries []string) error {
	if app.ForceDryRun {
		o.dryRun = true
	}
	verb, past := "disable", "disabled"
	if enable {
		verb, past = "enable", "enabled"
	}
	entries, _, err := app.listStartup(ctx)
	if err != nil {
		return err
	}
	var targets []startup.Entry
	seen := map[string]bool{}
	for _, q := range queries {
		m := startup.Find(entries, q)
		switch len(m) {
		case 0:
			return withCode(ExitUsage, "no startup entry matches %q (see `%s startup --list`)", q, buildinfo.Name)
		case 1:
		default:
			var names []string
			for _, e := range m {
				names = append(names, fmt.Sprintf("%s (%s)", e.Name, e.ID))
			}
			return withCode(ExitUsage, "%q matches %d startup entries; use the ID:\n  %s", q, len(m), strings.Join(names, "\n  "))
		}
		if !seen[m[0].ID] {
			seen[m[0].ID] = true
			targets = append(targets, m[0])
		}
	}

	plan := startup.Plan(targets, enable, app.Elevated)
	doc := map[string]any{"schema": "oow.startup-change/v1", "action": verb, "dry_run": o.dryRun,
		"sandbox": app.Sandbox != "", "elevated": app.Elevated}
	actionable, adminSkipped, refused := 0, 0, 0
	adminReason := ""
	for _, r := range plan {
		switch {
		case r.Status == startup.StatusPlanned:
			actionable++
		case startup.IsAdminReason(r.Reason):
			adminSkipped++
			adminReason = r.Reason
		case r.Status == startup.StatusSkipped:
			refused++
		}
	}
	// refusal reports entries that were asked for but cannot be changed.
	refusal := func() error {
		if adminSkipped > 0 {
			return startupAdminRefusal(ctx, app, o, verb, adminSkipped, adminReason, targets)
		}
		if refused > 0 {
			return alreadyReported(ExitError, "%s cannot be %s", ui.Plural(refused, "entry", "entries"), past)
		}
		return nil
	}
	if !app.JSON {
		app.header(strings.ToUpper(verb[:1])+verb[1:]+" startup entries", o.dryRun)
		printStartupResults(app, plan)
	}
	if o.dryRun || actionable == 0 {
		if app.JSON {
			doc["results"] = plan
			if err := app.printJSON(doc); err != nil {
				return err
			}
		} else if o.dryRun {
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was changed."))
		}
		if o.dryRun {
			return nil
		}
		return refusal()
	}

	switch {
	case app.interactive() && !o.yes:
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" %s %s? %s", strings.ToUpper(verb[:1])+verb[1:],
			ui.Plural(actionable, "entry", "entries"), "Only the StartupApproved value or a task's Enabled flag changes; this can be undone."))
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
	case !o.yes:
		return withCode(ExitNeedsConfirm, "refusing to %s startup entries without confirmation: pass --yes, or use --dry-run to preview", verb)
	}

	results := startup.SetEnabled(ctx, app.startupStore(), targets, enable, app.Elevated, time.Now())
	var enabledTargets []startup.Entry
	if enable {
		enabledTargets = targets
	}
	app.recordStartup(results, enabledTargets)
	failed := 0
	for _, r := range results {
		if r.Status == startup.StatusFailed {
			failed++
		}
	}
	if app.JSON {
		doc["results"] = results
		if err := app.printJSON(doc); err != nil {
			return err
		}
	} else {
		app.printf(" %s\n", ui.Bold.Render("Result"))
		printStartupResults(app, results)
	}
	if failed > 0 {
		return alreadyReported(ExitError, "%s could not be %s", ui.Plural(failed, "entry", "entries"), past)
	}
	return refusal()
}

// startupAdminRefusal reports entries skipped for lack of administrator
// rights and, interactively, offers an elevated window for them.
func startupAdminRefusal(ctx context.Context, app *App, o startupOptions, verb string, n int, reason string, targets []startup.Entry) error {
	if app.interactive() && !o.yes && app.Sandbox == "" {
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" %s %s in an elevated window? You will see a Windows permission prompt.",
			strings.ToUpper(verb[:1])+verb[1:], ui.Plural(n, "entry", "entries")))
		if err == nil && ok {
			args := []string{"startup", verb, "--pause"}
			for _, e := range targets {
				if e.NeedsAdmin {
					args = append(args, e.ID)
				}
			}
			code, err := relaunchElevated(args)
			reportElevated(app, code, err)
			return nil
		}
	}
	return alreadyReported(ExitError, "%s %s: %s", ui.Plural(n, "entry", "entries"), "not changed", reason)
}

func printStartupResults(app *App, results []startup.Result) {
	for _, r := range results {
		var mark, what string
		switch r.Status {
		case startup.StatusPlanned:
			mark, what = ui.Accent.Render(ui.SymItem), "will change"
		case startup.StatusChanged:
			mark, what = ui.OK.Render(ui.SymOK), string(r.Entry.State)
		case startup.StatusUnchanged:
			mark, what = ui.Muted.Render(ui.SymSkip), r.Reason
		case startup.StatusSkipped:
			mark, what = ui.Warn.Render(ui.SymWarn), "skipped: "+r.Reason
		default:
			mark, what = ui.Err.Render(ui.SymErr), "failed: "+r.Reason
		}
		app.printf("   %s %s %s\n", mark, ui.Bold.Render(r.Entry.Name), ui.Muted.Render(ui.SymDot+" "+r.Entry.SourceLabel))
		app.printf("     %s\n", what)
	}
	app.println()
}

// recordStartup appends the changes to history, with the previous approval
// value so a change can be undone by hand.
func (a *App) recordStartup(results []startup.Result, enabled []startup.Entry) {
	isEnable := map[string]bool{}
	for _, e := range enabled {
		isEnable[e.ID] = true
	}
	rec := history.Record{Time: time.Now(), Command: "startup", Sandbox: a.Sandbox != ""}
	for _, r := range results {
		action := "disabled"
		if isEnable[r.Entry.ID] {
			action = "enabled"
		}
		before := r.Before
		if before == "" {
			before = "absent (enabled)"
		}
		detail := "StartupApproved before: " + before
		if r.TaskEnabledBefore != nil {
			// A task has no approval value: record its own flag instead.
			detail = fmt.Sprintf("scheduled task %s, Enabled before: %v", r.Entry.Location, *r.TaskEnabledBefore)
		}
		c := history.Change{ID: r.Entry.ID, Name: r.Entry.Name, Action: action, Status: r.Status, Detail: detail}
		switch r.Status {
		case startup.StatusFailed:
			c.Error = r.Reason
			rec.Errors++
		case startup.StatusChanged:
		default:
			c.Status, c.Error = startup.StatusSkipped, r.Reason
			rec.Skipped++
		}
		rec.Changes = append(rec.Changes, c)
	}
	if len(rec.Changes) > 0 {
		a.record(rec)
	}
}
