package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/cleanup"
	"github.com/Harshul1484/out-of-windows/internal/config"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

func newConfigCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "Show settings and manage the whitelist of protected paths and targets",
		GroupID: "tool",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showConfig(app)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.JSON {
				return app.printJSON(map[string]string{"config_file": app.Dirs.ConfigFile(), "data_dir": app.Dirs.Data})
			}
			app.println(app.Dirs.ConfigFile())
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "protected",
		Short: "List every location the safety layer protects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			locs := app.Guard.ProtectedLocations()
			if app.JSON {
				return app.printJSON(map[string]any{"schema": "oow.protected/v1", "locations": locs})
			}
			app.header("Protected locations", false)
			kinds := map[string]string{
				"never-remove": "never removed itself, nor any folder containing it",
				"system-tree":  "system-owned: cleanup only inside reviewed exemptions",
				"user-content": "your files: never touched by automatic cleanup",
				"protected":    "whitelisted or used by " + buildinfo.Name,
				"sensitive":    "credentials, keys, wallets, VM and AI-tool state: never deleted",
			}
			for _, k := range []string{"system-tree", "user-content", "sensitive", "protected", "never-remove"} {
				app.printf(" %s %s\n", ui.Bold.Render(k), ui.Muted.Render(ui.SymDot+" "+kinds[k]))
				for _, l := range locs {
					if l.Kind == k {
						app.printf("   %s %s\n", ui.Muted.Render(ui.SymItem), l.Path)
					}
				}
				app.println()
			}
			return nil
		},
	})
	cmd.AddCommand(newWhitelistCmd(app))
	return cmd
}

func showConfig(app *App) error {
	if err := app.requireConfig(); err != nil {
		return err
	}
	if app.JSON {
		return app.printJSON(map[string]any{
			"schema":      "oow.config/v1",
			"config_file": app.Dirs.ConfigFile(),
			"data_dir":    app.Dirs.Data,
			"sandbox":     app.Sandbox != "",
			"config":      app.Config,
		})
	}
	app.header("Configuration", false)
	app.printf(" %s %s\n", ui.PadRight("Config", 10), app.Dirs.ConfigFile())
	app.printf(" %s %s\n", ui.PadRight("Data", 10), app.Dirs.Data)
	app.printf(" %s %s\n\n", ui.PadRight("Color", 10), orDefault(app.Config.UI.Color, "auto"))
	printWhitelist(app)
	return nil
}

func printWhitelist(app *App) {
	wl := app.Config.Whitelist
	app.printf(" %s\n", ui.Bold.Render("Whitelist (never cleaned)"))
	if len(wl.Paths) == 0 && len(wl.Rules) == 0 {
		app.printf("   %s\n", ui.Muted.Render("empty "+ui.SymDot+" add with `"+buildinfo.Name+" config whitelist add <path|target-id>`"))
	}
	for _, p := range wl.Paths {
		app.printf("   %s %s\n", ui.Accent.Render(ui.SymItem), p)
	}
	for _, r := range wl.Rules {
		app.printf("   %s %s %s\n", ui.Accent.Render(ui.SymItem), r, ui.Muted.Render("(cleanup target)"))
	}
	app.println()
}

func newWhitelistCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whitelist",
		Short: "List, add or remove protected paths and cleanup targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireConfig(); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(app.Config.Whitelist)
			}
			app.println()
			printWhitelist(app)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "add <path|target-id>...",
		Short: "Protect paths (and everything inside) or cleanup targets (IDs or prefixes)",
		Example: "  " + buildinfo.Name + " config whitelist add \"C:\\Users\\me\\AppData\\Local\\Temp\\keep\"\n" +
			"  " + buildinfo.Name + " config whitelist add windows.directx-shader-cache\n" +
			"  " + buildinfo.Name + " config whitelist add browser",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireConfig(); err != nil {
				return err
			}
			for _, a := range args {
				if config.IsPathEntry(a) {
					added, err := app.Config.AddPath(a)
					if err != nil {
						return withCode(ExitUsage, "%q: %v (paths must be absolute, like C:\\folder)", a, err)
					}
					report(app, added, a)
					continue
				}
				if !knownRulePrefix(a) {
					app.printf(" %s %s matches no current cleanup target; it will apply to future ones\n",
						ui.Warn.Render(ui.SymWarn), a)
				}
				report(app, app.Config.AddRule(a), a)
			}
			return app.Config.Save(app.Dirs.ConfigFile())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <path|target-id>...",
		Aliases: []string{"rm"},
		Short:   "Stop protecting paths or cleanup targets",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireConfig(); err != nil {
				return err
			}
			for _, a := range args {
				if app.Config.Remove(a) {
					app.printf(" %s removed %s\n", ui.OK.Render(ui.SymOK), a)
				} else {
					app.printf(" %s %s was not in the whitelist\n", ui.Muted.Render(ui.SymSkip), a)
				}
			}
			return app.Config.Save(app.Dirs.ConfigFile())
		},
	})
	return cmd
}

func report(app *App, added bool, entry string) {
	if app.JSON {
		return
	}
	if added {
		app.printf(" %s protected %s\n", ui.OK.Render(ui.SymOK), entry)
	} else {
		app.printf(" %s %s is already protected\n", ui.Muted.Render(ui.SymSkip), entry)
	}
}

func knownRulePrefix(id string) bool {
	for _, r := range cleanup.BuiltinRules() {
		if r.MatchesID(id) {
			return true
		}
	}
	return false
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
