package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/leftovers"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/uninstall"
)

// simApp is one simulated installed application and the behaviour of its
// simulated uninstaller.
type simApp struct {
	App apps.App `json:"app"`
	// Remove lists absolute paths the simulated uninstaller deletes.
	Remove []string `json:"remove"`
	// Fail makes the uninstaller exit with an error and leave the app.
	Fail bool `json:"fail"`
}

// Apps simulates installed applications, their uninstallers and the
// registry checks oow makes, from <root>\registry\apps.json.
type Apps struct{ Root string }

func (s Apps) file() string { return filepath.Join(s.Root, "registry", "apps.json") }

func (s Apps) load() ([]simApp, error) {
	data, err := os.ReadFile(s.file())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []simApp
	return list, json.Unmarshal(data, &list)
}

func (s Apps) save(list []simApp) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.file()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.file(), data, 0o644)
}

// List returns the simulated inventory.
func (s Apps) List(ctx context.Context) (*apps.Inventory, error) {
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	inv := &apps.Inventory{PackageManagers: map[string]string{}}
	for _, e := range list {
		a := e.App
		if a.Source == apps.SourceEXE && a.UninstallString != "" {
			if cmd, err := apps.ParseCommandLine(a.UninstallString, apps.FileExists); err == nil && !apps.FileExists(cmd.Exe) {
				a.Problems = []string{apps.ProblemUninstallerMissing, "missing file: " + cmd.Exe}
			}
		}
		inv.Apps = append(inv.Apps, a)
	}
	apps.SortByName(inv.Apps)
	return inv, nil
}

// Run simulates the app's uninstaller.
func (s Apps) Run(ctx context.Context, p uninstall.Plan) (int, error) {
	list, err := s.load()
	if err != nil {
		return 1, err
	}
	root, err := safety.Normalize(s.Root)
	if err != nil {
		return 1, err
	}
	for i, e := range list {
		if e.App.ID != p.App.ID {
			continue
		}
		for _, r := range e.Remove {
			n, err := safety.Normalize(r)
			if err != nil || !safety.IsStrictlyWithin(n, root) {
				return 1, fmt.Errorf("simulated uninstaller path %s is outside the sandbox", r)
			}
			if err := os.RemoveAll(n); err != nil {
				return 1, err
			}
		}
		if e.Fail {
			return 1603, nil // ERROR_INSTALL_FAILURE
		}
		list = append(list[:i], list[i+1:]...)
		return 0, s.save(list)
	}
	return 1605, nil // ERROR_UNKNOWN_PRODUCT
}

// Installed reports whether the simulated app is still registered.
func (s Apps) Installed(ctx context.Context, a apps.App) (bool, error) {
	list, err := s.load()
	if err != nil {
		return true, err
	}
	for _, e := range list {
		if e.App.ID == a.ID {
			return true, nil
		}
	}
	return false, nil
}

// Traces returns simulated usage traces from <root>\registry\traces.json.
func Traces(root string) []leftovers.Trace {
	data, err := os.ReadFile(filepath.Join(root, "registry", "traces.json"))
	if err != nil {
		return nil
	}
	var out []leftovers.Trace
	_ = json.Unmarshal(data, &out)
	return out
}

// seedApps writes the simulated application inventory, files and traces.
func seedApps(root string, l safety.Locations) error {
	const day = 24 * time.Hour
	now := time.Now()
	pf, pf86, pd := l.ProgramFiles, l.ProgramFilesX86, l.ProgramData
	roam, local := l.RoamingAppData, l.LocalAppData
	progs := filepath.Join(local, "Programs")
	j := filepath.Join

	files := []struct {
		path string
		size int
		age  time.Duration
	}{
		// Contoso Studio: machine-wide; its uninstaller leaves a cache folder
		// and data under the publisher folders and in Local AppData.
		{j(pf, "Contoso", "Studio", "studio.exe"), 900000, 120 * day},
		{j(pf, "Contoso", "Studio", "core.dll"), 2000000, 120 * day},
		{j(pf, "Contoso", "Studio", "uninstall.exe"), 80000, 120 * day},
		{j(pf, "Contoso", "Studio", "cache", "shader.bin"), 5000000, 30 * day},
		{j(roam, "Contoso", "Studio", "settings.json"), 3000, 10 * day},
		{j(local, "Contoso Studio", "logs", "studio.log"), 400000, 10 * day},
		{j(pd, "Contoso", "Studio", "license.lic"), 1000, 100 * day},
		// Contoso Agent: another Contoso app that stays installed.
		{j(pf, "Contoso", "Agent", "agent.exe"), 300000, 60 * day},

		// Fabrikam Player: per-user; uninstalls cleanly, leaves its library.
		{j(progs, "Fabrikam Player", "player.exe"), 700000, 50 * day},
		{j(progs, "Fabrikam Player", "uninstall.exe"), 60000, 50 * day},
		{j(roam, "Fabrikam Player", "library.db"), 9000000, 5 * day},

		// Northwind Sync: an MSI whose uninstall fails.
		{j(pf, "Northwind", "Sync", "sync.exe"), 400000, 40 * day},
		{j(roam, "Northwind Sync", "state.json"), 1000, 2 * day},

		// Wingtip Toys: stays installed; its data must never be touched.
		{j(pf86, "Wingtip Toys", "wingtip.exe"), 500000, 300 * day},
		{j(roam, "Wingtip Toys", "save.dat"), 70000, 3 * day},

		// Litware Tool: broken entry (uninstaller and program are gone).
		{j(pf, "Litware Tool", "readme.txt"), 2000, 400 * day},
		{j(pf, "Litware Tool", "data", "db.bin"), 3000000, 400 * day},

		// Old Editor: removed long ago; Windows still remembers running it.
		{j(pf, "OldEditor", "plugins", "spell.dll"), 1500000, 200 * day},
		{j(roam, "Old Editor", "prefs.ini"), 900, 200 * day},
		// Recent Tool: also gone, but its folder changed yesterday.
		{j(progs, "Recent Tool", "cache.bin"), 10000, 1 * day},
	}
	for _, f := range files {
		if err := WriteFile(f.path, f.size, now.Add(-f.age)); err != nil {
			return err
		}
	}

	entry := func(id, name, version, publisher string, src apps.Source, scope apps.Scope, loc, exe string, size int64) apps.App {
		key := `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + id
		if scope == apps.ScopeUser {
			key = `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + id
		}
		a := apps.App{
			ID: "reg:sim:" + id, Name: name, Version: version, Publisher: publisher, Source: src, Scope: scope,
			InstallLocation: loc, SizeBytes: size, RegistryKey: key,
		}
		if exe != "" {
			a.DisplayIcon = `"` + j(loc, exe) + `",0`
		}
		return a
	}
	studio := entry("ContosoStudio", "Contoso Studio", "4.2.0", "Contoso Ltd.", apps.SourceEXE, apps.ScopeMachine,
		j(pf, "Contoso", "Studio"), "studio.exe", 7_900_000)
	studio.UninstallString = `"` + j(pf, "Contoso", "Studio", "uninstall.exe") + `" /S`
	agent := entry("ContosoAgent", "Contoso Agent", "1.0", "Contoso Ltd.", apps.SourceEXE, apps.ScopeMachine,
		j(pf, "Contoso", "Agent"), "agent.exe", 300_000)
	agent.UninstallString = `"` + j(pf, "Contoso", "Agent", "agent.exe") + `" --uninstall`
	player := entry("FabrikamPlayer", "Fabrikam Player", "2.0.1", "Fabrikam, Inc.", apps.SourceEXE, apps.ScopeUser,
		j(progs, "Fabrikam Player"), "player.exe", 760_000)
	player.UninstallString = `"` + j(progs, "Fabrikam Player", "uninstall.exe") + `"`
	player.QuietUninstallString = player.UninstallString + " /quiet"
	sync := entry("{6F1D5C3A-2B4E-4C8D-9A7F-1E2D3C4B5A69}", "Northwind Sync", "3.1", "Northwind Traders", apps.SourceMSI,
		apps.ScopeMachine, j(pf, "Northwind", "Sync"), "sync.exe", 400_000)
	sync.ProductCode = "{6F1D5C3A-2B4E-4C8D-9A7F-1E2D3C4B5A69}"
	wingtip := entry("WingtipToys", "Wingtip Toys", "9.0", "Wingtip Toys", apps.SourceEXE, apps.ScopeMachine,
		j(pf86, "Wingtip Toys"), "wingtip.exe", 500_000)
	wingtip.UninstallString = `"` + j(pf86, "Wingtip Toys", "wingtip.exe") + `" /uninstall`
	litware := entry("LitwareTool", "Litware Tool", "1.4", "Litware", apps.SourceEXE, apps.ScopeMachine,
		j(pf, "Litware Tool"), "litware.exe", 0)
	litware.UninstallString = `"` + j(pf, "Litware Tool", "unins000.exe") + `"`

	list := []simApp{
		{App: studio, Remove: []string{j(pf, "Contoso", "Studio", "studio.exe"), j(pf, "Contoso", "Studio", "core.dll"),
			j(pf, "Contoso", "Studio", "uninstall.exe")}},
		{App: agent},
		{App: player, Remove: []string{j(progs, "Fabrikam Player")}},
		{App: sync, Fail: true},
		{App: wingtip},
		{App: litware},
	}
	if err := (Apps{Root: root}).save(list); err != nil {
		return err
	}

	traces := []leftovers.Trace{
		{Exe: j(pf, "OldEditor", "oldeditor.exe"), Name: "Old Editor", Company: "Proseware"},
		{Exe: j(progs, "Recent Tool", "recent.exe"), Name: "Recent Tool"},
		{Exe: j(local, "Temp", "setup-123", "setup.exe"), Name: "Setup"},
		{Exe: j(pf, "Contoso", "Agent", "agent.exe"), Name: "Contoso Agent", Company: "Contoso Ltd."},
		{Exe: j(l.UserContent[2], "tool.exe"), Name: "Downloaded Tool"},
	}
	data, err := json.MarshalIndent(traces, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j(root, "registry", "traces.json"), data, 0o644)
}
