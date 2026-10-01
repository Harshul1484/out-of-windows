package apps

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

type uninstallView struct {
	root   registry.Key
	access uint32
	label  string // stable ID prefix
	name   string // human-readable key root
	scope  Scope
}

var uninstallViews = []uninstallView{
	{registry.LOCAL_MACHINE, registry.WOW64_64KEY, "hklm64", `HKLM\` + uninstallPath, ScopeMachine},
	{registry.LOCAL_MACHINE, registry.WOW64_32KEY, "hklm32", `HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, ScopeMachine},
	{registry.CURRENT_USER, 0, "hkcu", `HKCU\` + uninstallPath, ScopeUser},
}

// readRegistry lists apps registered under the Uninstall keys, filtered like
// Settings > Apps: entries without a name, system components, and updates
// or hotfixes that belong to another product are left out.
func readRegistry() ([]App, []string) {
	var out []App
	var warnings []string
	seen := map[string]bool{}
	for _, v := range uninstallViews {
		k, err := registry.OpenKey(v.root, uninstallPath, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE|v.access)
		if err != nil {
			continue // view not present (e.g. 32-bit view on ARM) or no per-user entries
		}
		names, err := k.ReadSubKeyNames(-1)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not list %s: %v", v.name, err))
		}
		for _, name := range names {
			a, ok := readEntry(k, v, name)
			if !ok {
				continue
			}
			// The same product can appear in both machine views; keep one.
			dedup := strings.ToLower(a.Name + "|" + a.Version + "|" + string(a.Scope) + "|" + a.ProductCode)
			if seen[dedup] {
				continue
			}
			seen[dedup] = true
			out = append(out, a)
		}
		k.Close()
	}
	return out, warnings
}

func readEntry(parent registry.Key, v uninstallView, name string) (App, bool) {
	k, err := registry.OpenKey(parent, name, registry.QUERY_VALUE)
	if err != nil {
		return App{}, false
	}
	defer k.Close()
	str := func(value string) string {
		s, typ, err := k.GetStringValue(value)
		if err != nil {
			return ""
		}
		if typ == registry.EXPAND_SZ {
			if x, err := registry.ExpandString(s); err == nil {
				s = x
			}
		}
		return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "\x00"))
	}
	dword := func(value string) uint64 {
		n, _, err := k.GetIntegerValue(value)
		if err != nil {
			return 0
		}
		return n
	}

	a := App{
		Name:                 str("DisplayName"),
		Version:              str("DisplayVersion"),
		Publisher:            str("Publisher"),
		Scope:                v.scope,
		InstallLocation:      strings.Trim(str("InstallLocation"), `"`),
		InstallDate:          str("InstallDate"),
		UninstallString:      str("UninstallString"),
		QuietUninstallString: str("QuietUninstallString"),
		DisplayIcon:          str("DisplayIcon"),
		RegistryKey:          v.name + `\` + name,
		ID:                   "reg:" + v.label + ":" + name,
		NoRemove:             dword("NoRemove") == 1,
	}
	if a.Name == "" || dword("SystemComponent") == 1 || str("ParentKeyName") != "" {
		return App{}, false
	}
	switch strings.ToLower(str("ReleaseType")) {
	case "update", "hotfix", "security update", "servicepack", "service pack", "update rollup":
		return App{}, false
	}
	if kb := dword("EstimatedSize"); kb > 0 {
		a.SizeBytes = int64(kb) * 1024
	}

	a.Source = SourceEXE
	if code := ProductCodeIn(name); dword("WindowsInstaller") == 1 && code != "" && strings.EqualFold(code, name) {
		a.Source, a.ProductCode = SourceMSI, code
	} else if cmd, err := ParseCommandLine(a.UninstallString, FileExists); err == nil && IsMSIExec(cmd.Exe) {
		if code := ProductCodeIn(a.UninstallString); code != "" {
			a.Source, a.ProductCode = SourceMSI, code
		}
	}
	a.Problems = registryProblems(a)
	return a, true
}

// registryProblems detects entries whose uninstaller cannot run.
func registryProblems(a App) []string {
	if a.Source == SourceMSI || a.NoRemove {
		return nil
	}
	cmdline := a.UninstallString
	if cmdline == "" {
		cmdline = a.QuietUninstallString
	}
	if cmdline == "" {
		return []string{"no uninstaller is registered"}
	}
	cmd, err := ParseCommandLine(cmdline, FileExists)
	if err != nil {
		return []string{"the registered uninstall command is malformed"}
	}
	if strings.ContainsAny(cmd.Exe, `\/`) && !FileExists(cmd.Exe) {
		return []string{ProblemUninstallerMissing, "missing file: " + cmd.Exe}
	}
	return nil
}
