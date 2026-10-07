package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// Main runs the CLI with the process's arguments and streams and returns the
// exit code.
func Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		// After the first Ctrl+C, restore default handling so a second one
		// terminates immediately.
		<-ctx.Done()
		stop()
	}()
	return Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// Run executes the CLI. It is the entry point used by tests.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	app := &App{In: stdin, Out: stdout, Err: stderr}
	defer app.close()
	root := newRootCmd(app)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	code := ExitError
	var ee *exitError
	if errors.As(err, &ee) {
		code = ee.code
	} else if strings.HasPrefix(err.Error(), "unknown command") ||
		strings.HasPrefix(err.Error(), "unknown flag") ||
		strings.Contains(err.Error(), "flag needs an argument") ||
		strings.Contains(err.Error(), "invalid argument") ||
		strings.Contains(err.Error(), "accepts ") {
		code = ExitUsage
	}
	if code == ExitCancelled {
		fmt.Fprintln(stderr, "\nCancelled. Nothing further was changed.")
		return code
	}
	if ee != nil && ee.reported {
		return code // the command already showed it (or wrote its JSON document)
	}
	if app.JSON {
		_ = app.printJSON(map[string]any{"error": err.Error(), "exit_code": code})
	} else {
		fmt.Fprintf(stderr, "%s %s\n", ui.Err.Render("error:"), err)
	}
	return code
}

func newRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   buildinfo.Name,
		Short: "Clean, uninstall, analyze and monitor Windows from one terminal tool",
		Long: buildinfo.Name + " is a Windows-native system maintenance tool: deep cleanup, complete app\n" +
			"uninstall, disk analysis, installer cleanup, developer artifact purge, a live\n" +
			"system monitor and diagnostics.\n\n" +
			"Every destructive operation is previewed first, protected by safety checks,\n" +
			"confirmed by you, re-verified at deletion time, and recorded in history.\n" +
			"Use --dry-run with any destructive command to see what would happen.",
		Version:       buildinfo.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return app.setup()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !app.interactive() {
				return cmd.Help()
			}
			return runHome(cmd.Context(), app, cmd)
		},
	}
	root.SetVersionTemplate(buildinfo.String() + "\n")
	pf := root.PersistentFlags()
	pf.BoolVar(&app.JSON, "json", false, "machine-readable JSON output (no colors or prompts)")
	pf.BoolVar(&app.NoColor, "no-color", false, "disable colors (also honours NO_COLOR)")
	pf.BoolVar(&app.Debug, "debug", false, "write debug logs to stderr")
	pf.StringVar(&app.SandboxDir, "sandbox", "", "run against a simulated Windows layout in this folder")
	_ = pf.MarkHidden("sandbox")

	root.AddGroup(
		&cobra.Group{ID: "clean", Title: "Clean:"},
		&cobra.Group{ID: "analyze", Title: "Analyze:"},
		&cobra.Group{ID: "system", Title: "System:"},
		&cobra.Group{ID: "tool", Title: "Tool:"},
	)
	root.AddCommand(
		newCleanCmd(app),
		newUninstallCmd(app),
		newLeftoversCmd(app),
		newPurgeCmd(app),
		newInstallerCmd(app),
		newAnalyzeCmd(app),
		newStatusCmd(app),
		newProcessesCmd(app),
		newConfigCmd(app),
		newHistoryCmd(app),
		newVersionCmd(app),
		newSandboxCmd(app),
	)
	for _, p := range plannedCommands {
		root.AddCommand(newPlannedCmd(app, p))
	}
	root.SetHelpCommandGroupID("tool")
	root.SetCompletionCommandGroupID("tool")
	return root
}

// homeEntries maps home-screen items to commands.
var homeEntries = []ui.MenuItem{
	{Section: "Clean", Label: "Deep Clean", Command: "clean", Available: true},
	{Label: "Uninstall Apps", Command: "uninstall"},
	{Label: "Remove Installers", Command: "installer"},
	{Section: "Analyze", Label: "Disk Analyzer", Command: "analyze"},
	{Label: "Large Files", Command: "analyze --large"},
	{Label: "Project Artifacts", Command: "purge"},
	{Section: "System", Label: "Optimize", Command: "optimize"},
	{Label: "Startup Apps", Command: "startup"},
	{Label: "Live Status", Command: "status"},
	{Label: "System Doctor", Command: "doctor"},
}

func runHome(ctx context.Context, app *App, root *cobra.Command) error {
	osInfo := system.OS()
	items := make([]ui.MenuItem, len(homeEntries))
	copy(items, homeEntries)
	for i := range items {
		name := strings.Fields(items[i].Command)[0]
		if c, _, err := root.Find([]string{name}); err == nil && c.Annotations["planned"] == "" {
			items[i].Available = true
		} else if err == nil {
			items[i].Note = items[i].Label + " is coming in " + c.Annotations["planned"] + "."
		}
	}
	banner := ""
	if app.Sandbox != "" {
		banner = "SANDBOX MODE: paths are simulated under " + app.Sandbox
	}
	var cpu system.CPUSampler
	cpu.Sample()
	sysDrive := system.SystemDrive()
	choice, err := ui.RunHome(ui.HomeOptions{
		Product:  productName(),
		OS:       osInfo.Short(),
		Banner:   banner,
		Elevated: app.Elevated && app.Sandbox == "",
		Items:    items,
		Stats: func() ui.HomeStats {
			var s ui.HomeStats
			s.CPU, s.CPUReady = cpu.Sample()
			if m, err := system.MemoryStatus(); err == nil {
				s.MemUsed, s.MemTotal = m.Used, m.Total
			}
			if d, err := system.DiskUsage(sysDrive); err == nil {
				s.DiskRoot, s.DiskUsed, s.DiskTotal = d.Root, d.Used, d.Total
			}
			s.NetDown = -1
			return s
		},
	})
	if err != nil || choice == "" {
		return err
	}
	args := strings.Fields(choice)
	cmd, rest, err := root.Find(args)
	if err != nil {
		return err
	}
	if err := cmd.ParseFlags(rest); err != nil {
		return err
	}
	cmd.SetContext(ctx)
	return cmd.RunE(cmd, cmd.Flags().Args())
}

// planned describes a command scheduled for a later phase.
type planned struct {
	use, short, phase, group string
	aliases                  []string
}

var plannedCommands = []planned{
	{"optimize", "Run bounded, explained maintenance tasks", "Phase 6", "system", nil},
	{"startup", "Review and disable startup applications", "Phase 6", "system", nil},
	{"doctor", "Diagnose common Windows problems", "Phase 6", "system", nil},
	{"repair", "Guided repair for problems found by doctor", "Phase 6", "system", nil},
	{"update", "Update " + buildinfo.Name + " to the latest release", "Phase 8", "tool", nil},
	{"remove", "Uninstall " + buildinfo.Name + " itself", "Phase 8", "tool", nil},
}

func newPlannedCmd(app *App, p planned) *cobra.Command {
	return &cobra.Command{
		Use:                p.use,
		Short:              p.short + " (coming in " + p.phase + ")",
		GroupID:            p.group,
		Aliases:            p.aliases,
		Annotations:        map[string]string{"planned": p.phase},
		Args:               cobra.ArbitraryArgs,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			return withCode(ExitNotImplemented, "%s %s is not available in this build yet (planned for %s)",
				buildinfo.Name, p.use, p.phase)
		},
	}
}
