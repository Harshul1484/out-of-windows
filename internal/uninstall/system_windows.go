package uninstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/elevation"
)

// SystemRunner runs uninstallers on the real system. Programs are started
// through the Shell (ShellExecuteEx), so uninstallers that require
// administrator rights show the normal UAC prompt.
type SystemRunner struct{}

// Run starts the plan and waits for the process it started.
func (SystemRunner) Run(ctx context.Context, p Plan) (int, error) {
	if p.Method == MethodAppX {
		if err := apps.RemoveAppX(ctx, p.App.PackageName); err != nil {
			return 1, err
		}
		return 0, nil
	}
	verb := ""
	if p.Elevate {
		verb = "runas"
	}
	code, err := elevation.RunWait(p.Exe, p.Args, verb)
	return int(code), err
}

// SystemChecker checks the real system for an app.
type SystemChecker struct{}

// Installed reports whether a is still registered.
func (SystemChecker) Installed(ctx context.Context, a apps.App) (bool, error) {
	switch a.Source {
	case apps.SourceMSI, apps.SourceEXE:
		return registryKeyExists(a.RegistryKey), nil
	case apps.SourceAppX:
		inv, err := apps.System{}.List(ctx)
		if err != nil {
			return true, err
		}
		_, ok := inv.Find(a.ID)
		return ok, nil
	case apps.SourceScoop:
		_, err := os.Stat(filepath.Join(a.InstallLocation, "current"))
		return err == nil, nil
	case apps.SourceChoco:
		root := os.Getenv("ChocolateyInstall")
		if root == "" {
			root = filepath.Join(os.Getenv("ProgramData"), "chocolatey")
		}
		_, err := os.Stat(filepath.Join(root, "lib", a.PackageName))
		return err == nil, nil
	}
	return true, nil
}

func registryKeyExists(full string) bool {
	var root registry.Key
	var access uint32
	switch {
	case strings.HasPrefix(full, `HKLM\SOFTWARE\WOW6432Node\`):
		root, access = registry.LOCAL_MACHINE, registry.WOW64_32KEY
		full = `SOFTWARE\` + strings.TrimPrefix(full, `HKLM\SOFTWARE\WOW6432Node\`)
	case strings.HasPrefix(full, `HKLM\`):
		root, access = registry.LOCAL_MACHINE, registry.WOW64_64KEY
		full = strings.TrimPrefix(full, `HKLM\`)
	case strings.HasPrefix(full, `HKCU\`):
		root = registry.CURRENT_USER
		full = strings.TrimPrefix(full, `HKCU\`)
	default:
		return true // unknown: assume still installed (fail closed)
	}
	k, err := registry.OpenKey(root, full, registry.QUERY_VALUE|access)
	if errors.Is(err, registry.ErrNotExist) {
		return false
	}
	if err == nil {
		k.Close()
	}
	return true // present, or unreadable: never report "removed" on doubt
}
