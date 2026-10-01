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
			filepath.Join(local, "Microsoft"),
			filepath.Join(local, "Packages"),
			filepath.Join(local, "Programs"),
			filepath.Join(roaming, "Microsoft"),
		},
	}
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

	// A junction inside Temp that points at Documents. Cleanup must never
	// follow it: deleting through it would destroy user files.
	link := filepath.Join(l.Temp, "link-to-documents")
	if _, err := os.Lstat(link); os.IsNotExist(err) {
		if err := MakeJunction(link, l.UserContent[1]); err != nil {
			return fmt.Errorf("create junction: %w", err)
		}
	}
	return nil
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
