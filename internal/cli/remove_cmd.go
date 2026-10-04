package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/install"
	"github.com/Harshul1484/out-of-windows/internal/logging"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/selfupdate"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type removeOptions struct {
	dryRun   bool
	yes      bool
	keepData bool
}

func newRemoveCmd(app *App) *cobra.Command {
	var o removeOptions
	cmd := &cobra.Command{
		Use:     "remove",
		Short:   "Uninstall " + buildinfo.Name + " itself",
		GroupID: "tool",
		Long: "Remove " + buildinfo.Name + " from this computer: the program the installer put in\n" +
			"%LOCALAPPDATA%\\Programs\\" + buildinfo.AppID + ", the user PATH entry it added, and your settings,\n" +
			"history and logs, which go to the Recycle Bin. Everything is listed before anything changes.\n\n" +
			"Windows does not let a running program delete its own file, so the last step (deleting\n" +
			buildinfo.Name + ".exe) is a command shown at the end. Installs managed by winget, Scoop or\n" +
			"Chocolatey are removed with that package manager instead.",
		Example: "  " + buildinfo.Name + " remove --dry-run\n" +
			"  " + buildinfo.Name + " remove --keep-data\n" +
			"  " + buildinfo.Name + " remove --yes --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd.Context(), app, o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would be removed without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation (required for non-interactive use)")
	f.BoolVar(&o.keepData, "keep-data", false, "keep settings, history and logs")
	return cmd
}

// removeJSON is the oow.remove/v1 document.
type removeJSON struct {
	Schema      string           `json:"schema"`
	DryRun      bool             `json:"dry_run"`
	Sandbox     bool             `json:"sandbox"`
	KeepData    bool             `json:"keep_data"`
	ManagedBy   *managedJSON     `json:"managed_by"`
	Executed    bool             `json:"executed"`
	Items       []removeItemJSON `json:"items"`
	ManualSteps []manualStepJSON `json:"manual_steps"`
	Errors      int              `json:"errors"`
	Message     string           `json:"message"`
	Error       string           `json:"error,omitempty"`
}

// Item kinds, actions and statuses of oow.remove/v1.
const (
	kindExe        = "executable"
	kindInstallDir = "install-dir"
	kindPathEntry  = "path-entry"
	kindConfigDir  = "config-dir"
	kindDataDir    = "data-dir"

	actRemove     = "remove"
	actRemoveIfMT = "remove-if-empty"
	actPath       = "remove-from-path"
	actRecycle    = "recycle"
	actKeep       = "keep"

	stPlanned = "planned"
	stDone    = "done"
	stKept    = "kept"
	stManual  = "manual"
	stFailed  = "failed"
)

type removeItemJSON struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Action string `json:"action"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Bytes  int64  `json:"bytes"`
}

type manualStepJSON struct {
	Shell   string `json:"shell"`
	Command string `json:"command"`
}

func runRemove(ctx context.Context, app *App, o removeOptions) error {
	if err := app.requireConfig(); err != nil {
		return err // the whitelist must be honoured
	}
	if app.ForceDryRun {
		o.dryRun = true
	}
	doc := removeJSON{Schema: "oow.remove/v1", DryRun: o.dryRun, Sandbox: app.Sandbox != "", KeepData: o.keepData,
		Items: []removeItemJSON{}, ManualSteps: []manualStepJSON{}}
	done := func(code int, msg string) error {
		doc.Message = msg
		if code != ExitOK {
			doc.Error = msg
		}
		if !app.JSON {
			if code != ExitOK {
				return withCode(code, "%s", msg)
			}
			app.printf(" %s\n\n", msg)
			return nil
		}
		if err := app.printJSON(doc); err != nil {
			return err
		}
		if code != ExitOK {
			return alreadyReported(code, "%s", msg)
		}
		return nil
	}

	self, err := app.self()
	if err != nil {
		return withCode(ExitError, "cannot locate this executable: %v", err)
	}
	if !app.JSON {
		app.header("Remove "+buildinfo.Name, o.dryRun)
	}
	if m := managedBy(self); m.Manager != install.None {
		doc.ManagedBy = managedDoc(m)
		return done(ExitError, fmt.Sprintf("%s was installed with %s; remove it with: %s (your settings in %s and history in %s are left for you to keep or delete)",
			buildinfo.Name, m.Label(), m.RemoveCommand(), app.Dirs.Config, app.Dirs.Data))
	}

	doc.Items = planRemove(app, self, o)
	if !app.JSON {
		printRemovePlan(app, doc.Items)
	}
	actionable := 0
	for _, it := range doc.Items {
		if it.Action != actKeep {
			actionable++
		}
	}
	if actionable == 0 {
		return done(ExitOK, "Nothing to remove.")
	}
	if o.dryRun {
		return done(ExitOK, "Dry run: nothing was changed.")
	}

	switch {
	case app.interactive() && !o.yes:
		q := fmt.Sprintf(" Remove %s?", buildinfo.Name)
		if !o.keepData {
			q += " Settings and history go to the Recycle Bin."
		}
		ok, err := confirmCtx(ctx, app, q)
		if err != nil {
			return err
		}
		if !ok {
			return done(ExitOK, "Nothing was changed.")
		}
	case !o.yes:
		return done(ExitNeedsConfirm, "refusing to remove without confirmation: pass --yes, or use --dry-run to preview")
	}

	start := time.Now()
	doc.Executed = true
	dataRemoved := executeRemove(app, self, doc.Items)
	for _, it := range doc.Items {
		switch it.Status {
		case stFailed:
			doc.Errors++
		case stManual:
			if it.Kind == kindExe {
				doc.ManualSteps = manualSteps(it.Path, app.installDir())
			}
		}
	}
	if !dataRemoved {
		// The history lives in the data folder; record only while it stays.
		rec := history.Record{Time: time.Now(), Command: "remove", Sandbox: app.Sandbox != "",
			Errors: doc.Errors, DurationMS: time.Since(start).Milliseconds()}
		for _, it := range doc.Items {
			if it.Status == stDone {
				rec.Removed++
				rec.Recycled += it.Bytes
			}
		}
		app.record(rec)
	}
	if !app.JSON {
		printRemoveOutcome(app, doc)
	}
	if doc.Errors > 0 {
		return done(ExitError, fmt.Sprintf("%s could not be removed; see above", ui.Plural(doc.Errors, "item", "items")))
	}
	if len(doc.ManualSteps) > 0 {
		return done(ExitOK, "Almost done: run the command above to delete the program file.")
	}
	if o.keepData {
		return done(ExitOK, buildinfo.Name+" was removed; your settings and history were kept.")
	}
	return done(ExitOK, buildinfo.Name+" was removed.")
}

// planRemove lists what remove would change and what it keeps, and why.
func planRemove(app *App, self selfInfo, o removeOptions) []removeItemJSON {
	var items []removeItemJSON
	dir := app.installDir()
	inDir := install.InDir(self.Exe, dir)
	installed := inDir
	if !inDir {
		_, err := statFile(filepath.Join(dir, install.ExeName()))
		installed = err == nil
	}

	// The program and its folder.
	switch {
	case inDir:
		items = append(items,
			removeItemJSON{Kind: kindExe, Path: self.Exe, Action: actRemove, Status: stPlanned},
			removeItemJSON{Kind: kindInstallDir, Path: dir, Action: actRemoveIfMT, Status: stPlanned})
	default:
		items = append(items, removeItemJSON{Kind: kindExe, Path: self.Exe, Action: actKeep, Status: stKept,
			Reason: fmt.Sprintf("not in the installer's folder (%s); %s only removes the copy its installer put there, so delete this file yourself if you no longer need it",
				dir, buildinfo.Name)})
	}

	// The user PATH entry the installer added.
	up := app.userPath()
	if value, _, err := up.Get(); err != nil {
		items = append(items, removeItemJSON{Kind: kindPathEntry, Path: dir, Action: actKeep, Status: stFailed,
			Reason: "cannot read the user PATH: " + err.Error()})
	} else if install.HasEntry(value, dir, up.Expand) {
		it := removeItemJSON{Kind: kindPathEntry, Path: dir, Action: actPath, Status: stPlanned}
		if installed && !inDir {
			it.Action, it.Status = actKeep, stKept
			it.Reason = "the installed copy in that folder is not the one running; run its own `" + buildinfo.Name + " remove`"
		}
		items = append(items, it)
	}

	// Settings, history and logs.
	for _, td := range app.toolDirs() {
		e, err := filesystem.Lstat(td.Path)
		if err != nil || !e.IsDir() {
			continue // nothing there
		}
		it := removeItemJSON{Kind: td.Kind, Path: td.Path, Action: actRecycle, Status: stPlanned}
		it.Bytes, _, _ = filesystem.SizeOf(context.Background(), td.Path)
		d := app.Guard.Check(safety.Request{Path: td.Path, Purpose: safety.PurposeSelfRemove})
		switch {
		case o.keepData:
			it.Action, it.Status, it.Reason = actKeep, stKept, "you chose --keep-data"
		case td.Override != "" && !td.Standard:
			it.Action, it.Status = actKeep, stKept
			it.Reason = "chosen with " + td.Override + "; " + buildinfo.Name + " never removes a folder set by an environment variable, so delete it yourself if you no longer need it"
		case !td.Standard:
			it.Action, it.Status = actKeep, stKept
			it.Reason = "not in the standard location, so it is left for you to delete"
		case e.Reparse:
			it.Action, it.Status, it.Reason = actKeep, stKept, "is a link or junction (not followed)"
		case !d.Allowed:
			it.Action, it.Status, it.Reason = actKeep, stKept, d.Reason
		}
		items = append(items, it)
	}
	return items
}

// executeRemove carries out the planned items in a safe order: PATH entry,
// then settings and data (Recycle Bin), then the program. It reports whether
// the data folder (and with it the history file) was removed.
func executeRemove(app *App, self selfInfo, items []removeItemJSON) (dataRemoved bool) {
	dir := app.installDir()
	byKind := func(kind string) *removeItemJSON {
		for i := range items {
			if items[i].Kind == kind && items[i].Status == stPlanned {
				return &items[i]
			}
		}
		return nil
	}
	fail := func(it *removeItemJSON, err error) { it.Status, it.Reason = stFailed, err.Error() }

	if it := byKind(kindPathEntry); it != nil {
		if _, err := install.RemoveFromUserPath(app.userPath(), dir); err != nil {
			fail(it, err)
		} else {
			it.Status = stDone
		}
	}

	// The log file is open inside the data folder: close it first, and keep
	// logging to stderr only (with --debug).
	if byKind(kindConfigDir) != nil || byKind(kindDataDir) != nil {
		app.close()
		app.closeLog = logging.Setup("", app.Debug, app.Err)
	}
	for _, kind := range []string{kindConfigDir, kindDataDir} {
		it := byKind(kind)
		if it == nil {
			continue
		}
		if err := recycleToolDir(app, it.Path); err != nil {
			fail(it, err)
			continue
		}
		it.Status = stDone
		if kind == kindDataDir {
			dataRemoved = true
		}
	}

	exe := byKind(kindExe)
	if exe == nil {
		return dataRemoved
	}
	// Files an earlier update left behind are not running: remove them now.
	var leftover []string
	for _, p := range []string{selfupdate.OldPath(self.Exe), selfupdate.StagedPath(self.Exe)} {
		if err := install.RemoveOwnFile(p, nil); err != nil {
			leftover = append(leftover, fmt.Sprintf("%s (%v)", p, err))
		}
	}
	dirItem := byKind(kindInstallDir)
	dirRemoved, err := app.exeRemover(self).RemoveExe(self.Exe, dir)
	switch {
	case errors.Is(err, install.ErrRunning):
		exe.Status, exe.Reason = stManual, err.Error()
		if dirItem != nil {
			dirItem.Status, dirItem.Reason = stManual, "removed by the same command, once it is empty"
		}
	case err != nil:
		fail(exe, err)
		if dirItem != nil {
			dirItem.Status, dirItem.Reason = stKept, "the program in it could not be removed"
		}
	default:
		exe.Status = stDone
		if dirItem != nil {
			if dirRemoved {
				dirItem.Status = stDone
			} else {
				dirItem.Status, dirItem.Reason = stKept, "it still contains other files"
			}
		}
	}
	if len(leftover) > 0 && exe.Status != stFailed {
		exe.Reason = strings.TrimSpace(exe.Reason + "; not removed: " + strings.Join(leftover, ", "))
	}
	return dataRemoved
}

// recycleToolDir moves one of oow's own folders to the Recycle Bin through
// the verified sink. The guard allows exactly that folder (PurposeSelfRemove)
// and re-checks the OS-resolved final path.
func recycleToolDir(app *App, path string) error {
	e, err := filesystem.Lstat(path)
	if err != nil {
		return err
	}
	if !e.IsDir() || e.Reparse {
		return fmt.Errorf("%s is not a plain folder; leaving it alone", path)
	}
	check := func(final string) error {
		if d := app.Guard.Check(safety.Request{Path: final, Purpose: safety.PurposeSelfRemove}); !d.Allowed {
			return errors.New(d.Reason)
		}
		return nil
	}
	return filesystem.RecycleVerified(path, e.Fingerprint, check, app.recycler())
}

// manualSteps are the commands that delete the program file once it is no
// longer running: the file first, then the folder only if it is empty.
func manualSteps(exe, dir string) []manualStepJSON {
	ps := "Remove-Item -LiteralPath '" + strings.ReplaceAll(exe, "'", "''") + "'"
	cmd := `del "` + exe + `"`
	if dir != "" {
		ps += "; Remove-Item -LiteralPath '" + strings.ReplaceAll(dir, "'", "''") + "'"
		cmd += ` && rmdir "` + dir + `"`
	}
	return []manualStepJSON{{Shell: "powershell", Command: ps}, {Shell: "cmd", Command: cmd}}
}

func removeLabel(kind string, sameDir bool) string {
	switch kind {
	case kindExe:
		return "Program"
	case kindInstallDir:
		return "Its folder"
	case kindPathEntry:
		return "PATH entry"
	case kindConfigDir:
		return "Settings"
	case kindDataDir:
		if sameDir {
			return "Settings, history"
		}
		return "History, logs"
	}
	return kind
}

func sameToolDirs(items []removeItemJSON) bool {
	for _, it := range items {
		if it.Kind == kindConfigDir {
			return false
		}
	}
	return true
}

// printRemovePlan lists every item with its full path: what is removed must
// be visible exactly, so paths are never shortened here.
func printRemovePlan(app *App, items []removeItemJSON) {
	same := sameToolDirs(items)
	width := min(ui.Width(), 110)
	indent := "     " + ui.PadRight("", 18) + " "
	for _, it := range items {
		label := ui.PadRight(removeLabel(it.Kind, same), 18)
		var what string
		switch it.Action {
		case actRemove:
			what = "delete"
		case actRemoveIfMT:
			what = "remove once empty"
		case actPath:
			what = "remove from user PATH"
		case actRecycle:
			what = "Recycle Bin, " + ui.Bytes(it.Bytes)
		}
		if it.Action == actKeep {
			mark := ui.Muted.Render(ui.SymSkip)
			if it.Status == stFailed {
				mark = ui.Err.Render(ui.SymErr)
			}
			app.printf("   %s %s %s\n", mark, ui.Muted.Render(label), it.Path)
			app.printf("%s%s\n", indent, ui.Muted.Render(ui.Wrap("kept: "+it.Reason, max(30, width-len(indent)), indent)))
			continue
		}
		app.printf("   %s %s %s %s\n", ui.Accent.Render(ui.SymItem), label, ui.Muted.Render(ui.PadRight(what, 22)), it.Path)
	}
	app.println()
}

func printRemoveOutcome(app *App, doc removeJSON) {
	same := sameToolDirs(doc.Items)
	for _, it := range doc.Items {
		if it.Action == actKeep {
			continue
		}
		label := ui.PadRight(removeLabel(it.Kind, same), 18)
		switch it.Status {
		case stDone:
			what := "removed"
			if it.Action == actRecycle {
				what = "moved to the Recycle Bin"
			}
			app.printf("   %s %s %s\n", ui.OK.Render(ui.SymOK), label, what)
		case stManual:
			app.printf("   %s %s %s\n", ui.Warn.Render(ui.SymWarn), label, "one step left (below)")
		case stKept:
			app.printf("   %s %s %s\n", ui.Muted.Render(ui.SymSkip), label, ui.Muted.Render("kept: "+it.Reason))
		case stFailed:
			app.printf("   %s %s %s\n", ui.Err.Render(ui.SymErr), label, it.Reason)
		}
	}
	if len(doc.ManualSteps) > 0 {
		app.printf("\n %s Windows does not let a running program delete its own file.\n", ui.Warn.Render(ui.SymWarn))
		app.printf(" After this command returns, run one of these to finish:\n")
		for _, s := range doc.ManualSteps {
			name := "PowerShell"
			if s.Shell == "cmd" {
				name = "Command Prompt"
			}
			app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight(name, 15)), s.Command)
		}
	}
	app.println()
}
