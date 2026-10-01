// Package cli wires the commands together.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/config"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/logging"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// Exit codes. They are part of the documented scripting interface.
const (
	ExitOK             = 0
	ExitError          = 1
	ExitUsage          = 2
	ExitNotImplemented = 3
	ExitNeedsConfirm   = 4
	ExitCancelled      = 130
)

// exitError carries an exit code through cobra.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func withCode(code int, format string, args ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

var errCancelled = &exitError{code: ExitCancelled, err: errors.New("cancelled")}

// App is the state shared by all commands.
type App struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	// Global flags.
	JSON       bool
	NoColor    bool
	Debug      bool
	SandboxDir string

	// Derived during setup.
	ForceDryRun bool   // OOW_DRY_RUN=1: every destructive command only previews
	Sandbox     string // absolute sandbox root, "" when using the real system
	Dirs        config.Dirs
	Config      *config.Config
	ConfigErr   error
	Locations   safety.Locations
	Guard       *safety.Guard
	Elevated    bool
	closeLog    func()
}

// interactive reports whether we can prompt the user.
func (a *App) interactive() bool {
	if a.JSON {
		return false
	}
	in, ok1 := a.In.(*os.File)
	out, ok2 := a.Out.(*os.File)
	return ok1 && ok2 && ui.IsTerminal(in) && ui.IsTerminal(out)
}

// tty reports whether Err is a terminal (for spinners).
func (a *App) tty() bool {
	f, ok := a.Err.(*os.File)
	return ok && ui.IsTerminal(f) && !a.JSON
}

func (a *App) setup() error {
	a.ForceDryRun = os.Getenv("OOW_DRY_RUN") == "1"
	root := a.SandboxDir
	if root == "" {
		root = os.Getenv(sandbox.EnvVar)
	}
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return err
		}
		// Confine every deletion to the sandbox. An existing fence that
		// already contains the sandbox (tests) is kept.
		if f := filesystem.Fence(); f == "" || !safety.IsWithin(safety.MustNormalize(abs), f) {
			if err := filesystem.SetFence(abs); err != nil {
				return err
			}
		}
		a.Sandbox = abs
		a.Dirs = config.Dirs{Config: sandbox.DataDir(abs), Data: sandbox.DataDir(abs)}
		a.Locations = sandbox.Locations(abs)
		a.Elevated = true // simulated: the sandbox has no real permissions
	} else {
		d, err := config.DefaultDirs()
		if err != nil {
			return err
		}
		a.Dirs = d
		a.Locations = safety.DiscoverLocations()
		a.Elevated = system.IsElevated()
	}
	a.Locations.SelfDirs = append(a.Locations.SelfDirs, a.Dirs.Config, a.Dirs.Data)

	a.Config, a.ConfigErr = config.Load(a.Dirs.ConfigFile())
	var protected []string
	color := "auto"
	if a.ConfigErr == nil {
		protected = a.Config.Whitelist.Paths
		color = a.Config.UI.Color
	} else {
		a.Config = config.Default()
	}
	a.Guard = safety.NewGuard(a.Locations, protected)
	ui.Init(color, a.NoColor || a.JSON)
	a.closeLog = logging.Setup(a.Dirs.LogDir(), a.Debug, a.Err)
	return nil
}

// requireConfig fails commands that must honour the whitelist when the
// configuration file could not be read.
func (a *App) requireConfig() error {
	if a.ConfigErr != nil {
		return withCode(ExitError, "cannot read configuration: %v", a.ConfigErr)
	}
	return nil
}

func (a *App) close() {
	if a.closeLog != nil {
		a.closeLog()
	}
}

// printJSON writes v as indented JSON to stdout.
func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *App) printf(format string, args ...any) { fmt.Fprintf(a.Out, format, args...) }
func (a *App) println(args ...any)               { fmt.Fprintln(a.Out, args...) }

// header prints a command title with mode tags.
func (a *App) header(title string, dryRun bool) {
	line := " " + ui.Title.Render(title)
	if dryRun {
		line += "  " + ui.Warn.Render("DRY RUN")
	}
	if a.Sandbox != "" {
		line += "  " + ui.Warn.Render("SANDBOX")
	}
	a.println()
	a.println(line)
	if a.Sandbox != "" {
		a.println(" " + ui.Muted.Render("All paths are simulated under "+a.Sandbox))
	}
	a.println()
}

func cancelledErr(ctx context.Context) error {
	if ctx.Err() != nil {
		return errCancelled
	}
	return nil
}

// productName is shown on the home screen.
func productName() string {
	return "OUT OF WINDOWS  " + ui.Muted.Render(buildinfo.Name+" "+buildinfo.Version)
}
