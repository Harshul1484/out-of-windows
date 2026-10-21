// Package install knows how oow is installed: the folder the installer
// script uses, whether a package manager (winget, Scoop, Chocolatey) owns the
// executable instead, and the user PATH entry the installer adds.
//
// It decides; it does not delete. Removals go through the verified sinks in
// internal/filesystem, and PATH changes through an envpath.Store (the
// registry on a real system, a file in sandbox mode).
package install

import (
	"path/filepath"
	"strings"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Dir is the folder scripts/install.ps1 installs into:
// %LOCALAPPDATA%\Programs\<AppID>. localAppData must come from the Known
// Folder API, not from an environment variable.
func Dir(localAppData string) string {
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "Programs", buildinfo.AppID)
}

// ExeName is the executable's file name.
func ExeName() string { return buildinfo.Name + ".exe" }

// InDir reports whether exe is exactly <dir>\<ExeName()>.
func InDir(exe, dir string) bool {
	if exe == "" || dir == "" {
		return false
	}
	e, err1 := safety.Normalize(exe)
	d, err2 := safety.Normalize(filepath.Join(dir, ExeName()))
	return err1 == nil && err2 == nil && safety.Key(e) == safety.Key(d)
}

// Manager is a package manager that owns an installation.
type Manager string

// Package managers oow recognizes.
const (
	None       Manager = ""
	Winget     Manager = "winget"
	Scoop      Manager = "scoop"
	Chocolatey Manager = "chocolatey"
)

// Managed says which package manager owns the executable, if any.
type Managed struct {
	Manager Manager `json:"manager"`
	Package string  `json:"package"`
}

// Label is the manager's display name.
func (m Managed) Label() string {
	switch m.Manager {
	case Winget:
		return "winget"
	case Scoop:
		return "Scoop"
	case Chocolatey:
		return "Chocolatey"
	}
	return ""
}

// UpdateCommand is how the user updates through the manager.
func (m Managed) UpdateCommand() string {
	switch m.Manager {
	case Winget:
		return "winget upgrade --id " + m.pkg()
	case Scoop:
		return "scoop update " + m.pkg()
	case Chocolatey:
		return "choco upgrade " + m.pkg()
	}
	return ""
}

// RemoveCommand is how the user uninstalls through the manager.
func (m Managed) RemoveCommand() string {
	switch m.Manager {
	case Winget:
		return "winget uninstall --id " + m.pkg()
	case Scoop:
		return "scoop uninstall " + m.pkg()
	case Chocolatey:
		return "choco uninstall " + m.pkg()
	}
	return ""
}

func (m Managed) pkg() string {
	if m.Package != "" {
		return m.Package
	}
	return buildinfo.Name
}

// Env reads environment variables. Detection uses them only to recognize
// custom Scoop and Chocolatey roots, never to choose anything to delete.
type Env func(string) string

// DetectManager recognizes package-manager installs from the executable's
// path (pass both the path as launched and its resolved final path).
func DetectManager(env Env, paths ...string) Managed {
	if env == nil {
		env = func(string) string { return "" }
	}
	for _, p := range paths {
		n, err := safety.Normalize(p)
		if err != nil {
			continue
		}
		k := safety.Key(n) + `\`
		if len(k) != len(n)+1 {
			n = k[:len(k)-1] // keep byte offsets aligned with k
		}
		// winget portable packages: %LOCALAPPDATA%\Microsoft\WinGet\Packages\<Id>_<Source>\
		// (machine scope: %ProgramFiles%\WinGet\Packages), linked from WinGet\Links.
		if pkg, ok := segmentAfter(n, k, `\WINGET\PACKAGES\`); ok {
			id, _, _ := strings.Cut(pkg, "_")
			return Managed{Manager: Winget, Package: id}
		}
		if strings.Contains(k, `\WINGET\LINKS\`) {
			return Managed{Manager: Winget}
		}
		for _, root := range []string{env("SCOOP"), env("SCOOP_GLOBAL")} {
			if pkg, ok := under(n, root, "apps"); ok {
				return Managed{Manager: Scoop, Package: pkg}
			}
		}
		if pkg, ok := segmentAfter(n, k, `\SCOOP\APPS\`); ok {
			return Managed{Manager: Scoop, Package: pkg}
		}
		if strings.Contains(k, `\SCOOP\SHIMS\`) {
			return Managed{Manager: Scoop}
		}
		if pkg, ok := under(n, env("ChocolateyInstall"), "lib"); ok {
			return Managed{Manager: Chocolatey, Package: pkg}
		}
		if pkg, ok := segmentAfter(n, k, `\CHOCOLATEY\LIB\`); ok {
			return Managed{Manager: Chocolatey, Package: pkg}
		}
		if strings.Contains(k, `\CHOCOLATEY\BIN\`) {
			return Managed{Manager: Chocolatey}
		}
	}
	return Managed{}
}

// segmentAfter returns the path component that follows marker (matched
// against key, which is safety.Key of the path plus a trailing \).
func segmentAfter(n, key, marker string) (string, bool) {
	i := strings.Index(key, marker)
	if i < 0 {
		return "", false
	}
	rest := n[min(len(n), i+len(marker)):]
	seg, _, _ := strings.Cut(rest, `\`)
	return seg, true
}

// under reports whether n lies in <root>\<sub>\ and returns the next
// component (the package name).
func under(n, root, sub string) (string, bool) {
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	r, err := safety.Normalize(filepath.Join(root, sub))
	if err != nil || !safety.IsStrictlyWithin(n, r) {
		return "", false
	}
	if len(n) <= len(r)+1 {
		return "", true
	}
	seg, _, _ := strings.Cut(n[len(r)+1:], `\`)
	return seg, true
}
