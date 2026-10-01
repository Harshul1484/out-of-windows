package cli

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/cleanup"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type cleanOptions struct {
	dryRun    bool
	yes       bool
	all       bool
	details   bool
	list      bool
	whitelist bool
	pause     bool // keep an elevated window open at the end
	rules     []string
}

func newCleanCmd(app *App) *cobra.Command {
	var o cleanOptions
	cmd := &cobra.Command{
		Use:     "clean",
		Short:   "Remove temporary files, caches and logs that are safe to delete",
		GroupID: "clean",
		Long: "Scan for reclaimable temporary files, caches and logs, show what was found and\n" +
			"why it is safe, let you choose, then delete only what you confirmed.\n\n" +
			"Each item is re-verified immediately before deletion: links are never followed,\n" +
			"files changed since the scan are kept, and files in use are skipped.",
		Example: "  " + buildinfo.Name + " clean --dry-run\n" +
			"  " + buildinfo.Name + " clean --rule temp.user --yes\n" +
			"  " + buildinfo.Name + " clean --dry-run --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runClean(cmd.Context(), app, o)
			if o.pause {
				if err != nil {
					fmt.Fprintf(app.Err, "%s %v\n", ui.Err.Render("error:"), err)
				}
				fmt.Fprint(app.Out, "\n Press Enter to close this window.")
				_, _ = bufio.NewReader(app.In).ReadString('\n')
			}
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would be removed without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation (required for non-interactive cleaning)")
	f.BoolVar(&o.all, "all", false, "select every target, including ones not selected by default")
	f.BoolVar(&o.details, "details", false, "list every file that would be removed")
	f.BoolVar(&o.list, "list", false, "list cleanup targets with explanations and exit")
	f.BoolVar(&o.whitelist, "whitelist", false, "choose cleanup targets to protect (never clean)")
	f.StringSliceVar(&o.rules, "rule", nil, "only these targets (IDs or prefixes, e.g. temp.user or browser)")
	f.BoolVar(&o.pause, "pause", false, "wait for Enter before exiting (used for elevated windows)")
	_ = f.MarkHidden("pause")
	return cmd
}

func runClean(ctx context.Context, app *App, o cleanOptions) error {
	if err := app.requireConfig(); err != nil {
		return err
	}
	switch {
	case o.list:
		return listRules(app)
	case o.whitelist:
		return editRuleWhitelist(app)
	}

	if app.ForceDryRun {
		o.dryRun = true
	}
	rules, err := pickRules(o.rules)
	if err != nil {
		return err
	}
	env := app.cleanupEnv()

	if !app.JSON {
		app.header("Deep clean", o.dryRun)
	}
	var prog cleanup.Progress
	spin := ui.StartSpinner(app.Err, app.tty(), func() string {
		return fmt.Sprintf("Scanning %s %s files %s %s", prog.Current(), ui.SymDot,
			ui.Count(int(prog.Items.Load())), ui.Bytes(prog.Bytes.Load()))
	})
	res := cleanup.Scan(ctx, env, rules, &prog)
	spin.Stop()
	if res.Cancelled {
		return errCancelled
	}

	explicit := len(o.rules) > 0
	selected := map[string]bool{}
	for _, rs := range res.Rules {
		if rs.Status == cleanup.StatusReady && (explicit || o.all || rs.Rule.DefaultSelected) {
			selected[rs.Rule.ID] = true
		}
	}

	if o.dryRun {
		if app.JSON {
			return app.printJSON(cleanReport(app, res, selected, nil, true, o.details))
		}
		printScan(app, res, selected, o.details)
		app.printf("\n %s\n\n", ui.Muted.Render("Dry run: nothing was changed. Run `"+buildinfo.Name+" clean` to clean."))
		return nil
	}

	if !app.JSON {
		printScan(app, res, selected, o.details)
	}
	if res.Files() == 0 {
		if app.JSON {
			return app.printJSON(cleanReport(app, res, selected, nil, false, o.details))
		}
		app.printf("\n %s %s\n\n", ui.OK.Render(ui.SymOK), "Nothing to clean.")
		if !o.yes {
			offerElevation(ctx, app, res)
		}
		return nil
	}

	// Choose.
	switch {
	case app.interactive() && !o.yes:
		chosen, ok, err := chooseRules(res, selected)
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
		selected = chosen
	case !o.yes:
		return withCode(ExitNeedsConfirm, "refusing to delete without confirmation: pass --yes, or use --dry-run to preview")
	}

	var chosen []*cleanup.RuleScan
	var files int
	var bytes int64
	for _, rs := range res.Rules {
		if selected[rs.Rule.ID] && rs.Status == cleanup.StatusReady {
			chosen = append(chosen, rs)
			files += rs.ItemCount()
			bytes += rs.Bytes
		}
	}
	if len(chosen) == 0 {
		if app.JSON {
			return app.printJSON(cleanReport(app, res, selected, nil, false, o.details))
		}
		app.printf("\n %s\n\n", ui.Muted.Render("Nothing selected. Nothing was changed."))
		return nil
	}

	// Confirm.
	if !o.yes {
		app.println()
		app.printf(" You are about to permanently delete %s %s from %s:\n",
			ui.Bold.Render(ui.Plural(files, "item", "items")), ui.Bold.Render("("+ui.Bytes(bytes)+")"),
			ui.Plural(len(chosen), "cleanup target", "cleanup targets"))
		for _, rs := range chosen {
			app.printf("   %s %s %s\n", ui.Accent.Render(ui.SymItem), rs.Rule.Name, ui.Muted.Render(ui.Bytes(rs.Bytes)))
		}
		app.printf(" %s\n", ui.Muted.Render("These are temporary files and caches that programs recreate. They are deleted,\n not moved to the Recycle Bin. Files that change or are in use are kept."))
		for _, rs := range chosen {
			if rs.Rule.Special == cleanup.SpecialRecycleBin {
				app.printf(" %s %s\n", ui.Warn.Render(ui.SymWarn+" Recycle Bin:"),
					ui.Plural(rs.ItemCount(), "item", "items")+" you deleted earlier will be gone for good and cannot be restored.")
			}
		}
		app.println()
		ok, err := confirmCtx(ctx, app, " Continue?")
		if err != nil {
			return err
		}
		if !ok {
			app.printf(" %s\n\n", ui.Muted.Render("Cancelled. Nothing was changed."))
			return nil
		}
	}

	// Act.
	prog = cleanup.Progress{}
	spin = ui.StartSpinner(app.Err, app.tty(), func() string {
		return fmt.Sprintf("Cleaning %s %s %s removed %s %s", prog.Current(), ui.SymDot,
			ui.Count(int(prog.Items.Load())), ui.SymDot, ui.Bytes(prog.Bytes.Load()))
	})
	out := cleanup.Execute(ctx, env, chosen, &prog)
	spin.Stop()

	recordClean(app, out)

	if app.JSON {
		if err := app.printJSON(cleanReport(app, res, selected, out, false, o.details)); err != nil {
			return err
		}
	} else {
		printOutcome(app, out)
	}
	if out.Cancelled {
		return errCancelled
	}
	if !o.yes {
		offerElevation(ctx, app, res)
	}
	return nil
}

// offerElevation lets an interactive, non-elevated user clean the targets
// that were skipped only for lack of administrator rights, in a separate
// elevated window that runs its own scan, checklist and confirmation.
func offerElevation(ctx context.Context, app *App, res *cleanup.ScanResult) {
	if !app.interactive() || app.Elevated || app.Sandbox != "" {
		return
	}
	var ids, names []string
	for _, rs := range res.Rules {
		if rs.NeedsAdmin {
			ids = append(ids, rs.Rule.ID)
			names = append(names, rs.Rule.Name)
		}
	}
	if len(ids) == 0 {
		return
	}
	app.printf(" %s %s need administrator rights: %s\n", ui.Warn.Render(ui.SymWarn),
		ui.Plural(len(ids), "target", "targets"), strings.Join(names, ", "))
	ok, err := confirmCtx(ctx, app, " Scan and clean them in an elevated window? You will see a Windows permission prompt.")
	if err != nil || !ok {
		return
	}
	code, err := relaunchElevated([]string{"clean", "--rule", strings.Join(ids, ","), "--pause"})
	reportElevated(app, code, err)
}

func (a *App) cleanupEnv() *cleanup.Env {
	env := &cleanup.Env{
		Locations: a.Locations,
		Guard:     a.Guard,
		Elevated:  a.Elevated,
		Disabled: func(r *cleanup.Rule) bool {
			for _, e := range a.Config.Whitelist.Rules {
				if r.MatchesID(e) {
					return true
				}
			}
			return false
		},
	}
	if a.Sandbox == "" {
		env.Running = system.RunningNames
		env.Specials = map[string]cleanup.Special{cleanup.SpecialRecycleBin: system.RecycleBin{}}
	} else {
		env.Specials = map[string]cleanup.Special{
			cleanup.SpecialRecycleBin: sandbox.RecycleBin{Dir: sandbox.RecycleBinDir(a.Sandbox)},
		}
	}
	return env
}

func pickRules(filters []string) ([]*cleanup.Rule, error) {
	all := cleanup.BuiltinRules()
	if len(filters) == 0 {
		return all, nil
	}
	var out []*cleanup.Rule
	for _, r := range all {
		for _, f := range filters {
			if r.MatchesID(f) {
				out = append(out, r)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, withCode(ExitUsage, "no cleanup target matches %s (see `%s clean --list`)",
			strings.Join(filters, ", "), buildinfo.Name)
	}
	return out, nil
}

func chooseRules(res *cleanup.ScanResult, preselected map[string]bool) (map[string]bool, bool, error) {
	var items []ui.CheckItem
	var ids []string
	byCat := groupByCategory(res.Rules)
	for _, c := range cleanup.CategoryOrder {
		list := byCat[c]
		var ready []*cleanup.RuleScan
		for _, rs := range list {
			if rs.Status == cleanup.StatusReady {
				ready = append(ready, rs)
			}
		}
		if len(ready) == 0 {
			continue
		}
		items = append(items, ui.CheckItem{Header: c.Title()})
		ids = append(ids, "")
		for _, rs := range ready {
			items = append(items, ui.CheckItem{
				Label:   rs.Rule.Name,
				Right:   ui.PadLeft(ui.Bytes(rs.Bytes), 9) + ui.Muted.Render("  "+ui.Plural(rs.ItemCount(), "item", "items")),
				Checked: preselected[rs.Rule.ID],
				Weight:  rs.Bytes,
				Detail: []string{
					"What   " + rs.Rule.What,
					"Safe   " + rs.Rule.WhySafe,
					"After  " + rs.Rule.Impact,
				},
			})
			ids = append(ids, rs.Rule.ID)
		}
	}
	r, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Select what to clean", ConfirmVerb: "continue", ShowWeight: true}, items)
	if err != nil || !r.Confirmed {
		return nil, false, err
	}
	out := map[string]bool{}
	for _, i := range r.Checked {
		out[ids[i]] = true
	}
	return out, true, nil
}

func groupByCategory(scans []*cleanup.RuleScan) map[cleanup.Category][]*cleanup.RuleScan {
	out := map[cleanup.Category][]*cleanup.RuleScan{}
	for _, rs := range scans {
		out[rs.Rule.Category] = append(out[rs.Rule.Category], rs)
	}
	return out
}

func printScan(app *App, res *cleanup.ScanResult, selected map[string]bool, details bool) {
	width := min(ui.Width(), 100)
	// Targets with nothing to clean (including software that is not
	// installed) are summarised in one line unless --details is set.
	shown := func(rs *cleanup.RuleScan) bool { return details || rs.Status != cleanup.StatusEmpty }
	nameW, hidden := 24, 0
	for _, rs := range res.Rules {
		if shown(rs) {
			nameW = max(nameW, len([]rune(rs.Rule.Name)))
		} else {
			hidden++
		}
	}
	nameW = min(nameW, 40)
	byCat := groupByCategory(res.Rules)
	var recent, skipped int
	for _, c := range cleanup.CategoryOrder {
		var list []*cleanup.RuleScan
		for _, rs := range byCat[c] {
			if shown(rs) {
				list = append(list, rs)
			}
		}
		if len(list) == 0 {
			continue
		}
		app.printf(" %s\n", ui.Bold.Render(c.Title()))
		for _, rs := range list {
			name := ui.PadRight(rs.Rule.Name, nameW)
			switch rs.Status {
			case cleanup.StatusReady:
				mark := ui.Accent.Render(ui.SymItem)
				if !selected[rs.Rule.ID] {
					mark = ui.Muted.Render(ui.SymSkip)
				}
				app.printf("   %s %s %s  %s\n", mark, name, ui.PadLeft(ui.Bold.Render(ui.Bytes(rs.Bytes)), 9),
					ui.Muted.Render(ui.Plural(rs.ItemCount(), "item", "items")))
			case cleanup.StatusEmpty:
				app.printf("   %s %s %s\n", ui.OK.Render(ui.SymOK), ui.Muted.Render(name), ui.Muted.Render("nothing to clean"))
			case cleanup.StatusReview:
				app.printf("   %s %s %s\n", ui.Warn.Render(ui.SymWarn), name,
					ui.Warn.Render("review required: ")+ui.Muted.Render(ui.TruncateMiddle(rs.Reason, width-nameW-25)))
			default:
				app.printf("   %s %s %s\n", ui.Muted.Render(ui.SymSkip), ui.Muted.Render(name),
					ui.Muted.Render(ui.TruncateMiddle(rs.Reason, width-nameW-8)))
			}
			if rs.KeptRecent > 0 {
				recent += rs.KeptRecent
				app.printf("       %s\n", ui.Muted.Render(fmt.Sprintf("kept %s changed in the last %s",
					ui.Plural(rs.KeptRecent, "recent file", "recent files"), humanAge(rs.Rule.MinAge))))
			}
			if n := rs.Skipped.Total(); n > 0 {
				skipped += n
				app.printf("       %s\n", ui.Muted.Render(skippedLine(&rs.Skipped)))
			}
			if details && rs.Status == cleanup.StatusReady {
				for _, it := range rs.Files {
					app.printf("       %s  %s\n", ui.PadLeft(ui.Bytes(it.Fingerprint.Size), 9),
						ui.Muted.Render(ui.TruncateMiddle(it.Path, width-20)))
				}
			}
		}
		app.println()
	}
	if hidden > 0 {
		app.printf(" %s %s\n\n", ui.OK.Render(ui.SymOK), ui.Muted.Render(fmt.Sprintf(
			"%s nothing to clean or not installed (--details lists them)", ui.Plural(hidden, "other target has", "other targets have"))))
	}

	var selBytes int64
	var selFiles int
	for _, rs := range res.Rules {
		if selected[rs.Rule.ID] && rs.Status == cleanup.StatusReady {
			selBytes += rs.Bytes
			selFiles += rs.ItemCount()
		}
	}
	app.printf(" %s\n", ui.Divider(width-2))
	app.printf(" %s  %s %s\n", ui.PadRight("Reclaimable", 12), ui.Title.Render(ui.Bytes(res.Bytes())),
		ui.Muted.Render("in "+ui.Plural(res.Files(), "item", "items")))
	if selBytes != res.Bytes() {
		app.printf(" %s  %s %s\n", ui.PadRight("Selected", 12), ui.Bold.Render(ui.Bytes(selBytes)),
			ui.Muted.Render("in "+ui.Plural(selFiles, "item", "items")+"  ("+ui.SymSkip+" = not selected by default)"))
	}
	app.printf(" %s\n", ui.Muted.Render("Scanned in "+ui.Duration(res.Duration)))
}

func printOutcome(app *App, out *cleanup.Outcome) {
	width := min(ui.Width(), 100)
	nameW := 24
	for _, ro := range out.Rules {
		nameW = max(nameW, len([]rune(ro.Rule.Name)))
	}
	nameW = min(nameW, 40)
	app.println()
	for _, ro := range out.Rules {
		mark := ui.OK.Render(ui.SymOK)
		if ro.Errors > 0 {
			mark = ui.Err.Render(ui.SymErr)
		} else if ro.Removed == 0 {
			mark = ui.Muted.Render(ui.SymSkip)
		}
		app.printf("   %s %s %s  %s\n", mark, ui.PadRight(ro.Rule.Name, nameW), ui.PadLeft(ui.Bold.Render(ui.Bytes(ro.Reclaimed)), 9),
			ui.Muted.Render(ui.Plural(ro.Removed, "item", "items")+" removed"))
		if n := ro.Skipped.Total(); n > 0 {
			app.printf("       %s\n", ui.Muted.Render(ui.SymSkip+" "+skippedLine(&ro.Skipped)))
		}
	}
	app.printf("\n %s\n", ui.Divider(width-2))
	title := "Cleanup complete"
	if out.Cancelled {
		title = "Cleanup stopped"
	}
	app.printf(" %s\n\n", ui.Title.Render(title))
	files, dirs := 0, 0
	for _, ro := range out.Rules {
		files += ro.Removed
		dirs += ro.DirsRemoved
	}
	removed := ui.Plural(files, "item", "items")
	if dirs > 0 {
		removed += ", " + ui.Plural(dirs, "empty folder", "empty folders")
	}
	app.printf(" %s %s\n", ui.PadRight("Removed", 11), removed)
	freed := ""
	if d := out.FreedOnDisk(); d > 0 {
		freed = ui.Muted.Render("   free space +" + ui.Bytes(d))
	}
	app.printf(" %s %s%s\n", ui.PadRight("Reclaimed", 11), ui.Bold.Render(ui.Bytes(out.Reclaimed)), freed)
	app.printf(" %s %s\n", ui.PadRight("Skipped", 11), ui.Plural(out.Skipped, "item", "items"))
	errs := ui.Count(out.Errors)
	if out.Errors > 0 {
		errs = ui.Err.Render(errs) + ui.Muted.Render("  (details in "+app.Dirs.LogDir()+")")
	}
	app.printf(" %s %s\n\n", ui.PadRight("Errors", 11), errs)
}

// skippedLine renders "skipped 1 item: reason" or
// "skipped 5 items: reason (3), other reason (2)".
func skippedLine(t *cleanup.Tally) string {
	rs := t.Reasons()
	head := "skipped " + ui.Plural(t.Total(), "item", "items") + ": "
	if len(rs) == 1 {
		return head + rs[0].Reason
	}
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.Reason, ui.Count(r.Count)))
	}
	return head + strings.Join(parts, ", ")
}

func humanAge(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0 && d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	case d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	}
	return d.String()
}

func recordClean(app *App, out *cleanup.Outcome) {
	rec := history.Record{
		Time:       time.Now(),
		Command:    "clean",
		Sandbox:    app.Sandbox != "",
		Removed:    out.Removed,
		Reclaimed:  out.Reclaimed,
		Skipped:    out.Skipped,
		Errors:     out.Errors,
		Cancelled:  out.Cancelled,
		DurationMS: out.Duration.Milliseconds(),
	}
	for _, ro := range out.Rules {
		t := history.TargetStat{ID: ro.Rule.ID, Name: ro.Rule.Name, Removed: ro.Removed + ro.DirsRemoved,
			Reclaimed: ro.Reclaimed, Skipped: ro.Skipped.Total(), Errors: ro.Errors}
		for _, r := range ro.Skipped.Reasons() {
			t.SkipReasons = append(t.SkipReasons, history.ReasonCount{Reason: r.Reason, Count: r.Count})
		}
		rec.Targets = append(rec.Targets, t)
	}
	if err := history.Append(app.Dirs.Data, rec); err != nil {
		fmt.Fprintf(app.Err, "%s could not record history: %v\n", ui.Warn.Render("warning:"), err)
	}
}

func listRules(app *App) error {
	rules := cleanup.BuiltinRules()
	if app.JSON {
		type ruleInfo struct {
			ID              string   `json:"id"`
			Name            string   `json:"name"`
			Category        string   `json:"category"`
			Roots           []string `json:"roots"`
			MinAgeHours     float64  `json:"min_age_hours"`
			What            string   `json:"what"`
			WhySafe         string   `json:"why_safe"`
			Impact          string   `json:"impact"`
			RequiresAdmin   bool     `json:"requires_admin"`
			DefaultSelected bool     `json:"default_selected"`
			Whitelisted     bool     `json:"whitelisted"`
		}
		env := app.cleanupEnv()
		out := make([]ruleInfo, 0, len(rules))
		for _, r := range rules {
			out = append(out, ruleInfo{r.ID, r.Name, string(r.Category), r.Roots, r.MinAge.Hours(),
				r.What, r.WhySafe, r.Impact, r.RequiresAdmin, r.DefaultSelected, env.Disabled(r)})
		}
		return app.printJSON(map[string]any{"schema": "oow.rules/v1", "rules": out})
	}
	width := min(ui.Width(), 100)
	app.header("Cleanup targets", false)
	env := app.cleanupEnv()
	for _, c := range cleanup.CategoryOrder {
		first := true
		for _, r := range rules {
			if r.Category != c {
				continue
			}
			if first {
				app.printf(" %s\n", ui.Bold.Render(c.Title()))
				first = false
			}
			tags := []string{r.ID}
			if r.RequiresAdmin {
				tags = append(tags, "admin")
			}
			if !r.DefaultSelected {
				tags = append(tags, "opt-in")
			}
			if env.Disabled(r) {
				tags = append(tags, "whitelisted")
			}
			app.printf("   %s %s  %s\n", ui.Accent.Render(ui.SymItem), ui.Bold.Render(r.Name), ui.Muted.Render(strings.Join(tags, " "+ui.SymDot+" ")))
			for _, line := range [][2]string{{"What", r.What}, {"Safe", r.WhySafe}, {"After", r.Impact}} {
				app.printf("     %s %s\n", ui.Muted.Render(ui.PadRight(line[0], 6)), ui.Wrap(line[1], width-14, "            "))
			}
			app.println()
		}
	}
	return nil
}

func editRuleWhitelist(app *App) error {
	if !app.interactive() {
		return withCode(ExitUsage, "--whitelist is interactive; use `%s config whitelist add <rule-id>` in scripts", buildinfo.Name)
	}
	rules := cleanup.BuiltinRules()
	env := app.cleanupEnv()
	var items []ui.CheckItem
	var ids []string
	for _, c := range cleanup.CategoryOrder {
		header := false
		for _, r := range rules {
			if r.Category != c {
				continue
			}
			if !header {
				items = append(items, ui.CheckItem{Header: c.Title()})
				ids = append(ids, "")
				header = true
			}
			items = append(items, ui.CheckItem{Label: r.Name, Right: ui.Muted.Render(r.ID), Checked: env.Disabled(r),
				Detail: []string{"What   " + r.What}})
			ids = append(ids, r.ID)
		}
	}
	res, err := ui.RunChecklist(ui.ChecklistOptions{Title: "Protect cleanup targets (checked = never clean)", ConfirmVerb: "save"}, items)
	if err != nil || !res.Confirmed {
		return err
	}
	checked := map[string]bool{}
	for _, i := range res.Checked {
		checked[ids[i]] = true
	}
	changed := false
	for _, r := range rules {
		switch {
		case checked[r.ID] && !env.Disabled(r):
			changed = app.Config.AddRule(r.ID) || changed
		case !checked[r.ID]:
			changed = app.Config.Remove(r.ID) || changed
		}
	}
	if !changed {
		app.printf(" %s\n", ui.Muted.Render("No changes."))
		return nil
	}
	if err := app.Config.Save(app.Dirs.ConfigFile()); err != nil {
		return err
	}
	var protected []string
	for _, r := range rules {
		if env.Disabled(r) {
			protected = append(protected, r.ID)
		}
	}
	sort.Strings(protected)
	app.printf(" %s Saved. Protected targets: %s\n", ui.OK.Render(ui.SymOK), strings.Join(protected, ", "))
	return nil
}

// confirmCtx prompts on stdin but gives up if ctx is cancelled (Ctrl+C).
func confirmCtx(ctx context.Context, app *App, question string) (bool, error) {
	ch := make(chan bool, 1)
	go func() { ch <- ui.Confirm(app.In, app.Out, question) }()
	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		return false, errCancelled
	}
}
