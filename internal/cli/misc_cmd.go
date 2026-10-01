package cli

import (
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version and system information",
		GroupID: "tool",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			osInfo := system.OS()
			if app.JSON {
				return app.printJSON(map[string]any{
					"schema":   "oow.version/v1",
					"name":     buildinfo.Name,
					"version":  buildinfo.Version,
					"commit":   buildinfo.Commit,
					"built":    buildinfo.Date,
					"arch":     runtime.GOARCH,
					"os":       osInfo,
					"elevated": system.IsElevated(),
				})
			}
			app.println(buildinfo.String())
			app.println(osInfo.String())
			if !osInfo.Supported() {
				app.println(ui.Warn.Render(ui.SymWarn + " This Windows version is older than the supported minimum (Windows 10 1809)."))
			}
			return nil
		},
	}
}

// newSandboxCmd creates a simulated Windows layout for safe end-to-end
// testing. It is hidden from help because it is a development tool.
func newSandboxCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "sandbox",
		Short:  "Developer tools for the simulated sandbox",
		Hidden: true,
		// Never touch the real system's config, logs or locations.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			ui.Init("auto", app.NoColor || app.JSON)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init <dir>",
		Short: "Create or refresh a simulated Windows layout with sample junk in <dir>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			if err := sandbox.Seed(dir); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]string{"sandbox": dir})
			}
			app.printf("%s Sandbox ready: %s\n\n", ui.OK.Render(ui.SymOK), dir)
			app.printf("Run any command against it:\n  %s --sandbox \"%s\" clean --dry-run\n", buildinfo.Name, dir)
			app.printf("or set %s=%s\n", sandbox.EnvVar, dir)
			return nil
		},
	})
	return cmd
}
