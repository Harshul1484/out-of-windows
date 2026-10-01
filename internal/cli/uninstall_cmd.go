package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/leftovers"
	"github.com/Harshul1484/out-of-windows/internal/ui"
	"github.com/Harshul1484/out-of-windows/internal/uninstall"
)

type uninstallOptions struct {
	dryRun        bool
	yes           bool
	quiet         bool
	keepLeftovers bool
	list          bool
	id            string
	wait          time.Duration
	pause         bool
}

func newUninstallCmd(app *App) *cobra.Command {
	var o uninstallOptions
	cmd := &cobra.Command{
		Use:     "uninstall [app name]",
		Short:   "Uninstall apps with their own uninstaller, then remove what they leave behind",
		GroupID: "clean",
		Long: "Uninstall applications installed with Windows Installer, regular installers, the Microsoft\n" +
			"Store, Scoop or Chocolatey. oow runs the app's own uninstaller, waits for it, verifies the\n" +
			"app is gone, then shows folders it left behind with the evidence for each. Leftovers you\n" +
			"confirm are moved to the Recycle Bin, so they can be restored.",
		Example: "  " + buildinfo.Name + " uninstall\n" +
			"  " + buildinfo.Name + " uninstall --list\n" +
			"  " + buildinfo.Name + " uninstall \"Contoso Studio\" --dry-run\n" +
			"  " + buildinfo.Name + " uninstall --id reg:hklm64:ContosoStudio --yes --quiet",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runUninstall(cmd.Context(), app, o, strings.TrimSpace(strings.Join(args, " ")))
			pauseIf(app, o.pause, err)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would run and what may be left behind, without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation (required for non-interactive use)")
	f.BoolVar(&o.quiet, "quiet", false, "use the app's silent uninstall mode when it has one")
	f.BoolVar(&o.keepLeftovers, "keep-leftovers", false, "do not look for or remove leftover folders")
	f.BoolVar(&o.list, "list", false, "list installed apps and exit")
	f.StringVar(&o.id, "id", "", "uninstall the app with this exact ID (see --list --json)")
	f.DurationVar(&o.wait, "wait", 3*time.Minute, "how long to wait for an uninstaller to finish")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	return cmd
}

func pauseIf(app *App, pause bool, err error) {
	if !pause {
		return
	}
	if err != nil {
		fmt.Fprintf(app.Err, "%s %v\n", ui.Err.Render("error:"), err)
	}
	fmt.Fprint(app.Out, "\n Press Enter to close this window.")
	_, _ = bufio.NewReader(app.In).ReadString('\n')
}

func (a *App) loadInventory(ctx context.Context, what string) (*apps.Inventory, error) {
	spin := ui.StartSpinner(a.Err, a.tty(), func() string { return what })
	inv, err := a.appProvider().List(ctx)
	spin.Stop()
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, errCancelled
	}
	return inv, nil
}

func runUninstall(ctx context.Context, app *App, o uninstallOptions, query string) error {
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
	if o.list {
		return printAppList(app, inv)
	}

	targets, err := chooseApps(app, inv, o, query)
	if err != nil || len(targets) == 0 {
		return err
	}
	if !app.JSON {
		app.header("Uninstall", o.dryRun)
	}
	report := uninstallJSON{Schema: "oow.uninstall/v1", DryRun: o.dryRun, Sandbox: app.Sandbox != ""}
	var firstErr error
	for _, a := range targets {
		r, err := uninstallOne(ctx, app, inv, a, o)
		if err != nil && r.Error == "" {
			r.Error = err.Error()
		}
		report.Results = append(report.Results, r)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if errors.Is(err, errCancelled) {
			break
		}
	}
	if app.JSON {
		if err := app.printJSON(report); err != nil {
			return err
		}
		// The document already carries per-app errors; keep stdout to one
		// JSON document while preserving the exit code.
		var ee *exitError
		if errors.As(firstErr, &ee) {
			return &exitError{code: ee.code, err: ee.err, reported: true}
		}
	}
	return firstErr
}

func chooseApps(app *App, inv *apps.Inventory, o uninstallOptions, query string) ([]apps.App, error) {
	if o.id != "" {
		a, ok := inv.Find(o.id)
		if !ok {
			return nil, withCode(ExitUsage, "no installed app has ID %q (see `%s uninstall --list --json`)", o.id, buildinfo.Name)
		}
		return []apps.App{a}, nil
	}
	var matches []apps.App
	if query != "" {
		q := strings.ToLower(query)
		for _, a := range inv.Apps {
			if strings.EqualFold(a.Name, query) {
				matches = []apps.App{a}
				break
			}
			if strings.Contains(strings.ToLower(a.Name), q) {
				matches = append(matches, a)
			}
		}
		if len(matches) == 0 {
			return nil, withCode(ExitUsage, "no installed app matches %q (see `%s uninstall --list`)", query, buildinfo.Name)
		}
		if len(matches) == 1 {
			return matches, nil
		}
	}
	if !app.interactive() {
		if query == "" {
			return nil, withCode(ExitUsage, "name the app to uninstall, or use --list to see installed apps")
		}
		var names []string
		for _, m := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", m.Name, m.ID))
		}
		return nil, withCode(ExitUsage, "%q matches %d apps; be more specific or use --id:\n  %s",
			query, len(matches), strings.Join(names, "\n  "))
	}
	list := inv.Apps
	if len(matches) > 0 {
		list = matches
	}
	items := make([]ui.PickItem, len(list))
	for i, a := range list {
		items[i] = appPickItem(a)
	}
	res, err := ui.RunPicker(ui.PickerOptions{Title: "Uninstall apps", Multi: true, ConfirmVerb: "uninstall", Noun: "app"}, items)
	if err != nil || !res.Confirmed {
		return nil, err
	}
	var out []apps.App
	for _, i := range res.Selected {
		out = append(out, list[i])
	}
	return out, nil
}

func appPickItem(a apps.App) ui.PickItem {
	sub := strings.Join(nonEmptyStrings(a.Version, a.Publisher, sourceLabel(a)), " · ")
	size := ui.Muted.Render("—")
	if a.SizeBytes > 0 {
		size = ui.Bytes(a.SizeBytes)
	}
	it := ui.PickItem{Title: a.Name, Subtitle: sub, Right: size, Size: a.SizeBytes}
	if ok, why := a.Removable(); !ok {
		it.Disabled, it.Note = true, why
	}
	return it
}

func sourceLabel(a apps.App) string {
	switch a.Source {
	case apps.SourceMSI:
		return "Windows Installer"
	case apps.SourceAppX:
		return "Microsoft Store"
	case apps.SourceScoop:
		return "Scoop"
	case apps.SourceChoco:
		return "Chocolatey"
	}
	if a.Scope == apps.ScopeUser {
		return "this user"
	}
	return ""
}

func nonEmptyStrings(v ...string) []string {
	var out []string
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func printAppList(app *App, inv *apps.Inventory) error {
	if app.JSON {
		if inv.Apps == nil {
			inv.Apps = []apps.App{}
		}
		return app.printJSON(map[string]any{"schema": "oow.apps/v1", "apps": inv.Apps,
			"package_managers": inv.PackageManagers, "warnings": nonNil(inv.Warnings)})
	}
	app.header("Installed apps", false)
	width := min(ui.Width(), 110)
	nameW := 24
	for _, a := range inv.Apps {
		nameW = max(nameW, len([]rune(a.Name)))
	}
	nameW = min(nameW, 40)
	for _, a := range inv.Apps {
		size := ui.Muted.Render("—")
		if a.SizeBytes > 0 {
			size = ui.Bytes(a.SizeBytes)
		}
		mark := ui.Accent.Render(ui.SymItem)
		detail := strings.Join(nonEmptyStrings(a.Version, a.Publisher, sourceLabel(a)), " · ")
		if ok, why := a.Removable(); !ok {
			mark, detail = ui.Muted.Render(ui.SymSkip), why
		}
		app.printf("  %s %s %s  %s\n", mark, ui.PadRight(ui.TruncateMiddle(a.Name, nameW), nameW), ui.PadLeft(size, 9),
			ui.Muted.Render(ui.Truncate(detail, max(10, width-nameW-18))))
	}
	app.printf("\n %s\n", ui.Muted.Render(ui.Plural(len(inv.Apps), "app", "apps")))
	for _, w := range inv.Warnings {
		app.printf(" %s %s\n", ui.Warn.Render(ui.SymWarn), w)
	}
	app.println()
	return nil
}

// uninstallJSON is the oow.uninstall/v1 document.
type uninstallJSON struct {
	Schema  string                `json:"schema"`
	DryRun  bool                  `json:"dry_run"`
	Sandbox bool                  `json:"sandbox"`
	Results []uninstallResultJSON `json:"results"`
}

type uninstallResultJSON struct {
	App       apps.App           `json:"app"`
	Plan      *uninstall.Plan    `json:"plan,omitempty"`
	Outcome   *uninstall.Outcome `json:"outcome,omitempty"`
	Error     string             `json:"error,omitempty"`
	Leftovers *leftovers.Result  `json:"leftovers,omitempty"`
	Recycled  *leftovers.Outcome `json:"recycled,omitempty"`
}

// uninstallOne plans, confirms, runs and verifies one uninstall, then
// handles its leftovers.
func uninstallOne(ctx context.Context, app *App, inv *apps.Inventory, a apps.App, o uninstallOptions) (uninstallResultJSON, error) {
	r := uninstallResultJSON{App: a}
	plan, err := uninstall.NewPlan(a, o.quiet)
	if err != nil {
		r.Error = err.Error()
		if !app.JSON {
			app.printf(" %s %s\n\n", ui.Err.Render(ui.SymErr), err)
		}
		return r, withCode(ExitError, "%v", err)
	}
	r.Plan = &plan
	ev := leftovers.FromApp(a, leftovers.SourceUninstalled)

	// What may remain: matching folders now, judged as if the app were gone.
	preview := leftovers.Find(ctx, app.leftoverEnv(inv, func(x apps.App) bool { return x.ID == a.ID || x.IsBroken() }),
		[]leftovers.Evidence{ev})
	if !app.JSON {
		printPlan(app, plan, preview)
	}
	if o.dryRun {
		r.Leftovers = preview
		if !app.JSON {
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was changed. Leftovers are only reviewed after the uninstaller has run."))
		}
		return r, nil
	}

	switch {
	case app.interactive() && !o.yes:
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" Uninstall %s?", a.Name))
		if err != nil {
			return r, err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Skipped. Nothing was changed."))
			return r, nil
		}
	case !o.yes:
		return r, withCode(ExitNeedsConfirm, "refusing to uninstall without confirmation: pass --yes, or use --dry-run to preview")
	}

	// Run and verify. Stdin is deliberately not read while waiting: a stray
	// reader would steal the answers to the prompts that follow.
	hint := fmt.Sprintf("waiting up to %s for the uninstaller to finish; follow its window if one opens (Ctrl+C stops waiting)",
		o.wait.Round(time.Second))
	spin := ui.StartSpinner(app.Err, app.tty(), func() string { return a.Name + ": " + hint })
	outcome, runErr := uninstall.Execute(ctx, plan, app.uninstallRunner(), app.uninstallChecker(),
		uninstall.Options{WaitTimeout: o.wait})
	spin.Stop()
	r.Outcome = &outcome
	if ctx.Err() != nil && !outcome.Removed {
		return r, errCancelled
	}

	rec := history.Record{Time: time.Now(), Command: "uninstall", Sandbox: app.Sandbox != "",
		DurationMS: outcome.Waited.Milliseconds()}
	target := history.TargetStat{ID: a.ID, Name: a.Name}
	if !outcome.Removed {
		rec.Errors, target.Errors = 1, 1
		rec.Targets = []history.TargetStat{target}
		app.record(rec)
		if !app.JSON {
			app.printf(" %s %s: %s\n\n", ui.Err.Render(ui.SymErr), a.Name, outcome.Message)
		}
		if runErr != nil && !errors.Is(runErr, uninstall.ErrNotRemoved) {
			return r, withCode(ExitError, "%s: %v", a.Name, runErr)
		}
		return r, alreadyReported(ExitError, "%s: %s", a.Name, outcome.Message)
	}
	if !app.JSON {
		app.printf(" %s %s %s\n", ui.OK.Render(ui.SymOK), ui.Bold.Render(a.Name), outcome.Message)
	}
	rec.Apps = []history.AppIdentity{{Name: a.Name, Version: a.Version, Publisher: a.Publisher,
		InstallLocation: a.InstallLocation, Exes: ev.Exes}}

	if o.keepLeftovers {
		target.Removed = 1
		rec.Removed, rec.Targets = 1, []history.TargetStat{target}
		app.record(rec)
		app.println()
		return r, nil
	}

	// Leftovers, judged against what is installed now.
	inv2, err := app.appProvider().List(ctx)
	if err != nil {
		inv2 = inv
	}
	env := app.leftoverEnv(inv2, func(x apps.App) bool { return x.IsBroken() })
	found := leftovers.Find(ctx, env, []leftovers.Evidence{ev})
	r.Leftovers = found
	recycled, err := reviewAndRecycle(ctx, app, env, found, o.yes, o.dryRun, true)
	r.Recycled = recycled
	target.Removed = 1
	if recycled != nil {
		rec.Removed = 1 + len(recycled.Recycled)
		rec.Recycled = recycled.Bytes
		rec.Skipped, rec.Errors = len(recycled.Skipped), recycled.Errors
		target.Removed += len(recycled.Recycled)
		target.Skipped, target.Errors = len(recycled.Skipped), recycled.Errors
	} else {
		rec.Removed = 1
	}
	rec.Targets = []history.TargetStat{target}
	app.record(rec)
	return r, err
}

func printPlan(app *App, p uninstall.Plan, preview *leftovers.Result) {
	a := p.App
	size := ""
	if a.SizeBytes > 0 {
		size = " · " + ui.Bytes(a.SizeBytes)
	}
	app.printf(" %s %s\n", ui.Bold.Render(a.Name), ui.Muted.Render(strings.Join(nonEmptyStrings(a.Version, a.Publisher), " · ")+size))
	app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("Uninstaller", 13)), p.Command)
	if p.Elevate {
		app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("", 13)), ui.Warn.Render("runs with administrator rights (Windows will ask)"))
	}
	for _, n := range p.Notes {
		app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("", 13)), ui.Muted.Render(n))
	}
	if a.InstallLocation != "" {
		app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("Installed in", 13)), a.InstallLocation)
	}
	if n := len(preview.Candidates); n > 0 {
		app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("Related", 13)),
			fmt.Sprintf("%s (%s now); whatever is left is reviewed after uninstalling",
				ui.Plural(n, "folder", "folders"), ui.Bytes(preview.Bytes())))
	}
	app.println()
}

// leftoverEnv builds the leftover environment with claims from inv (minus
// skipped apps) and from running programs, services and startup entries.
func (a *App) leftoverEnv(inv *apps.Inventory, skip func(apps.App) bool) *leftovers.Env {
	return &leftovers.Env{
		Guard:          a.Guard,
		Claims:         leftovers.NewClaims(inv, skip, a.systemClaims(), a.Guard),
		Elevated:       a.Elevated,
		RecentActivity: 7 * 24 * time.Hour,
	}
}

func (a *App) record(rec history.Record) {
	if err := history.Append(a.Dirs.Data, rec); err != nil {
		fmt.Fprintf(a.Err, "%s could not record history: %v\n", ui.Warn.Render("warning:"), err)
	}
}

// reviewAndRecycle shows leftover candidates, lets the user choose (high
// confidence preselected; --yes takes exactly those), confirms, and moves
// them to the Recycle Bin.
func reviewAndRecycle(ctx context.Context, app *App, env *leftovers.Env, found *leftovers.Result, yes, dryRun, showList bool) (*leftovers.Outcome, error) {
	if showList && !app.JSON {
		printLeftovers(app, found, nil)
	}
	if len(found.Candidates) == 0 || dryRun {
		return nil, nil
	}
	var chosen []leftovers.Candidate
	var admin []leftovers.Candidate
	switch {
	case app.interactive() && !yes:
		var items []ui.CheckItem
		var idx []int
		lastApp := ""
		for i, c := range found.Candidates {
			if c.App != lastApp {
				items = append(items, ui.CheckItem{Header: c.App})
				idx = append(idx, -1)
				lastApp = c.App
			}
			it := ui.CheckItem{
				Label:   c.Path,
				Right:   ui.PadLeft(ui.Bytes(c.Bytes), 9) + "  " + confidenceLabel(c.Confidence),
				Checked: c.Confidence == leftovers.High,
				Weight:  c.Bytes,
				Detail:  append([]string{"Why    " + strings.Join(c.Reasons, "; ")}, adminNote(c)...),
			}
			if c.NeedsAdmin && !env.Elevated {
				it.Disabled, it.Note, it.Checked = true, "needs administrator rights", false
			}
			items = append(items, it)
			idx = append(idx, i)
		}
		res, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Choose leftovers to move to the Recycle Bin", ConfirmVerb: "continue", ShowWeight: true}, items)
		if err != nil {
			return nil, err
		}
		if !res.Confirmed {
			app.printf(" %s\n\n", ui.Muted.Render("Leftovers kept. Nothing else was changed."))
			return nil, nil
		}
		for _, i := range res.Checked {
			chosen = append(chosen, found.Candidates[idx[i]])
		}
	default:
		for _, c := range found.Candidates {
			if c.Confidence == leftovers.High {
				chosen = append(chosen, c)
			}
		}
	}
	for _, c := range found.Candidates {
		if c.NeedsAdmin && !env.Elevated {
			admin = append(admin, c)
		}
	}
	var runnable []leftovers.Candidate
	var bytes int64
	for _, c := range chosen {
		if !c.NeedsAdmin || env.Elevated {
			runnable = append(runnable, c)
			bytes += c.Bytes
		}
	}
	var out *leftovers.Outcome
	if len(runnable) > 0 {
		if app.interactive() && !yes {
			ok, err := confirmCtx(ctx, app, fmt.Sprintf(" Move %s (%s) to the Recycle Bin? You can restore them from there.",
				ui.Plural(len(runnable), "folder", "folders"), ui.Bytes(bytes)))
			if err != nil {
				return nil, err
			}
			if !ok {
				app.printf(" %s\n\n", ui.Muted.Render("Leftovers kept."))
				return nil, nil
			}
		}
		out = leftovers.Recycle(ctx, env, runnable, app.recycler())
		if !app.JSON {
			printRecycled(app, out)
		}
	}
	if len(admin) > 0 && !app.JSON && !yes {
		offerElevatedLeftovers(ctx, app, admin)
	}
	return out, nil
}

func adminNote(c leftovers.Candidate) []string {
	if c.NeedsAdmin {
		return []string{"Note   in a protected location: removing it needs administrator rights"}
	}
	return nil
}

func confidenceLabel(c leftovers.Confidence) string {
	if c == leftovers.High {
		return ui.OK.Render("high")
	}
	return ui.Warn.Render("medium")
}

func printLeftovers(app *App, found *leftovers.Result, evidence map[string]leftovers.Evidence) {
	if len(found.Candidates) == 0 {
		app.printf(" %s %s\n", ui.OK.Render(ui.SymOK), "No leftovers found.")
	} else {
		width := min(ui.Width(), 110)
		app.printf("\n %s\n", ui.Bold.Render("Leftovers"))
		lastApp := ""
		for _, c := range found.Candidates {
			if c.App != lastApp {
				line := "   " + ui.Accent.Render(ui.SymItem) + " " + ui.Bold.Render(c.App)
				if ev, ok := evidence[c.App]; ok {
					line += "  " + ui.Muted.Render(ev.Describe())
				}
				app.println(line)
				lastApp = c.App
			}
			admin := ""
			if c.NeedsAdmin {
				admin = ui.Warn.Render(" admin")
			}
			app.printf("     %s %s %s%s\n", ui.PadRight(confidenceLabel(c.Confidence), 7), ui.PadLeft(ui.Bytes(c.Bytes), 9),
				ui.TruncateMiddle(c.Path, width-30), admin)
			app.printf("       %s\n", ui.Muted.Render(ui.Wrap(strings.Join(c.Reasons, "; "), width-10, "       ")))
		}
		app.printf("\n %s %s %s\n", ui.PadRight("Total", 12), ui.Title.Render(ui.Bytes(found.Bytes())),
			ui.Muted.Render("in "+ui.Plural(len(found.Candidates), "folder", "folders")))
	}
	if n := len(found.Kept); n > 0 {
		app.printf(" %s\n", ui.Muted.Render(fmt.Sprintf("%s kept (still in use or protected); --json lists them",
			ui.Plural(n, "matching folder", "matching folders"))))
	}
	app.println()
}

func printRecycled(app *App, out *leftovers.Outcome) {
	for _, c := range out.Recycled {
		app.printf("   %s %s %s\n", ui.OK.Render(ui.SymOK), ui.PadLeft(ui.Bytes(c.Bytes), 9), filepath.Clean(c.Path))
	}
	for _, s := range out.Skipped {
		app.printf("   %s %s %s\n", ui.Muted.Render(ui.SymSkip), s.Path, ui.Muted.Render(s.Reason))
	}
	app.printf("\n %s %s moved to the Recycle Bin %s\n\n", ui.OK.Render(ui.SymOK), ui.Bytes(out.Bytes),
		ui.Muted.Render("(restore from the Recycle Bin if needed)"))
}

func offerElevatedLeftovers(ctx context.Context, app *App, admin []leftovers.Candidate) {
	if !app.interactive() || app.Elevated || app.Sandbox != "" {
		return
	}
	app.printf(" %s %s in protected locations need administrator rights.\n", ui.Warn.Render(ui.SymWarn),
		ui.Plural(len(admin), "leftover folder", "leftover folders"))
	ok, err := confirmCtx(ctx, app, " Review them in an elevated window? You will see a Windows permission prompt.")
	if err != nil || !ok {
		return
	}
	code, err := relaunchElevated([]string{"leftovers", "--pause"})
	reportElevated(app, code, err)
}
