package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/purge"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type purgeOptions struct {
	dryRun bool
	yes    bool
	paths  bool
}

func newPurgeCmd(app *App) *cobra.Command {
	var o purgeOptions
	cmd := &cobra.Command{
		Use:     "purge [folder...]",
		Short:   "Remove rebuildable developer artifacts (node_modules, target, .venv, ...)",
		GroupID: "clean",
		Long: "Find dependency folders, build output and tool caches in your projects (node_modules,\n" +
			".next, target, build, bin/obj, .venv, __pycache__, .dart_tool, CMake build trees, ...) and\n" +
			"remove the ones you confirm. Folders selected by default are rebuilt by npm install, cargo\n" +
			"build, dotnet build and similar commands, so they are deleted permanently; folders you add\n" +
			"from review are moved to the Recycle Bin instead.\n\n" +
			"Only folders next to their project's marker file count (node_modules next to package.json).\n" +
			"Folders with Git-tracked files, a nested repository, links, keys or certificates are kept;\n" +
			"folders changed in the last 7 days are listed but not selected. Scans the folders you pass,\n" +
			"else the configured purge folders, else common project folders in your profile.",
		Example: "  " + buildinfo.Name + " purge --dry-run\n" +
			"  " + buildinfo.Name + " purge D:\\code --yes\n" +
			"  " + buildinfo.Name + " purge --paths\n" +
			"  " + buildinfo.Name + " config purge add D:\\code",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPurge(cmd.Context(), app, o, args)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would be removed without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "delete the folders selected by default without asking (required non-interactively)")
	f.BoolVar(&o.paths, "paths", false, "show which folders are scanned for projects and exit")
	return cmd
}

func (a *App) purgeEnv() *purge.Env {
	return &purge.Env{Guard: a.Guard, Locations: a.Locations, Ceiling: a.Sandbox}
}

// purgeJSON is the oow.purge/v1 document.
type purgeJSON struct {
	Schema   string           `json:"schema"`
	DryRun   bool             `json:"dry_run"`
	Sandbox  bool             `json:"sandbox"`
	Roots    []purge.Root     `json:"roots"`
	Projects []*purge.Project `json:"projects"`
	Summary  purgeSummaryJSON `json:"summary"`
}

type purgeSummaryJSON struct {
	Projects         int   `json:"projects"`
	Artifacts        int   `json:"artifacts"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	Selected         int   `json:"selected"`
	SelectedBytes    int64 `json:"selected_bytes"`
	Kept             int   `json:"kept"`
	ScanErrors       int   `json:"scan_errors"`
	Executed         bool  `json:"executed"`
	Removed          int   `json:"removed"`
	RemovedFiles     int   `json:"removed_files"`
	ReclaimedBytes   int64 `json:"reclaimed_bytes"`
	Recycled         int   `json:"recycled"`
	RecycledBytes    int64 `json:"recycled_bytes"`
	FreedOnDiskBytes int64 `json:"freed_on_disk_bytes"`
	Skipped          int   `json:"skipped"`
	Errors           int   `json:"errors"`
	Cancelled        bool  `json:"cancelled"`
	ScanMS           int64 `json:"scan_ms"`
	PurgeMS          int64 `json:"purge_ms"`
}

// purgeTotals counts offered (ready or review), selected and kept artifacts.
type purgeTotals struct {
	offered, selected, kept int
	offeredBytes, selBytes  int64
}

func totals(res *purge.Result) purgeTotals {
	var t purgeTotals
	for _, a := range res.Artifacts() {
		switch {
		case a.Status == purge.StatusKept:
			t.kept++
		default:
			t.offered++
			t.offeredBytes += a.Bytes
			if a.Selected {
				t.selected++
				t.selBytes += a.Bytes
			}
		}
	}
	return t
}

func purgeDoc(app *App, res *purge.Result, dryRun bool, out *purge.Outcome) purgeJSON {
	t := totals(res)
	doc := purgeJSON{Schema: "oow.purge/v1", DryRun: dryRun, Sandbox: app.Sandbox != "", Roots: res.Roots,
		Projects: res.Projects}
	if doc.Roots == nil {
		doc.Roots = []purge.Root{}
	}
	if doc.Projects == nil {
		doc.Projects = []*purge.Project{}
	}
	doc.Summary = purgeSummaryJSON{Projects: len(res.Projects), Artifacts: t.offered, ReclaimableBytes: t.offeredBytes,
		Selected: t.selected, SelectedBytes: t.selBytes, Kept: t.kept, ScanErrors: res.ScanErrors,
		ScanMS: res.Duration.Milliseconds()}
	if out != nil {
		doc.Summary.Executed = true
		doc.Summary.Removed, doc.Summary.RemovedFiles = out.Removed, out.RemovedFiles
		doc.Summary.ReclaimedBytes, doc.Summary.FreedOnDiskBytes = out.Reclaimed, out.FreedOnDisk()
		doc.Summary.Recycled, doc.Summary.RecycledBytes = out.Recycled, out.RecycledBytes
		doc.Summary.Skipped, doc.Summary.Errors = out.Skipped, out.Errors
		doc.Summary.Cancelled, doc.Summary.PurgeMS = out.Cancelled, out.Duration.Milliseconds()
	}
	return doc
}

func runPurge(ctx context.Context, app *App, o purgeOptions, args []string) error {
	if err := app.requireConfig(); err != nil {
		return err
	}
	if app.ForceDryRun {
		o.dryRun = true
	}
	env := app.purgeEnv()
	roots := purge.Roots(env, args, app.Config.Purge.Paths)
	if o.paths {
		return printPurgePaths(app, roots)
	}
	ok := 0
	var refused []string
	for _, r := range roots {
		if r.Status == purge.RootOK {
			ok++
		} else if r.Source != purge.SourceDefault {
			refused = append(refused, r.Path+": "+r.Reason)
		}
	}
	if len(args) > 0 && ok == 0 {
		return withCode(ExitUsage, "cannot scan %s", strings.Join(refused, "; "))
	}

	if !app.JSON {
		app.header("Project artifacts", o.dryRun)
	}
	var prog purge.Progress
	spin := ui.StartSpinner(app.Err, app.tty(), func() string {
		return fmt.Sprintf("Scanning %s %s %s %s %s", ui.TruncateMiddle(prog.Current(), 50), ui.SymDot,
			ui.Plural(int(prog.Projects.Load()), "project", "projects"), ui.SymDot, ui.Bytes(prog.Bytes.Load()))
	})
	res := purge.Find(ctx, env, roots, &prog)
	spin.Stop()
	if res.Cancelled {
		return errCancelled
	}
	t := totals(res)

	if o.dryRun || t.offered == 0 {
		if app.JSON {
			return app.printJSON(purgeDoc(app, res, o.dryRun, nil))
		}
		printPurgeScan(app, res)
		switch {
		case ok == 0:
			app.printf(" %s\n\n", ui.Muted.Render("No project folders to scan. Pass one (`"+buildinfo.Name+
				" purge D:\\code`) or add it with `"+buildinfo.Name+" config purge add <folder>`."))
		case o.dryRun:
			app.printf(" %s\n\n", ui.Muted.Render("Dry run: nothing was changed. Run `"+buildinfo.Name+" purge` to choose and delete."))
		default:
			app.printf(" %s %s\n\n", ui.OK.Render(ui.SymOK), "Nothing to purge.")
		}
		return nil
	}
	if !app.JSON {
		printPurgeScan(app, res)
	}

	// Choose.
	var chosen []*purge.Artifact
	switch {
	case app.interactive() && !o.yes:
		c, confirmed, err := choosePurge(res)
		if err != nil {
			return err
		}
		if !confirmed {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
		chosen = c
	case !o.yes:
		return withCode(ExitNeedsConfirm, "refusing to delete without confirmation: pass --yes, or use --dry-run to preview")
	default:
		for _, a := range res.Artifacts() {
			if a.Selected {
				chosen = append(chosen, a)
			}
		}
	}
	if len(chosen) == 0 {
		if app.JSON {
			return app.printJSON(purgeDoc(app, res, false, nil))
		}
		app.printf("\n %s\n\n", ui.Muted.Render("Nothing selected. Nothing was changed."))
		return nil
	}

	// Confirm.
	if !o.yes {
		var delBytes, binBytes int64
		var del, bin int
		rebuild := map[string]bool{}
		var how []string
		for _, a := range chosen {
			if a.Status == purge.StatusReady {
				del++
				delBytes += a.Bytes
			} else {
				bin++
				binBytes += a.Bytes
			}
			if !rebuild[a.Rebuild] {
				rebuild[a.Rebuild] = true
				how = append(how, a.Rebuild)
			}
		}
		app.println()
		if del > 0 {
			app.printf(" You are about to permanently delete %s %s.\n",
				ui.Bold.Render(ui.Plural(del, "folder", "folders")), ui.Bold.Render("("+ui.Bytes(delBytes)+")"))
		}
		if bin > 0 {
			app.printf(" %s %s you added from review will be moved to the Recycle Bin, so they can be restored.\n",
				ui.Bold.Render(ui.Plural(bin, "folder", "folders")), ui.Bold.Render("("+ui.Bytes(binBytes)+")"))
		}
		app.printf(" %s\n", ui.Muted.Render("Rebuild them when you need them:"))
		for i, h := range how {
			if i == 4 {
				app.printf("   %s\n", ui.Muted.Render(fmt.Sprintf("… and %d more", len(how)-4)))
				break
			}
			app.printf("   %s %s\n", ui.Accent.Render(ui.SymItem), h)
		}
		app.printf(" %s\n\n", ui.Muted.Render("Folders that change, hold Git-tracked files or are in use are kept."))
		yes, err := confirmCtx(ctx, app, " Continue?")
		if err != nil {
			return err
		}
		if !yes {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
	}

	// Act.
	prog = purge.Progress{}
	spin = ui.StartSpinner(app.Err, app.tty(), func() string {
		return fmt.Sprintf("Deleting %s %s %s removed", ui.TruncateMiddle(prog.Current(), 50), ui.SymDot, ui.Bytes(prog.Bytes.Load()))
	})
	out := purge.Remove(ctx, env, res, chosen, app.recycler(), &prog)
	spin.Stop()
	recordPurge(app, out)

	if app.JSON {
		if err := app.printJSON(purgeDoc(app, res, false, out)); err != nil {
			return err
		}
	} else {
		printPurgeOutcome(app, out)
	}
	if out.Cancelled {
		return errCancelled
	}
	return nil
}

func choosePurge(res *purge.Result) ([]*purge.Artifact, bool, error) {
	var items []ui.CheckItem
	var refs []*purge.Artifact
	for _, p := range res.Projects {
		header := false
		for _, a := range p.Artifacts {
			if a.Status == purge.StatusKept {
				continue
			}
			if !header {
				items = append(items, ui.CheckItem{Header: p.Path})
				refs = append(refs, nil)
				header = true
			}
			how := "How    deleted permanently"
			if a.Status != purge.StatusReady {
				how = "How    moved to the Recycle Bin (added from review)"
			}
			detail := []string{"What   " + a.Label + " (" + a.Ecosystem + ")", "Back   " + a.Rebuild, how}
			if len(a.Reasons) > 0 {
				detail = append(detail, "Note   "+strings.Join(a.Reasons, "; "))
			}
			items = append(items, ui.CheckItem{
				Label:   relTo(p.Path, a.Path),
				Right:   ui.PadLeft(ui.Bytes(a.Bytes), 9) + ui.Muted.Render("  "+ui.Plural(a.Files, "file", "files")),
				Checked: a.Selected,
				Weight:  a.Bytes,
				Detail:  detail,
			})
			refs = append(refs, a)
		}
	}
	r, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Select folders to remove (preselected ones are deleted, others recycled)",
		ConfirmVerb: "continue", ShowWeight: true}, items)
	if err != nil || !r.Confirmed {
		return nil, false, err
	}
	var out []*purge.Artifact
	for _, i := range r.Checked {
		if refs[i] != nil {
			out = append(out, refs[i])
		}
	}
	return out, true, nil
}

func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func printPurgeScan(app *App, res *purge.Result) {
	width := min(ui.Width(), 100)
	var scanned []string
	for _, r := range res.Roots {
		switch {
		case r.Status == purge.RootOK:
			scanned = append(scanned, r.Path)
		case r.Source != purge.SourceDefault:
			app.printf(" %s %s %s\n", ui.Warn.Render(ui.SymWarn), r.Path, ui.Muted.Render(r.Reason))
		}
	}
	if len(scanned) > 0 {
		app.printf(" %s %s\n\n", ui.Muted.Render("Scanned"), ui.Wrap(strings.Join(scanned, ", "), width-10, "         "))
	}
	nameW := 16
	for _, p := range res.Projects {
		for _, a := range p.Artifacts {
			if a.Status != purge.StatusKept {
				nameW = max(nameW, len([]rune(relTo(p.Path, a.Path))))
			}
		}
	}
	nameW = min(nameW, 32)
	var kept []*purge.Artifact
	for _, p := range res.Projects {
		header := false
		for _, a := range p.Artifacts {
			if a.Status == purge.StatusKept {
				kept = append(kept, a)
				continue
			}
			if !header {
				app.printf(" %s\n", ui.Bold.Render(ui.TruncateMiddle(p.Path, width-2)))
				header = true
			}
			mark := ui.Accent.Render(ui.SymItem)
			if !a.Selected {
				mark = ui.Muted.Render(ui.SymSkip)
			}
			app.printf("   %s %s %s  %s  %s\n", mark, ui.PadRight(ui.TruncateMiddle(relTo(p.Path, a.Path), nameW), nameW),
				ui.PadLeft(ui.Bold.Render(ui.Bytes(a.Bytes)), 9), ui.PadLeft(ui.Plural(a.Files, "file", "files"), 12),
				ui.Muted.Render(ui.Truncate(a.Label, max(10, width-nameW-34))))
			if len(a.Reasons) > 0 {
				app.printf("       %s\n", ui.Muted.Render(ui.Wrap(strings.Join(a.Reasons, "; "), width-10, "       ")))
			}
		}
		if header {
			app.println()
		}
	}
	if len(kept) > 0 {
		app.printf(" %s %s\n", ui.Bold.Render("Kept"), ui.Muted.Render("(never deleted)"))
		for i, a := range kept {
			if i == 8 {
				app.printf("   %s\n", ui.Muted.Render(fmt.Sprintf("… and %d more (--json lists them)", len(kept)-8)))
				break
			}
			app.printf("   %s %s\n", ui.Muted.Render(ui.SymSkip), ui.TruncateMiddle(a.Path, width-6))
			app.printf("       %s\n", ui.Muted.Render(ui.Wrap(strings.Join(a.Reasons, "; "), width-10, "       ")))
		}
		app.println()
	}
	t := totals(res)
	app.printf(" %s\n", ui.Divider(width-2))
	app.printf(" %s  %s %s\n", ui.PadRight("Reclaimable", 12), ui.Title.Render(ui.Bytes(t.offeredBytes)),
		ui.Muted.Render("in "+ui.Plural(t.offered, "folder", "folders")))
	if t.selected != t.offered {
		app.printf(" %s  %s %s\n", ui.PadRight("Selected", 12), ui.Bold.Render(ui.Bytes(t.selBytes)),
			ui.Muted.Render("in "+ui.Plural(t.selected, "folder", "folders")+"  ("+ui.SymSkip+" = not selected by default)"))
	}
	summary := fmt.Sprintf("%s in %s", ui.Plural(len(res.Projects), "project", "projects"), ui.Duration(res.Duration))
	if res.ScanErrors > 0 {
		summary += fmt.Sprintf("; %s could not be read", ui.Plural(res.ScanErrors, "folder", "folders"))
	}
	app.printf(" %s\n", ui.Muted.Render("Scanned "+summary))
}

func printPurgeOutcome(app *App, out *purge.Outcome) {
	width := min(ui.Width(), 100)
	app.println()
	for _, a := range out.Artifacts {
		r := a.Result
		switch {
		case r.Kept != "":
			app.printf("   %s %s %s\n", ui.Muted.Render(ui.SymSkip), ui.TruncateMiddle(a.Path, width-40), ui.Muted.Render("kept: "+r.Kept))
		case r.Method == purge.MethodRecycled:
			app.printf("   %s %s %s %s\n", ui.OK.Render(ui.SymOK), ui.TruncateMiddle(a.Path, width-40), ui.Bold.Render(ui.Bytes(r.RecycledBytes)),
				ui.Muted.Render("moved to the Recycle Bin"))
		case r.Complete:
			app.printf("   %s %s %s\n", ui.OK.Render(ui.SymOK), ui.TruncateMiddle(a.Path, width-20), ui.Bold.Render(ui.Bytes(r.Reclaimed)))
		default:
			mark := ui.Warn.Render(ui.SymWarn)
			if r.Errors > 0 {
				mark = ui.Err.Render(ui.SymErr)
			}
			app.printf("   %s %s %s %s\n", mark, ui.TruncateMiddle(a.Path, width-30), ui.Bold.Render(ui.Bytes(r.Reclaimed)),
				ui.Muted.Render("partly removed"))
			if r.Skipped > 0 {
				parts := make([]string, 0, len(r.SkipReasons))
				for _, s := range r.SkipReasons {
					parts = append(parts, fmt.Sprintf("%s (%s)", s.Reason, ui.Count(s.Count)))
				}
				app.printf("       %s\n", ui.Muted.Render("left "+ui.Plural(r.Skipped, "item", "items")+": "+strings.Join(parts, ", ")))
			}
		}
	}
	app.printf("\n %s\n", ui.Divider(width-2))
	title := "Purge complete"
	if out.Cancelled {
		title = "Purge stopped"
	}
	app.printf(" %s\n\n", ui.Title.Render(title))
	app.printf(" %s %s, %s\n", ui.PadRight("Deleted", 11), ui.Plural(out.Removed, "folder", "folders"),
		ui.Plural(out.RemovedFiles, "file", "files"))
	freed := ""
	if d := out.FreedOnDisk(); d > 0 {
		freed = ui.Muted.Render("   free space +" + ui.Bytes(d))
	}
	app.printf(" %s %s%s\n", ui.PadRight("Reclaimed", 11), ui.Bold.Render(ui.Bytes(out.Reclaimed)), freed)
	if out.Recycled > 0 {
		app.printf(" %s %s %s\n", ui.PadRight("Recycled", 11), ui.Plural(out.Recycled, "folder", "folders"),
			ui.Muted.Render("("+ui.Bytes(out.RecycledBytes)+" moved to the Recycle Bin; restore from there if needed)"))
	}
	app.printf(" %s %s\n", ui.PadRight("Skipped", 11), ui.Plural(out.Skipped, "item", "items"))
	errs := ui.Count(out.Errors)
	if out.Errors > 0 {
		errs = ui.Err.Render(errs) + ui.Muted.Render("  (details in "+app.Dirs.LogDir()+")")
	}
	app.printf(" %s %s\n\n", ui.PadRight("Errors", 11), errs)
}

func recordPurge(app *App, out *purge.Outcome) {
	rec := history.Record{Time: time.Now(), Command: "purge", Sandbox: app.Sandbox != "", Reclaimed: out.Reclaimed,
		Recycled: out.RecycledBytes, Skipped: out.Skipped, Errors: out.Errors, Cancelled: out.Cancelled,
		DurationMS: out.Duration.Milliseconds()}
	for _, a := range out.Artifacts {
		r := a.Result
		t := history.TargetStat{ID: a.Path, Name: a.Label, Removed: r.RemovedFiles + r.RemovedDirs,
			Reclaimed: r.Reclaimed, Skipped: r.Skipped, Errors: r.Errors}
		if r.Method == purge.MethodRecycled {
			t.Removed = 1 // the folder, moved whole to the Recycle Bin
		}
		if r.Kept != "" {
			t.Skipped++
			t.SkipReasons = append(t.SkipReasons, history.ReasonCount{Reason: r.Kept, Count: 1})
			rec.Skipped++
		}
		for _, s := range r.SkipReasons {
			t.SkipReasons = append(t.SkipReasons, history.ReasonCount{Reason: s.Reason, Count: s.Count})
		}
		rec.Removed += t.Removed
		rec.Targets = append(rec.Targets, t)
	}
	app.record(rec)
}

// purgePathsJSON is the oow.purge-paths/v1 document.
type purgePathsJSON struct {
	Schema     string       `json:"schema"`
	ConfigFile string       `json:"config_file"`
	Configured []string     `json:"configured"`
	Roots      []purge.Root `json:"roots"`
}

func printPurgePaths(app *App, roots []purge.Root) error {
	configured := app.Config.Purge.Paths
	if configured == nil {
		configured = []string{}
	}
	if roots == nil {
		roots = []purge.Root{}
	}
	if app.JSON {
		return app.printJSON(purgePathsJSON{Schema: "oow.purge-paths/v1", ConfigFile: app.Dirs.ConfigFile(),
			Configured: configured, Roots: roots})
	}
	app.header("Purge folders", false)
	if len(roots) == 0 {
		app.printf("   %s\n", ui.Muted.Render("none: no configured folders and none of the usual project folders exist"))
	}
	sorted := append([]purge.Root(nil), roots...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Status == purge.RootOK && sorted[j].Status != purge.RootOK })
	for _, r := range sorted {
		mark, note := ui.Accent.Render(ui.SymItem), r.Source
		if r.Status != purge.RootOK {
			mark, note = ui.Warn.Render(ui.SymWarn), r.Source+" "+ui.SymDot+" "+r.Reason
		}
		app.printf("   %s %s %s\n", mark, r.Path, ui.Muted.Render(note))
	}
	app.printf("\n %s\n\n", ui.Muted.Render("Change them with `"+buildinfo.Name+" config purge add|remove <folder>`; folders passed to `"+
		buildinfo.Name+" purge` replace them for that run."))
	return nil
}

// newConfigPurgeCmd manages the folders purge scans.
func newConfigPurgeCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "List, add or remove folders scanned for project artifacts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireConfig(); err != nil {
				return err
			}
			return printPurgePaths(app, purge.Roots(app.purgeEnv(), nil, app.Config.Purge.Paths))
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "add <folder>...",
		Short:   "Scan these folders for projects (instead of the usual project folders)",
		Example: "  " + buildinfo.Name + " config purge add D:\\code \"%USERPROFILE%\\source\\repos\"",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireConfig(); err != nil {
				return err
			}
			for _, a := range args {
				r := purge.Roots(app.purgeEnv(), []string{a}, nil)
				if len(r) != 1 {
					return withCode(ExitUsage, "%s cannot be scanned", a)
				}
				if r[0].Status != purge.RootOK {
					return withCode(ExitUsage, "%s cannot be scanned: %s", a, r[0].Reason)
				}
				added, err := app.Config.AddPurgePath(r[0].Path)
				if err != nil {
					return withCode(ExitUsage, "%q: %v", a, err)
				}
				if !app.JSON {
					if added {
						app.printf(" %s will scan %s\n", ui.OK.Render(ui.SymOK), r[0].Path)
					} else {
						app.printf(" %s %s is already listed\n", ui.Muted.Render(ui.SymSkip), r[0].Path)
					}
				}
			}
			if err := app.Config.Save(app.Dirs.ConfigFile()); err != nil {
				return err
			}
			if app.JSON {
				return printPurgePaths(app, purge.Roots(app.purgeEnv(), nil, app.Config.Purge.Paths))
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <folder>...",
		Aliases: []string{"rm"},
		Short:   "Stop scanning these folders",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireConfig(); err != nil {
				return err
			}
			for _, a := range args {
				removed := app.Config.RemovePurgePath(a)
				if app.JSON {
					continue
				}
				if removed {
					app.printf(" %s removed %s\n", ui.OK.Render(ui.SymOK), a)
				} else {
					app.printf(" %s %s was not listed\n", ui.Muted.Render(ui.SymSkip), a)
				}
			}
			if err := app.Config.Save(app.Dirs.ConfigFile()); err != nil {
				return err
			}
			if app.JSON {
				return printPurgePaths(app, purge.Roots(app.purgeEnv(), nil, app.Config.Purge.Paths))
			}
			return nil
		},
	})
	return cmd
}
