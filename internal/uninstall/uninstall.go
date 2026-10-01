// Package uninstall removes applications with their own uninstallers and
// verifies that they are gone.
//
// oow never deletes an installation folder as a substitute for uninstalling.
// It runs the mechanism the app was installed with (Windows Installer, the
// registered uninstaller, the AppX package manager, Scoop or Chocolatey),
// waits for it, and then checks that the app is no longer registered. Only a
// verified removal moves on to leftover cleanup.
package uninstall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
)

// Method is how an app is removed.
type Method string

const (
	MethodMSI   Method = "msi"
	MethodEXE   Method = "exe"
	MethodAppX  Method = "appx"
	MethodScoop Method = "scoop"
	MethodChoco Method = "choco"
)

// Plan is exactly what will be run for one app.
type Plan struct {
	App     apps.App `json:"app"`
	Method  Method   `json:"method"`
	Exe     string   `json:"exe,omitempty"`
	Args    []string `json:"args,omitempty"`
	Command string   `json:"command"` // human-readable
	Elevate bool     `json:"elevate"` // start through UAC (runas)
	Quiet   bool     `json:"quiet"`
	Notes   []string `json:"notes,omitempty"`
}

// NewPlan decides how to uninstall a. With quiet, silent options are used
// when the app provides them.
func NewPlan(a apps.App, quiet bool) (Plan, error) {
	if ok, why := a.Removable(); !ok {
		return Plan{}, fmt.Errorf("%s cannot be uninstalled: %s", a.Name, why)
	}
	p := Plan{App: a, Quiet: quiet}
	switch a.Source {
	case apps.SourceMSI:
		p.Method, p.Exe = MethodMSI, "msiexec.exe"
		p.Args = []string{"/x", a.ProductCode}
		if quiet {
			p.Args = append(p.Args, "/qn", "/norestart")
		}
	case apps.SourceEXE:
		cmdline := a.UninstallString
		if quiet {
			if a.QuietUninstallString != "" {
				cmdline = a.QuietUninstallString
			} else {
				p.Notes = append(p.Notes, "this uninstaller has no silent mode; its own window will open")
				p.Quiet = false
			}
		}
		if cmdline == "" {
			cmdline = a.QuietUninstallString
		}
		cmd, err := apps.ParseCommandLine(cmdline, apps.FileExists)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", a.Name, err)
		}
		if apps.IsMSIExec(cmd.Exe) {
			// "MsiExec.exe /I{code}" opens repair/modify; uninstall explicitly.
			code := apps.ProductCodeIn(cmdline)
			if code == "" {
				return Plan{}, fmt.Errorf("%s: Windows Installer command without a product code", a.Name)
			}
			p.Method, p.Exe, p.Args = MethodMSI, "msiexec.exe", []string{"/x", code}
			if quiet {
				p.Args = append(p.Args, "/qn", "/norestart")
			}
		} else {
			p.Method, p.Exe, p.Args = MethodEXE, cmd.Exe, cmd.Args
		}
	case apps.SourceAppX:
		p.Method = MethodAppX
		p.Command = "Remove-AppxPackage -Package " + a.PackageName
		return p, nil
	case apps.SourceScoop:
		p.Method, p.Exe = MethodScoop, "cmd.exe"
		p.Args = []string{"/c", "scoop", "uninstall", a.PackageName}
		if a.Scope == apps.ScopeMachine {
			p.Args = append(p.Args, "--global")
			p.Elevate = true
		}
	case apps.SourceChoco:
		p.Method, p.Exe, p.Elevate = MethodChoco, "choco.exe", true
		p.Args = []string{"uninstall", a.PackageName, "-y"}
	default:
		return Plan{}, fmt.Errorf("%s: unknown installation type %q", a.Name, a.Source)
	}
	p.Command = strings.TrimSpace(quoteIfNeeded(p.Exe) + " " + strings.Join(p.Args, " "))
	return p, nil
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// Runner starts an uninstall plan and waits for the started process.
type Runner interface {
	Run(ctx context.Context, p Plan) (exitCode int, err error)
}

// Checker reports whether an app is still installed.
type Checker interface {
	Installed(ctx context.Context, a apps.App) (bool, error)
}

// Outcome describes one uninstall attempt.
type Outcome struct {
	Plan     Plan          `json:"plan"`
	Started  bool          `json:"started"`
	ExitCode int           `json:"exit_code"`
	Removed  bool          `json:"removed"`
	Restart  bool          `json:"restart_required"`
	Waited   time.Duration `json:"-"`
	Message  string        `json:"message"`
}

// Options control Execute.
type Options struct {
	// WaitTimeout is how long to wait for the app to disappear after the
	// started process exits (uninstallers often hand over to a copy of
	// themselves in Temp and exit immediately).
	WaitTimeout time.Duration
	Poll        time.Duration
	// StopWaiting, when closed, ends the wait early (the user says the
	// uninstaller window is closed).
	StopWaiting <-chan struct{}
}

// Windows Installer exit codes that matter here.
const (
	msiUserCancelled  = 1602
	msiUnknownProduct = 1605
	msiRebootRequired = 3010
	msiRebootStarted  = 1641
)

// ErrNotRemoved is returned when the uninstaller ran but the app is still
// installed (cancelled or failed).
var ErrNotRemoved = errors.New("the app is still installed")

// Execute runs the plan, waits, and verifies the result.
func Execute(ctx context.Context, p Plan, r Runner, c Checker, opts Options) (Outcome, error) {
	if opts.WaitTimeout == 0 {
		opts.WaitTimeout = 3 * time.Minute
	}
	if opts.Poll == 0 {
		opts.Poll = 2 * time.Second
	}
	out := Outcome{Plan: p}
	start := time.Now()
	code, err := r.Run(ctx, p)
	if err != nil {
		out.Message = err.Error()
		return out, err
	}
	out.Started, out.ExitCode = true, code
	out.Restart = code == msiRebootRequired || code == msiRebootStarted

	if code == msiUserCancelled {
		out.Message = "the uninstall was cancelled"
		return out, ErrNotRemoved
	}

	deadline := time.Now().Add(opts.WaitTimeout)
	for {
		installed, err := c.Installed(ctx, p.App)
		if err == nil && !installed {
			out.Removed = true
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil || stopped(opts.StopWaiting) {
			break
		}
		select {
		case <-ctx.Done():
		case <-opts.StopWaiting:
		case <-time.After(opts.Poll):
		}
	}
	out.Waited = time.Since(start)
	switch {
	case out.Removed:
		out.Message = "uninstalled"
		if out.Restart {
			out.Message = "uninstalled; Windows needs a restart to finish"
		}
		return out, nil
	case code == msiUnknownProduct:
		out.Message = "Windows Installer no longer knows this product, but its entry is still listed"
	case code != 0:
		out.Message = fmt.Sprintf("the uninstaller exited with code %d and the app is still installed", code)
	default:
		out.Message = "the app is still installed (the uninstaller may have been cancelled or is still running)"
	}
	return out, ErrNotRemoved
}

func stopped(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
