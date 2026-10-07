package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/install"
)

// SelfExe is where the simulated installer put the executable:
// <root>\C\Users\sandbox\AppData\Local\Programs\oow\oow.exe. It is a plain
// file, never run, so update and remove can act on it safely.
func SelfExe(root string) string {
	return filepath.Join(install.Dir(Locations(root).LocalAppData), install.ExeName())
}

// SeedInstall simulates `scripts/install.ps1`: a fake executable in the
// install folder and a user PATH that contains it among other entries.
func SeedInstall(root string) error {
	if err := WriteFile(SelfExe(root), 4096, time.Now().Add(-30*24*time.Hour)); err != nil {
		return err
	}
	up := UserPath{Root: root}
	return up.Set(`C:\Tools\bin;%LOCALAPPDATA%\Programs\`+buildinfo.AppID+`;%USERPROFILE%\go\bin`, true)
}

// UserPath simulates HKCU\Environment\Path with a JSON file in the sandbox.
type UserPath struct{ Root string }

type userPathFile struct {
	Value      string `json:"value"`
	Expandable bool   `json:"expandable"`
}

func (u UserPath) file() string { return filepath.Join(u.Root, "registry", "user-path.json") }

// Get implements install.UserPath.
func (u UserPath) Get() (string, bool, error) {
	data, err := os.ReadFile(u.file())
	if errors.Is(err, os.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", true, err
	}
	var f userPathFile
	if err := json.Unmarshal(data, &f); err != nil {
		return "", true, err
	}
	return f.Value, f.Expandable, nil
}

// Set implements install.UserPath.
func (u UserPath) Set(value string, expandable bool) error {
	if err := os.MkdirAll(filepath.Dir(u.file()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(userPathFile{Value: value, Expandable: expandable}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(u.file(), append(data, '\n'), 0o644)
}

// Expand maps the user's folder variables into the sandbox, so entries such
// as %LOCALAPPDATA%\Programs\oow compare like they would on Windows. Other
// variables are left as they are (and so never match).
func (u UserPath) Expand(entry string) string {
	l := Locations(u.Root)
	r := strings.NewReplacer(
		"%LOCALAPPDATA%", l.LocalAppData, "%localappdata%", l.LocalAppData,
		"%APPDATA%", l.RoamingAppData, "%appdata%", l.RoamingAppData,
		"%USERPROFILE%", l.UserProfile, "%userprofile%", l.UserProfile,
	)
	return r.Replace(entry)
}
