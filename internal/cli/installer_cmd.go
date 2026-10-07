package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/installer"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type installerOptions struct {
	dryRun bool
	yes    bool
}

func newInstallerCmd(app *App) *cobra.Command {
	var o installerOptions
	cmd := &cobra.Command{
		Use:     "installer [folder...]",
		Aliases: []string{"installers"},
		Short:   "Find installer files you no longer need and move them to the Recycle Bin",
		GroupID: "clean",
		Long: "Look in Downloads, Desktop and Documents (or the folders you pass) for installer packages:\n" +
			"Windows Installer packages and patches, MSIX/APPX packages, setup programs, archives with a\n" +
			"setup program at their root, and disc images. Files are identified by their content, never\n" +
			"by their name alone, and compared exactly with your installed apps.\n\n" +
			"Packages whose app is installed and that are older than 7 days are selected; the rest are\n" +
			"listed for review. Confirmed packages go to the Recycle Bin, so they can be restored.",
		Example: "  " + buildinfo.Name + " installer --dry-run\n" +
			"  " + buildinfo.Name + " installer --yes\n" +
			"  " + buildinfo.Name + " installer D:\\Setups --dry-run --json",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstaller(cmd.Context(), app, o, args)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show installer packages without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "move the packages selected by default without asking (required non-interactively)")
	return cmd
}

// installerFolders are where installers are looked for by default.
func (a *App) installerFolders() []string {
	if a.Sandbox != "" {
		return []string{sandbox.DownloadsDir(a.Sandbox), sandbox.DesktopDir(a.Sandbox), sandbox.DocumentsDir(a.Sandbox)}
	}
	return installer.SystemFolders()
}

// installerJSON is the oow.installer/v1 document.
type installerJSON struct {
	Schema     string               `json:"schema"`
	DryRun     bool                 `json:"dry_run"`
	Sandbox    bool                 `json:"sandbox"`
	Folders    []installer.Folder   `json:"folders"`
	Installers []*installer.Package `json:"installers"`
	Warnings   []string             `json:"warnings"`
	Summary    installerSummaryJSON `json:"summary"`
	Recycled   *installer.Outcome   `json:"recycled,omitempty"`
}

type installerSummaryJSON struct {
	Installers    int   `json:"installers"`
	Bytes         int64 `json:"bytes"`
	Selected      int   `json:"selected"`
	SelectedBytes int64 `json:"selected_bytes"`
	ScanErrors    int   `json:"scan_errors"`
	Executed      bool  `json:"executed"`
	RecycledFiles int   `json:"recycled"`
	RecycledBytes int64 `json:"recycled_bytes"`
	Skipped       int   `json:"skipped"`
	Errors        int   `json:"errors"`
	Cancelled     bool  `json:"cancelled"`
	ScanMS        int64 `json:"scan_ms"`
}

func installerDoc(app *App, res *installer.Result, dryRun bool, warnings []string, out *installer.Outcome) installerJSON {
	doc := installerJSON{Schema: "oow.installer/v1", DryRun: dryRun, Sandbox: app.Sandbox != "", Folders: res.Folders,
		Installers: res.Packages, Warnings: warnings, Recycled: out}
	if doc.Folders == nil {
		doc.Folders = []installer.Folder{}
	}
	if doc.Installers == nil {
		doc.Installers = []*installer.Package{}
	}
	if doc.Warnings == nil {
		doc.Warnings = []string{}
	}
	s := &doc.Summary
	for _, p := range res.Packages {
		s.Installers++
		s.Bytes += p.Size
		if p.Selected {
			s.Selected++
			s.SelectedBytes += p.Size
		}
	}
	s.ScanErrors, s.ScanMS = res.ScanErrors, res.Duration.Milliseconds()
	if out != nil {
		s.Executed = true
		s.RecycledFiles, s.RecycledBytes = len(out.Recycled), out.Bytes
		s.Skipped, s.Errors, s.Cancelled = len(out.Skipped), out.Errors, out.Cancelled
	}
	return doc
}

func runInstaller(ctx context.Context, app *App, o installerOptions, args []string) error {
	if err := app.requireConfig(); err != nil {
		return err
	}
	if app.ForceDryRun {
		o.dryRun = true
	}
	env := &installer.Env{Guard: app.Guard, Locations: app.Locations}
	paths := args
	if len(paths) == 0 {
		paths = app.installerFolders()
	}
	folders := installer.ResolveFolders(env, paths)
	ok := 0
	var refused []string
	for _, f := range folders {
		if f.Status == "ok" {
			ok++
		} else {
			refused = append(refused, f.Path+": "+f.Reason)
		}
	}
	if len(args) > 0 && ok == 0 {
		return withCode(ExitUsage, "cannot search %s", strings.Join(refused, "; "))
	}

	var warnings []string
	inv, err := app.loadInventory(ctx, "Reading installed apps")
	switch {
	case err == errCancelled:
		return err
	case err != nil:
		warnings = append(warnings, "installed apps could not be read ("+err.Error()+"), so nothing is selected by default")
	default:
		env.Inventory = inv
		for _, w := range inv.Warnings {
			warnings = append(warnings, w)
		}
	}

	if !app.JSON {
		app.header("Installer packages", o.dryRun)
	}
	var prog installer.Progress
	spin := ui.StartSpinner(app.Err, app.tty(), func() string {
		return fmt.Sprintf("Looking in %s %s %s found", ui.TruncateMiddle(prog.Current(), 50), ui.SymDot,
			ui.Plural(int(prog.Found.Load()), "installer", "installers"))
	})
	res := installer.Find(ctx, env, folders, &prog)
	spin.Stop()
	if res.Cancelled {
		return errCancelled
	}

	if o.dryRun || len(res.Packages) == 0 {
		if app.JSON {
			return app.printJSON(installerDoc(app, res, o.dryRun, warnings, nil))
		}
		printInstallers(app, res, warnings)
		if o.dryRun {
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was changed. Run `"+buildinfo.Name+" installer` to choose."))
		} else {
			app.printf(" %s %s\n\n", ui.OK.Render(ui.SymOK), "No installer packages found.")
		}
		return nil
	}
	if !app.JSON {
		printInstallers(app, res, warnings)
	}

	var chosen []*installer.Package
	switch {
	case app.interactive() && !o.yes:
		c, confirmed, err := chooseInstallers(res)
		if err != nil {
			return err
		}
		if !confirmed {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
		chosen = c
	case !o.yes:
		return withCode(ExitNeedsConfirm, "refusing to move installers without confirmation: pass --yes, or use --dry-run to preview")
	default:
		for _, p := range res.Packages {
			if p.Selected {
				chosen = append(chosen, p)
			}
		}
	}
	if len(chosen) == 0 {
		if app.JSON {
			return app.printJSON(installerDoc(app, res, false, warnings, nil))
		}
		app.printf("\n %s\n\n", ui.Muted.Render("Nothing selected. Nothing was changed."))
		return nil
	}
	if !o.yes {
		var bytes int64
		for _, p := range chosen {
			bytes += p.Size
		}
		app.println()
		yes, err := confirmCtx(ctx, app, fmt.Sprintf(" Move %s (%s) to the Recycle Bin? You can restore them from there.",
			ui.Plural(len(chosen), "installer", "installers"), ui.Bytes(bytes)))
		if err != nil {
			return err
		}
		if !yes {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
	}

	start := time.Now()
	out := installer.Recycle(ctx, app.Guard, chosen, app.recycler())
	recordInstallers(app, out, time.Since(start))
	if app.JSON {
		if err := app.printJSON(installerDoc(app, res, false, warnings, out)); err != nil {
			return err
		}
	} else {
		printInstallerOutcome(app, out)
	}
	if out.Cancelled {
		return errCancelled
	}
	return nil
}

func installerLine(p *installer.Package) string {
	what := strings.TrimSpace(p.Product + " " + p.Version)
	if what == "" {
		what = p.Format
	}
	switch p.Installed.Status {
	case "installed":
		what += " " + ui.SymDot + " installed"
	case "not-found":
		what += " " + ui.SymDot + " not installed"
	}
	return what
}

func chooseInstallers(res *installer.Result) ([]*installer.Package, bool, error) {
	var items []ui.CheckItem
	var refs []*installer.Package
	for _, f := range res.Folders {
		header := false
		for _, p := range res.Packages {
			if f.Status != "ok" || !inFolder(p.Path, f.Path, res.Folders) {
				continue
			}
			if !header {
				items = append(items, ui.CheckItem{Header: f.Path})
				refs = append(refs, nil)
				header = true
			}
			detail := []string{"What   " + installerLine(p) + " (" + p.Format + ")", "Why    " + strings.Join(p.Evidence, "; ")}
			if len(p.Reasons) > 0 {
				detail = append(detail, "Note   "+strings.Join(p.Reasons, "; "))
			}
			items = append(items, ui.CheckItem{
				Label:   relTo(f.Path, p.Path),
				Right:   ui.PadLeft(ui.Bytes(p.Size), 9) + ui.Muted.Render(fmt.Sprintf("  %4d days", p.AgeDays)),
				Checked: p.Selected,
				Weight:  p.Size,
				Detail:  detail,
			})
			refs = append(refs, p)
		}
	}
	r, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Select installers to move to the Recycle Bin", ConfirmVerb: "continue", ShowWeight: true}, items)
	if err != nil || !r.Confirmed {
		return nil, false, err
	}
	var out []*installer.Package
	for _, i := range r.Checked {
		if refs[i] != nil {
			out = append(out, refs[i])
		}
	}
	return out, true, nil
}

// inFolder reports whether p belongs to folder: inside it and not inside a
// more specific searched folder.
func inFolder(p, folder string, all []installer.Folder) bool {
	if !safety.IsStrictlyWithin(p, folder) {
		return false
	}
	for _, f := range all {
		if f.Status == "ok" && len(f.Path) > len(folder) && safety.IsStrictlyWithin(p, f.Path) {
			return false
		}
	}
	return true
}

func printInstallers(app *App, res *installer.Result, warnings []string) {
	width := min(ui.Width(), 110)
	for _, w := range warnings {
		app.printf(" %s %s\n", ui.Warn.Render(ui.SymWarn), ui.Muted.Render(w))
	}
	nameW := 20
	for _, p := range res.Packages {
		nameW = max(nameW, len([]rune(p.Name)))
	}
	nameW = min(nameW, 26)
	var total, sel int64
	selN := 0
	for _, f := range res.Folders {
		if f.Status != "ok" {
			if f.Status == "refused" || f.Status == "missing" {
				app.printf(" %s %s %s\n", ui.Warn.Render(ui.SymWarn), f.Path, ui.Muted.Render(f.Reason))
			}
			continue
		}
		var list []*installer.Package
		for _, p := range res.Packages {
			if inFolder(p.Path, f.Path, res.Folders) {
				list = append(list, p)
			}
		}
		if len(list) == 0 {
			continue
		}
		app.printf(" %s\n", ui.Bold.Render(f.Path))
		for _, p := range list {
			total += p.Size
			mark := ui.Accent.Render(ui.SymItem)
			if p.Selected {
				sel += p.Size
				selN++
			} else {
				mark = ui.Muted.Render(ui.SymSkip)
			}
			info := installerLine(p) + " " + ui.SymDot + " " + ui.Plural(p.AgeDays, "day", "days") + " old"
			app.printf("   %s %s %s  %s\n", mark, ui.PadRight(ui.TruncateMiddle(relTo(f.Path, p.Path), nameW), nameW),
				ui.PadLeft(ui.Bold.Render(ui.Bytes(p.Size)), 9), ui.Muted.Render(ui.Truncate(info, max(10, width-nameW-17))))
			if len(p.Reasons) > 0 {
				app.printf("       %s\n", ui.Muted.Render(ui.Wrap(strings.Join(p.Reasons, "; "), width-10, "       ")))
			}
		}
		app.println()
	}
	app.printf(" %s\n", ui.Divider(width-2))
	app.printf(" %s  %s %s\n", ui.PadRight("Found", 10), ui.Title.Render(ui.Bytes(total)),
		ui.Muted.Render("in "+ui.Plural(len(res.Packages), "installer", "installers")))
	if selN != len(res.Packages) {
		app.printf(" %s  %s %s\n", ui.PadRight("Selected", 10), ui.Bold.Render(ui.Bytes(sel)),
			ui.Muted.Render("in "+ui.Plural(selN, "installer", "installers")+"  ("+ui.SymSkip+" = review first: not selected by default)"))
	}
	note := "Searched in " + ui.Duration(res.Duration)
	if res.ScanErrors > 0 {
		note += fmt.Sprintf("; %s could not be read", ui.Plural(res.ScanErrors, "folder", "folders"))
	}
	app.printf(" %s\n", ui.Muted.Render(note))
}

func printInstallerOutcome(app *App, out *installer.Outcome) {
	width := min(ui.Width(), 110)
	app.println()
	for _, r := range out.Recycled {
		app.printf("   %s %s %s\n", ui.OK.Render(ui.SymOK), ui.PadLeft(ui.Bytes(r.Size), 9), ui.TruncateMiddle(r.Path, width-16))
	}
	for _, s := range out.Skipped {
		app.printf("   %s %s %s\n", ui.Muted.Render(ui.SymSkip), ui.TruncateMiddle(s.Path, width-40), ui.Muted.Render(s.Reason))
	}
	app.printf("\n %s %s moved to the Recycle Bin %s\n\n", ui.OK.Render(ui.SymOK), ui.Bytes(out.Bytes),
		ui.Muted.Render("(restore from the Recycle Bin if needed)"))
}

func recordInstallers(app *App, out *installer.Outcome, d time.Duration) {
	rec := history.Record{Time: time.Now(), Command: "installer", Sandbox: app.Sandbox != "",
		Removed: len(out.Recycled), Recycled: out.Bytes, Skipped: len(out.Skipped), Errors: out.Errors,
		Cancelled: out.Cancelled, DurationMS: d.Milliseconds()}
	for _, r := range out.Recycled {
		rec.Targets = append(rec.Targets, history.TargetStat{ID: r.Path, Name: "installer", Removed: 1})
	}
	for _, s := range out.Skipped {
		rec.Targets = append(rec.Targets, history.TargetStat{ID: s.Path, Name: "installer", Skipped: 1,
			SkipReasons: []history.ReasonCount{{Reason: s.Reason, Count: 1}}})
	}
	app.record(rec)
}
