package cli

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/install"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/selfupdate"
)

// updateAPIBase is the GitHub API used by `update`. Tests point it at an
// httptest server; production never changes it (no environment override, so
// nothing outside the binary can redirect where updates come from).
var updateAPIBase = selfupdate.DefaultAPIBase

// selfExeOverride replaces the simulated executable in sandbox mode (tests
// use it to place the executable where a package manager would).
var selfExeOverride string

// selfInfo describes the executable that update and remove act on.
type selfInfo struct {
	Exe     string // OS-resolved path
	Raw     string // as launched
	Running bool   // it is this process's own image
}

// self returns this executable on a real system, or the simulated installed
// one (never the test binary) in sandbox mode.
func (a *App) self() (selfInfo, error) {
	if a.Sandbox != "" {
		exe := sandbox.SelfExe(a.Sandbox)
		if selfExeOverride != "" {
			exe = selfExeOverride
		}
		return selfInfo{Exe: exe, Raw: exe}, nil
	}
	raw, err := os.Executable()
	if err != nil {
		return selfInfo{}, err
	}
	resolved := raw
	if f, err := filesystem.FinalPath(raw); err == nil {
		resolved = f
	}
	return selfInfo{Exe: resolved, Raw: raw, Running: true}, nil
}

// managedBy reports a package manager that owns the executable.
func managedBy(s selfInfo) install.Managed {
	return install.DetectManager(os.Getenv, s.Raw, s.Exe)
}

// installDir is the installer's folder, from the Known Folder for
// %LOCALAPPDATA% (or the sandbox).
func (a *App) installDir() string { return install.Dir(a.Locations.LocalAppData) }

// exeRemover removes the installed executable. The running program cannot
// delete its own file, so on a real system the user gets the command to run
// after exit instead.
func (a *App) exeRemover(s selfInfo) install.ExeRemover {
	if s.Running {
		return install.RunningExe{}
	}
	return install.VerifiedExe{Check: func(final string) error {
		if d := a.Guard.Check(safety.Request{Path: final, Purpose: safety.PurposeUserSelected}); !d.Allowed {
			return errors.New(d.Reason)
		}
		return nil
	}}
}

func (a *App) updateSource() selfupdate.Source {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return selfupdate.Source{
		APIBase:   updateAPIBase,
		Repo:      buildinfo.Repo,
		Token:     token,
		UserAgent: buildinfo.Name + "/" + buildinfo.Version,
	}
}

// cleanupOldExe removes the executable a previous update moved aside. It runs
// at every start; a file still in use stays for a later start.
func (a *App) cleanupOldExe() {
	s, err := a.self()
	if err != nil || s.Exe == "" {
		return
	}
	if _, err := os.Lstat(selfupdate.OldPath(s.Exe)); err != nil {
		return
	}
	if err := selfupdate.CleanupOld(s.Exe); err != nil {
		slog.Debug("previous version not removed yet", "path", selfupdate.OldPath(s.Exe), "err", err)
		return
	}
	slog.Info("removed the previous version left by update", "path", selfupdate.OldPath(s.Exe))
}

// toolDirs are oow's own config and data folders as remove sees them: the
// folder in use, and whether it is the standard location (Known Folder +
// AppID, or the sandbox's data folder). Folders chosen through OOW_CONFIG_DIR
// or OOW_DATA_DIR are never removed.
type toolDir struct {
	Kind     string // "config-dir" or "data-dir"
	Path     string
	Standard bool
	Override string // the environment variable that chose it, if any
}

func (a *App) toolDirs() []toolDir {
	cfgWant := filepath.Join(a.Locations.RoamingAppData, buildinfo.AppID)
	dataWant := filepath.Join(a.Locations.LocalAppData, buildinfo.AppID)
	if a.Sandbox != "" {
		cfgWant, dataWant = sandbox.DataDir(a.Sandbox), sandbox.DataDir(a.Sandbox)
	}
	same := func(x, y string) bool {
		nx, e1 := safety.Normalize(x)
		ny, e2 := safety.Normalize(y)
		return e1 == nil && e2 == nil && safety.Key(nx) == safety.Key(ny)
	}
	dirs := []toolDir{
		{Kind: "config-dir", Path: a.Dirs.Config, Standard: same(a.Dirs.Config, cfgWant)},
		{Kind: "data-dir", Path: a.Dirs.Data, Standard: same(a.Dirs.Data, dataWant)},
	}
	if a.Sandbox == "" {
		if os.Getenv("OOW_CONFIG_DIR") != "" {
			dirs[0].Override = "OOW_CONFIG_DIR"
		}
		if os.Getenv("OOW_DATA_DIR") != "" {
			dirs[1].Override = "OOW_DATA_DIR"
		}
	}
	if same(dirs[0].Path, dirs[1].Path) {
		// One folder holds both (sandbox mode): list it once.
		return dirs[1:]
	}
	return dirs
}
