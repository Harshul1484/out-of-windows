// Package apps discovers installed applications from every place Windows
// records them: the Uninstall registry keys (MSI and EXE installers, 64-bit,
// 32-bit and per-user), Microsoft Store / MSIX packages, Scoop and
// Chocolatey. Discovery is read-only.
package apps

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Source says how an app was installed and therefore how it is removed.
type Source string

const (
	SourceMSI   Source = "msi"   // Windows Installer product
	SourceEXE   Source = "exe"   // registered uninstaller program
	SourceAppX  Source = "appx"  // Microsoft Store / MSIX package
	SourceScoop Source = "scoop" // Scoop app
	SourceChoco Source = "choco" // Chocolatey package
)

// Scope is who the app is installed for.
type Scope string

const (
	ScopeMachine Scope = "machine"
	ScopeUser    Scope = "user"
)

// App is one installed application.
type App struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Version              string `json:"version,omitempty"`
	Publisher            string `json:"publisher,omitempty"`
	Source               Source `json:"source"`
	Scope                Scope  `json:"scope"`
	InstallLocation      string `json:"install_location,omitempty"`
	InstallDate          string `json:"install_date,omitempty"`
	SizeBytes            int64  `json:"size_bytes"`
	UninstallString      string `json:"uninstall_string,omitempty"`
	QuietUninstallString string `json:"quiet_uninstall_string,omitempty"`
	ProductCode          string `json:"product_code,omitempty"`
	RegistryKey          string `json:"registry_key,omitempty"`
	PackageName          string `json:"package_name,omitempty"` // AppX full name, Scoop app, Chocolatey id
	DisplayIcon          string `json:"display_icon,omitempty"`
	NoRemove             bool   `json:"no_remove,omitempty"`
	// Problems describe why the app cannot be uninstalled normally, e.g. a
	// registered uninstaller that no longer exists.
	Problems []string `json:"problems,omitempty"`
}

// ProblemUninstallerMissing is the first entry of Problems when the
// registered uninstaller file no longer exists.
const ProblemUninstallerMissing = "its uninstaller is missing"

// Removable reports whether oow can start an uninstall for the app.
func (a App) Removable() (bool, string) {
	switch {
	case a.NoRemove:
		return false, "the publisher marked it as not removable"
	case len(a.Problems) > 0:
		return false, a.Problems[0]
	case a.Source == SourceEXE && a.UninstallString == "" && a.QuietUninstallString == "":
		return false, "no uninstaller is registered"
	}
	return true, ""
}

// IsBroken reports whether a registered app has lost its program files: its
// uninstaller is gone and so is its main executable (or install folder).
// Such an entry is evidence of an app that was removed incompletely.
func (a App) IsBroken() bool {
	if len(a.Problems) == 0 || a.Problems[0] != ProblemUninstallerMissing {
		return false
	}
	if exe := strings.Trim(strings.Split(a.DisplayIcon, ",")[0], `" `); exe != "" {
		return !FileExists(exe)
	}
	if a.InstallLocation == "" {
		return true
	}
	_, err := os.Stat(a.InstallLocation)
	return err != nil
}

// Inventory is the result of discovery.
type Inventory struct {
	Apps []App `json:"apps"`
	// Warnings describe sources that could not be read completely.
	Warnings []string `json:"warnings,omitempty"`
	// PackageManagers maps detected package managers to their location.
	PackageManagers map[string]string `json:"package_managers"`
}

// Find returns the app with the given ID.
func (inv *Inventory) Find(id string) (App, bool) {
	for _, a := range inv.Apps {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}

// Provider discovers installed apps.
type Provider interface {
	List(ctx context.Context) (*Inventory, error)
}

// SortByName orders apps case-insensitively by name, then version.
func SortByName(list []App) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].Version < list[j].Version
	})
}

var (
	parenthesized = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)
	versionToken  = regexp.MustCompile(`^v?\d+([._-]\d+)+[a-z0-9]*$|^v\d+$`)
	archToken     = regexp.MustCompile(`^(x64|x86|amd64|arm64|win64|win32|64-bit|32-bit|64bit|32bit)$`)
	legalSuffix   = regexp.MustCompile(`^(inc|ltd|llc|corp|corporation|gmbh|co|company|limited|sa|ag|bv|plc|srl|sro|ab|oy|kg)$`)
)

// NormalizeName reduces an application, publisher or folder name to a
// comparison key: lower case, letters and digits only, with bracketed notes,
// version numbers and architecture tags removed. "7-Zip 23.01 (x64)" and the
// folder "7-Zip" both normalize to "7zip".
func NormalizeName(s string) string {
	s = parenthesized.ReplaceAllString(strings.ToLower(s), " ")
	var kept []string
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) {
		tok = strings.Trim(tok, ".-_")
		if tok == "" || versionToken.MatchString(tok) || archToken.MatchString(tok) {
			continue
		}
		kept = append(kept, tok)
	}
	var b strings.Builder
	for _, r := range strings.Join(kept, "") {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizePublisher is NormalizeName without legal-entity suffixes, so
// "Contoso Ltd." and the folder "Contoso" match.
func NormalizePublisher(s string) string {
	s = parenthesized.ReplaceAllString(strings.ToLower(s), " ")
	var kept []string
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) {
		if legalSuffix.MatchString(strings.Trim(tok, ".")) {
			continue
		}
		kept = append(kept, tok)
	}
	return NormalizeName(strings.Join(kept, " "))
}

// genericNames are folder or product names too generic to prove ownership.
var genericNames = map[string]bool{
	"app": true, "apps": true, "application": true, "applications": true, "data": true, "cache": true,
	"caches": true, "temp": true, "tmp": true, "update": true, "updater": true, "updates": true,
	"common": true, "commonfiles": true, "shared": true, "microsoft": true, "windows": true,
	"google": true, "intel": true, "amd": true, "nvidia": true, "program": true, "programs": true,
	"tools": true, "tool": true, "launcher": true, "client": true, "service": true, "services": true,
	"helper": true, "setup": true, "installer": true, "install": true, "uninstall": true,
	"config": true, "configuration": true, "settings": true, "logs": true, "log": true, "user": true,
	"users": true, "default": true, "local": true, "localcache": true, "system": true, "runtime": true,
	"runtimes": true, "framework": true, "sdk": true, "sdks": true, "driver": true, "drivers": true,
	"plugin": true, "plugins": true, "packages": true, "package": true, "bin": true, "lib": true,
	"crashpad": true, "crashreports": true, "crashdumps": true, "backup": true, "backups": true,
	"downloads": true, "documents": true, "desktop": true, "assets": true, "resources": true,
	"profile": true, "profiles": true, "extensions": true, "home": true, "software": true,
	"games": true, "game": true, "studio": true, "editor": true, "player": true, "browser": true,
	"agent": true, "server": true, "manager": true, "center": true, "desktopapp": true,
}

// IsDistinctive reports whether a normalized name is specific enough to be
// used as ownership evidence.
func IsDistinctive(norm string) bool {
	return len([]rune(norm)) >= 4 && !genericNames[norm]
}

// ExeName returns the executable file name in a DisplayIcon-style value such
// as `"C:\App\app.exe",0`, or "".
func ExeName(displayIcon string) string {
	s := strings.TrimSpace(displayIcon)
	if i := strings.LastIndex(s, ","); i > strings.LastIndex(s, `\`) {
		s = s[:i]
	}
	s = strings.Trim(s, `" `)
	base := filepath.Base(s)
	if !strings.EqualFold(filepath.Ext(base), ".exe") {
		return ""
	}
	return base
}

// Keys returns the distinctive normalized names identifying an app: its
// display name, its install folder name and its main executable name.
func (a App) Keys() []string {
	var keys []string
	add := func(s string) {
		n := NormalizeName(s)
		if !IsDistinctive(n) {
			return
		}
		for _, k := range keys {
			if k == n {
				return
			}
		}
		keys = append(keys, n)
	}
	add(a.Name)
	if a.InstallLocation != "" {
		add(filepath.Base(strings.TrimRight(a.InstallLocation, `\/`)))
	}
	if exe := ExeName(a.DisplayIcon); exe != "" {
		add(strings.TrimSuffix(exe, filepath.Ext(exe)))
	}
	return keys
}
