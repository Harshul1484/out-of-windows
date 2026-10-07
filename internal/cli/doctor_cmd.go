package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/cleanup"
	"github.com/Harshul1484/out-of-windows/internal/doctor"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// doctorProbe and pathStore return real or simulated implementations.
func (a *App) doctorProbe() doctor.Probe {
	if a.Sandbox != "" {
		return sandbox.Doctor{Root: a.Sandbox}
	}
	return doctor.System{}
}

func (a *App) pathStore() envpath.Store {
	if a.Sandbox != "" {
		return sandbox.Paths{Root: a.Sandbox}
	}
	return envpath.System{}
}

// pathReports reads and analyzes both PATH variables.
func (a *App) pathReports() (user, machine *envpath.Report) {
	store := a.pathStore()
	opts := envpath.Options{Expand: store.Expand, Probe: system.ProbePath, Profile: a.Locations.UserProfile}
	read := func(scope envpath.Scope) *envpath.Report {
		v, err := store.Read(scope)
		if err != nil {
			return &envpath.Report{Scope: scope, Entries: []envpath.Entry{}, Error: err.Error()}
		}
		r := envpath.Analyze(scope, v, opts)
		return &r
	}
	return read(envpath.User), read(envpath.Machine)
}

// doctorFacts gathers everything the checks look at. With scan set, the
// default cleanup targets are scanned (read-only) for reclaimable space.
func (a *App) doctorFacts(ctx context.Context, scan bool) doctor.Facts {
	dirs := []doctor.Dir{
		{ID: "temp", Label: "Temp folder", Path: a.Locations.Temp},
		{ID: "data", Label: buildinfo.Name + " data folder", Path: a.Dirs.Data},
	}
	f := doctor.Collect(a.doctorProbe(), dirs)
	f.UserPath, f.MachinePath = a.pathReports()
	f.Startup, _, f.StartupErr = a.startupStore().List(ctx)
	if scan && ctx.Err() == nil {
		var rules []*cleanup.Rule
		for _, r := range cleanup.BuiltinRules() {
			if r.DefaultSelected {
				rules = append(rules, r)
			}
		}
		res := cleanup.Scan(ctx, a.cleanupEnv(), rules, nil)
		rc := &doctor.Reclaimable{Bytes: res.Bytes(), Items: res.Files(), Partial: res.Cancelled}
		for _, rs := range res.Rules {
			if rs.Status == cleanup.StatusReady {
				rc.Targets++
			}
		}
		f.Reclaimable = rc
	}
	return f
}

func newDoctorCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Diagnose common Windows problems (changes nothing)",
		GroupID: "system",
		Long: "Check free space, pending restarts, Windows Update settings, PATH, startup entries,\n" +
			"reclaimable caches, network configuration, folder permissions and package managers.\n" +
			"Each problem comes with a one-line explanation and a next step. doctor only reads;\n" +
			"`" + buildinfo.Name + " repair` can fix PATH and startup findings for the current user.\n\n" +
			"The network check reads the local adapter configuration only; nothing is sent.",
		Example: "  " + buildinfo.Name + " doctor\n  " + buildinfo.Name + " doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.Context(), app)
		},
	}
}

func runDoctor(ctx context.Context, app *App) error {
	spin := ui.StartSpinner(app.Err, app.tty(), func() string { return "Checking the system" })
	f := app.doctorFacts(ctx, true)
	spin.Stop()
	if ctx.Err() != nil {
		return errCancelled
	}
	checks := doctor.Evaluate(f)
	if app.ConfigErr != nil {
		checks = append(checks, doctor.Check{ID: "config", Category: "tools", Title: buildinfo.Name + " configuration",
			Status: doctor.Problem, Summary: "config.json cannot be read, so cleanup commands are blocked",
			Details: []string{app.ConfigErr.Error()}, Next: "Fix or delete " + app.Dirs.ConfigFile() + "."})
	}
	for i := range checks {
		if checks[i].Details == nil {
			checks[i].Details = []string{}
		}
	}
	sum := doctor.Summarize(checks)
	if app.JSON {
		return app.printJSON(map[string]any{"schema": "oow.doctor/v1", "sandbox": app.Sandbox != "",
			"elevated": app.Elevated, "checks": checks, "summary": sum})
	}
	app.header("System doctor", false)
	printChecks(app, checks)
	width := min(ui.Width(), 100)
	app.printf(" %s\n", ui.Divider(width-2))
	if sum.Issues == 0 {
		app.printf(" %s %s", ui.OK.Render(ui.SymOK), ui.Bold.Render("No issues found"))
	} else {
		app.printf(" %s %s", ui.Warn.Render(ui.SymWarn), ui.Bold.Render(ui.Plural(sum.Issues, "issue found", "issues found")))
		app.printf(" %s", ui.Muted.Render(fmt.Sprintf("(%s, %s)", ui.Plural(sum.Problems, "problem", "problems"),
			ui.Plural(sum.Warnings, "warning", "warnings"))))
	}
	app.printf("  %s\n", ui.Muted.Render(fmt.Sprintf("%s %d ok %s %d info %s %d unknown", ui.SymDot, sum.OK, ui.SymDot, sum.Info, ui.SymDot, sum.Unknown)))
	app.printf(" %s\n\n", ui.Muted.Render("doctor only reads; nothing was changed."))
	return nil
}

func checkMark(s doctor.Status) string {
	switch s {
	case doctor.OK:
		return ui.OK.Render(ui.SymOK + " ok")
	case doctor.Warning:
		return ui.Warn.Render(ui.SymWarn + " warning")
	case doctor.Problem:
		return ui.Err.Render(ui.SymErr + " problem")
	case doctor.Info:
		return ui.Muted.Render(ui.SymSkip + " info")
	}
	return ui.Muted.Render(ui.SymSkip + " unknown")
}

func printChecks(app *App, checks []doctor.Check) {
	width := min(ui.Width(), 110)
	titleW := 16
	for _, c := range checks {
		titleW = max(titleW, len([]rune(c.Title)))
	}
	titleW = min(titleW, 22)
	indent := strings.Repeat(" ", 3+11+1)
	summaryIndent := indent + strings.Repeat(" ", titleW+1)
	plain := func(s ...string) string { return strings.Join(s, " ") }
	last := ""
	for _, c := range checks {
		if c.Category != last {
			if last != "" {
				app.println()
			}
			app.printf(" %s\n", ui.Bold.Render(doctor.CategoryTitle(c.Category)))
			last = c.Category
		}
		app.printf("   %s %s %s\n", ui.PadRight(checkMark(c.Status), 11), ui.PadRight(ui.TruncateMiddle(c.Title, titleW), titleW),
			wrapRender(plain, c.Summary, width-len(summaryIndent), summaryIndent))
		if c.Status == doctor.OK {
			continue
		}
		if c.Status != doctor.Info {
			shown := 0
			for _, d := range c.Details {
				if shown == 4 {
					app.printf("%s%s\n", indent, ui.Muted.Render(fmt.Sprintf("… and %d more (--json lists all)", len(c.Details)-shown)))
					break
				}
				app.printf("%s%s\n", indent, wrapRender(ui.Muted.Render, d, width-len(indent)-1, indent))
				shown++
			}
		}
		if c.Next != "" {
			app.printf("%s%s %s\n", indent, ui.Accent.Render("→"), wrapRender(plain, c.Next, width-len(indent)-3, indent+"  "))
		}
	}
	app.println()
}

// wrapRender wraps s to width and renders each line separately (a style
// applied to several lines would pad them into a block). Words longer than a
// line, such as paths, are shortened in the middle.
func wrapRender(render func(...string) string, s string, width int, indent string) string {
	if strings.Contains(s, `:\`) || strings.Contains(s, `\\`) {
		// A line naming a path stays one line, shortened in the middle, so
		// the path is not split at its spaces.
		return render(ui.TruncateMiddle(s, max(width, 20)))
	}
	words := strings.Fields(s)
	for i, w := range words {
		if width > 10 && len([]rune(w)) > width {
			words[i] = ui.TruncateMiddle(w, width)
		}
	}
	lines := strings.Split(ui.Wrap(strings.Join(words, " "), width, ""), "\n")
	for i := range lines {
		lines[i] = render(lines[i])
	}
	return strings.Join(lines, "\n"+indent)
}
