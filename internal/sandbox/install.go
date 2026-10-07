package sandbox

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/install"
)

// SelfExe is where the simulated installer put the executable:
// <root>\C\Users\sandbox\AppData\Local\Programs\oow\oow.exe. It is a plain
// file, never run, so update and remove can act on it safely.
func SelfExe(root string) string {
	return filepath.Join(install.Dir(Locations(root).LocalAppData), install.ExeName())
}

// InstallPathEntry is the user PATH entry the simulated installer adds.
var InstallPathEntry = `%LOCALAPPDATA%\Programs\` + buildinfo.AppID

// SeedInstall simulates `scripts/install.ps1`: a fake executable in the
// install folder, and the folder appended to the simulated user PATH
// (Paths) unless it is already there.
func SeedInstall(root string) error {
	if err := WriteFile(SelfExe(root), 4096, time.Now().Add(-30*24*time.Hour)); err != nil {
		return err
	}
	p := Paths{Root: root}
	cur, err := p.Read(envpath.User)
	if err != nil {
		return err
	}
	expand := func(s string) string { x, _ := p.Expand(s); return x }
	if install.HasEntry(cur.Raw, install.Dir(Locations(root).LocalAppData), expand) {
		return nil
	}
	next := envpath.Value{Raw: cur.Raw, Expand: cur.Expand || !cur.Exists, Exists: true}
	if next.Raw != "" && !strings.HasSuffix(next.Raw, ";") {
		next.Raw += ";"
	}
	next.Raw += InstallPathEntry
	return p.WriteUser(cur, next)
}
