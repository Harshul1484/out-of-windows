// Package sandbox builds a simulated Windows directory layout inside an
// ordinary folder so every destructive code path can be exercised without
// touching the real system.
//
// In sandbox mode (OOW_SANDBOX=<dir>) the CLI maps every Windows location into
// <dir>, stores its configuration and history there, and sets the deletion
// fence to <dir> so nothing outside it can be deleted, whatever the policy
// code decides.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// EnvVar enables sandbox mode in the CLI.
const EnvVar = "OOW_SANDBOX"

// UserName is the simulated user's name.
const UserName = "sandbox"

// Locations maps every Windows location into root.
func Locations(root string) safety.Locations {
	c := filepath.Join(root, "C")
	win := filepath.Join(c, "Windows")
	users := filepath.Join(c, "Users")
	prof := filepath.Join(users, UserName)
	local := filepath.Join(prof, "AppData", "Local")
	roaming := filepath.Join(prof, "AppData", "Roaming")
	return safety.Locations{
		Windows:          win,
		WindowsTemp:      filepath.Join(win, "Temp"),
		ProgramFiles:     filepath.Join(c, "Program Files"),
		ProgramFilesX86:  filepath.Join(c, "Program Files (x86)"),
		ProgramData:      filepath.Join(c, "ProgramData"),
		CommonFilesExtra: []string{filepath.Join(c, "Program Files", "Common Files")},
		UsersRoot:        users,
		PublicProfile:    filepath.Join(users, "Public"),
		UserProfile:      prof,
		RoamingAppData:   roaming,
		LocalAppData:     local,
		LocalLow:         filepath.Join(prof, "AppData", "LocalLow"),
		Temp:             filepath.Join(local, "Temp"),
		UserContent: []string{
			filepath.Join(prof, "Desktop"),
			filepath.Join(prof, "Documents"),
			filepath.Join(prof, "Downloads"),
			filepath.Join(prof, "Pictures"),
			filepath.Join(prof, "Music"),
			filepath.Join(prof, "Videos"),
			filepath.Join(prof, "OneDrive"),
		},
		CriticalExtra: []string{
			filepath.Join(roaming, "Microsoft", "Windows", "Start Menu"),
			StartMenuPrograms(root),
			filepath.Join(StartMenuPrograms(root), "Startup"),
			filepath.Join(local, "Microsoft"),
			filepath.Join(local, "Packages"),
			filepath.Join(local, "Programs"),
			filepath.Join(roaming, "Microsoft"),
		},
		ShortcutRoots: []string{StartMenuPrograms(root), filepath.Join(prof, "Desktop")},
	}
}

// StartMenuPrograms is the simulated user's Start Menu Programs folder.
func StartMenuPrograms(root string) string {
	return filepath.Join(root, "C", "Users", UserName, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs")
}

// DataDir is where sandbox mode keeps configuration, history and logs.
func DataDir(root string) string { return filepath.Join(root, "oow-data") }

// File describes one seeded file.
type File struct {
	Rel  string        // path relative to the sandbox root
	Size int           // bytes
	Age  time.Duration // creation and modification time = now - Age
}

// Seed creates the simulated layout plus a realistic mix of junk and
// precious files under root. It is idempotent: existing files are rewritten.
func Seed(root string) error {
	now := time.Now()
	const day = 24 * time.Hour
	l := Locations(root)
	rel := func(p string) string {
		r, err := filepath.Rel(root, p)
		if err != nil {
			panic(err)
		}
		return r
	}
	tmp, local, win := rel(l.Temp), rel(l.LocalAppData), rel(l.Windows)
	docs := rel(l.UserContent[1])

	files := []File{
		// Precious data that no rule may ever touch.
		{filepath.Join(win, "System32", "kernel32.dll"), 4096, 400 * day},
		{filepath.Join(win, "System32", "drivers", "etc", "hosts"), 824, 400 * day},
		{filepath.Join(rel(l.ProgramFiles), "Contoso", "contoso.exe"), 65536, 90 * day},
		{filepath.Join(rel(l.ProgramData), "Contoso", "license.dat"), 512, 90 * day},
		{filepath.Join(docs, "thesis.docx"), 120000, 30 * day},
		{filepath.Join(docs, "old-notes.txt"), 2048, 900 * day},
		{filepath.Join(local, "Contoso", "settings.json"), 300, 60 * day},

		// User temp: old junk, recent files, nested folders.
		{filepath.Join(tmp, "setup_4f2a.log"), 18000, 12 * day},
		{filepath.Join(tmp, "chrome_installer.exe"), 1500000, 40 * day},
		{filepath.Join(tmp, "7zS1A2B.tmp", "payload.bin"), 3000000, 20 * day},
		{filepath.Join(tmp, "7zS1A2B.tmp", "nested", "data.cab"), 800000, 20 * day},
		{filepath.Join(tmp, "fresh-download.part"), 250000, 2 * time.Hour},
		{filepath.Join(tmp, "vscode-ipc.sock.lock"), 10, 10 * time.Minute},

		// Old but sensitive files in Temp: never removed automatically.
		{filepath.Join(tmp, "cert-export", "signing.pfx"), 4000, 30 * day},
		{filepath.Join(tmp, "wsl-import", "ext4.vhdx"), 5000000, 30 * day},

		// Credentials in the profile: never touched, whatever happens.
		{filepath.Join(rel(l.UserProfile), ".ssh", "id_ed25519"), 400, 200 * day},
		{filepath.Join(rel(l.RoamingAppData), "Microsoft", "Protect", "S-1-5-21-1", "masterkey"), 740, 200 * day},

		// Windows temp (requires administrator on a real system).
		{filepath.Join(rel(l.WindowsTemp), "MpCmdRun.log"), 40000, 15 * day},
		{filepath.Join(rel(l.WindowsTemp), "DismHost", "dism.log"), 90000, 15 * day},

		// DirectX shader cache.
		{filepath.Join(local, "D3DSCache", "6a1b2c", "shader.idx"), 4096, 3 * day},
		{filepath.Join(local, "D3DSCache", "6a1b2c", "shader.val"), 900000, 3 * day},

		// Windows Error Reporting.
		{filepath.Join(local, "Microsoft", "Windows", "WER", "ReportArchive",
			"AppCrash_contoso.exe_1", "Report.wer"), 12000, 45 * day},
		{filepath.Join(local, "Microsoft", "Windows", "WER", "ReportQueue",
			"AppHang_contoso.exe_2", "memory.hdmp"), 2500000, 8 * day},
	}
	files = append(files, phase2Files(l, rel)...)

	for _, d := range []string{
		l.UserContent[0], l.UserContent[2], l.UserContent[3], l.UserContent[4],
		l.UserContent[5], l.UserContent[6], l.RoamingAppData, l.LocalLow,
		l.PublicProfile, l.ProgramFilesX86, l.CommonFilesExtra[0], DataDir(root),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, f := range files {
		if err := WriteFile(filepath.Join(root, f.Rel), f.Size, now.Add(-f.Age)); err != nil {
			return err
		}
	}

	// Old folders get old timestamps too, so emptied ones can be removed.
	for _, d := range []string{
		filepath.Join(tmp, "7zS1A2B.tmp"),
		filepath.Join(tmp, "7zS1A2B.tmp", "nested"),
		filepath.Join(rel(l.WindowsTemp), "DismHost"),
		filepath.Join(local, "D3DSCache", "6a1b2c"),
	} {
		if err := filesystem.SetTimes(filepath.Join(root, d), now.Add(-20*day), now.Add(-20*day)); err != nil {
			return err
		}
	}

	// Installed apps, their simulated uninstallers and usage traces.
	if err := seedApps(root, l); err != nil {
		return err
	}
	// Startup entries, PATH values and system facts (startup, doctor, repair).
	if err := seedSystem(root, l); err != nil {
		return err
	}
	// Developer projects (purge) and installer packages (installer).
	if err := seedPhase7(root, l); err != nil {
		return err
	}

	// Junctions that cleanup must never follow: one inside Temp, and a fake
	// browser "profile" that points at Documents.
	for link, target := range map[string]string{
		filepath.Join(l.Temp, "link-to-documents"):                                  l.UserContent[1],
		filepath.Join(l.LocalAppData, "Google", "Chrome", "User Data", "Profile 9"): l.UserContent[1],
	} {
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			if err := MakeJunction(link, target); err != nil {
				return fmt.Errorf("create junction: %w", err)
			}
		}
	}
	return nil
}

// RecycleBinDir is the simulated Recycle Bin of the sandbox's C: drive.
func RecycleBinDir(root string) string { return filepath.Join(root, "C", "$Recycle.Bin") }

// phase2Files seeds browser, application and developer caches together with
// the precious data that lives right next to them.
func phase2Files(l safety.Locations, rel func(string) string) []File {
	const day = 24 * time.Hour
	local, roaming, prof := rel(l.LocalAppData), rel(l.RoamingAppData), rel(l.UserProfile)
	chrome := filepath.Join(local, "Google", "Chrome", "User Data")
	ff := filepath.Join("Mozilla", "Firefox", "Profiles", "k3x9.default-release")
	j := filepath.Join
	return []File{
		// Chrome: two real profiles (marker "Preferences"), browser-wide shader
		// cache, and profile data that must survive.
		{j(chrome, "Default", "Preferences"), 2000, 30 * day},
		{j(chrome, "Default", "Cache", "Cache_Data", "data_1"), 270000, 3 * day},
		{j(chrome, "Default", "Cache", "Cache_Data", "f_00a1b2"), 900000, 3 * day},
		{j(chrome, "Default", "Code Cache", "js", "4f1e2d_0"), 64000, 3 * day},
		{j(chrome, "Default", "GPUCache", "data_0"), 8192, 3 * day},
		{j(chrome, "Profile 1", "Preferences"), 2000, 30 * day},
		{j(chrome, "Profile 1", "Cache", "Cache_Data", "f_000001"), 400000, 9 * day},
		{j(chrome, "ShaderCache", "data_0"), 120000, 9 * day},
		{j(chrome, "GrShaderCache", "data_1"), 60000, 9 * day},
		{j(chrome, "Local State"), 5000, 30 * day},
		{j(chrome, "Default", "Cookies"), 40000, 1 * day},
		{j(chrome, "Default", "Login Data"), 50000, 1 * day},
		{j(chrome, "Default", "History"), 300000, 1 * day},
		{j(chrome, "Default", "Bookmarks"), 9000, 60 * day},
		{j(chrome, "Default", "Local Storage", "leveldb", "000003.log"), 7000, 2 * day},
		{j(chrome, "Default", "Service Worker", "CacheStorage", "a1", "index"), 3000, 2 * day},
		{j(chrome, "Default", "Extensions", "abcdef", "1.0", "manifest.json"), 900, 90 * day},
		// A folder with a Cache but no Preferences is not a profile.
		{j(chrome, "Crashpad", "Cache", "notaprofile.bin"), 1000, 30 * day},

		// Firefox: cache in the local profile, credentials in the roaming one.
		{j(local, ff, "cache2", "entries", "0A1B2C3D"), 350000, 5 * day},
		{j(local, ff, "startupCache", "startupCache.8.little"), 40000, 5 * day},
		{j(roaming, ff, "logins.json"), 3000, 10 * day},
		{j(roaming, ff, "key4.db"), 300000, 10 * day},
		{j(roaming, ff, "places.sqlite"), 5000000, 1 * day},

		// Discord (Electron): caches vs local storage and settings.
		{j(roaming, "discord", "Cache", "Cache_Data", "f_000042"), 2200000, 4 * day},
		{j(roaming, "discord", "Code Cache", "js", "index"), 24000, 4 * day},
		{j(roaming, "discord", "Local Storage", "leveldb", "000005.ldb"), 11000, 4 * day},
		{j(roaming, "discord", "settings.json"), 400, 4 * day},

		// VS Code: caches vs user settings and workspace state.
		{j(roaming, "Code", "Cache", "Cache_Data", "f_00000a"), 500000, 6 * day},
		{j(roaming, "Code", "CachedData", "a1b2c3", "chrome", "js", "x.code"), 800000, 6 * day},
		{j(roaming, "Code", "CachedExtensionVSIXs", "ms-python.python-2025.1.0"), 15000000, 20 * day},
		{j(roaming, "Code", "User", "settings.json"), 2500, 3 * day},
		{j(roaming, "Code", "User", "workspaceStorage", "9f8e", "state.vscdb"), 70000, 3 * day},

		// JetBrains: caches/index (opt-in) vs local history; Toolbox excluded.
		{j(local, "JetBrains", "IntelliJIdea2025.2", "caches", "content.dat"), 3000000, 2 * day},
		{j(local, "JetBrains", "IntelliJIdea2025.2", "index", "stubs", "stubs.dat"), 2000000, 2 * day},
		{j(local, "JetBrains", "IntelliJIdea2025.2", "LocalHistory", "changes.storageData"), 400000, 2 * day},
		{j(local, "JetBrains", "Toolbox", "caches", "toolbox.cache"), 1000, 2 * day},

		// npm: cache and logs vs npx installs.
		{j(local, "npm-cache", "_cacache", "content-v2", "sha512", "ab", "cd", "ef01"), 1200000, 12 * day},
		{j(local, "npm-cache", "_logs", "2026-09-01T10_00_00_000Z-debug-0.log"), 30000, 30 * day},
		{j(local, "npm-cache", "_npx", "8e1f", "package.json"), 500, 12 * day},

		// Go build cache vs module cache.
		{j(local, "go-build", "3f", "3fa1b2-d"), 700000, 3 * day},
		{j(prof, "go", "pkg", "mod", "golang.org", "x", "sys@v0.48.0", "go.mod"), 300, 30 * day},

		// Cargo: archives vs extracted sources and installed binaries.
		{j(prof, ".cargo", "registry", "cache", "index.crates.io-6f17", "serde-1.0.200.crate"), 77000, 40 * day},
		{j(prof, ".cargo", "registry", "src", "index.crates.io-6f17", "serde-1.0.200", "Cargo.toml"), 3000, 40 * day},
		{j(prof, ".cargo", "bin", "cargo.exe"), 9000, 40 * day},

		// Windows: Outlook attachment cache is excluded from INetCache.
		{j(local, "Microsoft", "Windows", "INetCache", "IE", "X1Y2Z3", "logo[1].png"), 30000, 10 * day},
		{j(local, "Microsoft", "Windows", "INetCache", "Content.Outlook", "QWER1234", "contract-edited.docx"), 80000, 10 * day},

		// Simulated Recycle Bin.
		{j("C", "$Recycle.Bin", "S-1-5-21-sandbox", "$RABC123.txt"), 6000, 2 * day},
		{j("C", "$Recycle.Bin", "S-1-5-21-sandbox", "$IABC123.txt"), 100, 2 * day},
		{j("C", "$Recycle.Bin", "S-1-5-21-sandbox", "$RDEF456", "old-photo.jpg"), 2000000, 2 * day},
	}
}

// WriteFile creates a file of the given size with creation and modification
// times set to t, creating parent directories as needed.
func WriteFile(path string, size int, t time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		return err
	}
	return filesystem.SetTimes(path, t, t)
}
