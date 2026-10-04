package install_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/install"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func TestDirAndInDir(t *testing.T) {
	dir := install.Dir(`C:\Users\alice\AppData\Local`)
	if dir != `C:\Users\alice\AppData\Local\Programs\oow` {
		t.Fatalf("Dir = %s", dir)
	}
	if install.Dir("") != "" {
		t.Error("Dir of an unknown folder must be empty, never relative")
	}
	for exe, want := range map[string]bool{
		dir + `\oow.exe`: true,
		`c:\users\ALICE\appdata\local\programs\OOW\OOW.EXE`: true,
		`\\?\` + dir + `\oow.exe`:                           true,
		dir + `\other.exe`:                                  false,
		dir + `\sub\oow.exe`:                                false,
		`D:\Projects\oow\bin\oow.exe`:                       false,
		``:                                                  false,
	} {
		if install.InDir(exe, dir) != want {
			t.Errorf("install.InDir(%s) = %v", exe, !want)
		}
	}
}

func TestDetectManager(t *testing.T) {
	env := func(vars map[string]string) install.Env { return func(k string) string { return vars[k] } }
	cases := []struct {
		path string
		vars map[string]string
		want install.Managed
	}{
		{`C:\Users\a\AppData\Local\Microsoft\WinGet\Packages\Contoso.Oow_Microsoft.Winget.Source_8wekyb3d8bbwe\oow.exe`, nil,
			install.Managed{Manager: install.Winget, Package: "Contoso.Oow"}},
		{`C:\Program Files\WinGet\Packages\Contoso.Oow_Microsoft.Winget.Source_8wekyb3d8bbwe\oow.exe`, nil, install.Managed{Manager: install.Winget, Package: "Contoso.Oow"}},
		{`C:\Users\a\AppData\Local\Microsoft\WinGet\Links\oow.exe`, nil, install.Managed{Manager: install.Winget}},
		{`C:\Users\a\scoop\apps\oow\current\oow.exe`, nil, install.Managed{Manager: install.Scoop, Package: "oow"}},
		{`C:\ProgramData\scoop\apps\oow\1.2.0\oow.exe`, nil, install.Managed{Manager: install.Scoop, Package: "oow"}},
		{`D:\tools\sc\apps\oow\current\oow.exe`, map[string]string{"SCOOP": `D:\tools\sc`}, install.Managed{Manager: install.Scoop, Package: "oow"}},
		{`C:\Users\a\scoop\shims\oow.exe`, nil, install.Managed{Manager: install.Scoop}},
		{`C:\ProgramData\chocolatey\lib\oow\tools\oow.exe`, nil, install.Managed{Manager: install.Chocolatey, Package: "oow"}},
		{`E:\choco\lib\oow.portable\tools\oow.exe`, map[string]string{"ChocolateyInstall": `E:\choco`}, install.Managed{Manager: install.Chocolatey, Package: "oow.portable"}},
		{`C:\ProgramData\chocolatey\bin\oow.exe`, nil, install.Managed{Manager: install.Chocolatey}},
		// Not managed: the installer's folder, a source build, and lookalikes.
		{`C:\Users\a\AppData\Local\Programs\oow\oow.exe`, nil, install.Managed{}},
		{`D:\Projects\out-of-windows\bin\oow.exe`, nil, install.Managed{}},
		{`D:\Projects\scoop-apps\oow.exe`, nil, install.Managed{}},
		{`D:\x\apps\oow\oow.exe`, map[string]string{"SCOOP": ``}, install.Managed{}},
	}
	for _, c := range cases {
		if got := install.DetectManager(env(c.vars), c.path); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.path, got, c.want)
		}
	}
	// The resolved path is checked too (winget links point into Packages).
	got := install.DetectManager(nil, `C:\bin\oow.exe`, `C:\Users\a\AppData\Local\Microsoft\WinGet\Packages\X.Y_s\oow.exe`)
	if got.Manager != install.Winget || got.UpdateCommand() != "winget upgrade --id X.Y" || got.RemoveCommand() != "winget uninstall --id X.Y" {
		t.Errorf("resolved path: %+v", got)
	}
	if m := (install.Managed{Manager: install.Scoop}); m.UpdateCommand() != "scoop update oow" {
		t.Errorf("default package name: %s", m.UpdateCommand())
	}
}

func TestRemoveEntryKeepsEverythingElse(t *testing.T) {
	dir := `C:\Users\alice\AppData\Local\Programs\oow`
	expand := func(s string) string {
		return strings.ReplaceAll(s, "%LOCALAPPDATA%", `C:\Users\alice\AppData\Local`)
	}
	value := `C:\Tools;%LOCALAPPDATA%\Programs\oow;;C:\Users\alice\AppData\Local\Programs\OOW\;"C:\Program Files\x";%LOCALAPPDATA%\Programs\oow-old`
	got, n := install.RemoveEntry(value, dir, expand)
	if n != 2 || got != `C:\Tools;;"C:\Program Files\x";%LOCALAPPDATA%\Programs\oow-old` {
		t.Fatalf("RemoveEntry = %q, %d", got, n)
	}
	if !install.HasEntry(value, dir, expand) || install.HasEntry(got, dir, expand) {
		t.Error("HasEntry disagrees with RemoveEntry")
	}
	if same, n := install.RemoveEntry(`C:\Tools;D:\bin`, dir, expand); n != 0 || same != `C:\Tools;D:\bin` {
		t.Errorf("no entry: %q, %d", same, n)
	}
	for _, e := range []string{"", ";", "relative\\oow", `C:\Users\alice\AppData\Local\Programs`} {
		if install.SameDir(e, dir, expand) {
			t.Errorf("install.SameDir(%q) matched", e)
		}
	}
}

type fakePath struct {
	value      string
	expandable bool
	gets       int
	changeOn   int // change the value on this Get call
	set        []string
}

func (f *fakePath) Get() (string, bool, error) {
	f.gets++
	if f.gets == f.changeOn {
		f.value += `;C:\New`
	}
	return f.value, f.expandable, nil
}
func (f *fakePath) Set(v string, e bool) error { f.set = append(f.set, v); f.value = v; return nil }
func (f *fakePath) Expand(s string) string     { return s }

func TestRemoveFromUserPath(t *testing.T) {
	dir := `C:\P\oow`
	fp := &fakePath{value: `C:\A;C:\P\oow;C:\B`, expandable: true}
	if n, err := install.RemoveFromUserPath(fp, dir); err != nil || n != 1 || fp.value != `C:\A;C:\B` {
		t.Fatalf("n=%d err=%v value=%q", n, err, fp.value)
	}
	// Nothing to do: no write at all.
	fp = &fakePath{value: `C:\A`}
	if n, err := install.RemoveFromUserPath(fp, dir); err != nil || n != 0 || len(fp.set) != 0 {
		t.Errorf("no entry: n=%d err=%v writes=%d", n, err, len(fp.set))
	}
	// A concurrent change between read and write is never overwritten.
	fp = &fakePath{value: `C:\A;C:\P\oow`, changeOn: 2}
	if _, err := install.RemoveFromUserPath(fp, dir); !errors.Is(err, install.ErrPathChanged) || len(fp.set) != 0 {
		t.Errorf("concurrent change: err=%v writes=%d", err, len(fp.set))
	}
}

func TestVerifiedExeRemoval(t *testing.T) {
	f := testutil.NewFixture(t)
	dir := f.Path(`Local/Programs/oow`)
	exe := f.File(`Local/Programs/oow/oow.exe`, 4096, time.Hour)

	if _, err := (install.RunningExe{}).RemoveExe(exe, dir); !errors.Is(err, install.ErrRunning) || !f.Exists(`Local/Programs/oow/oow.exe`) {
		t.Fatalf("running exe: err = %v", err)
	}
	if _, err := (install.VerifiedExe{}).RemoveExe(f.File(`elsewhere/oow.exe`, 10, time.Hour), dir); err == nil {
		t.Error("removed an executable outside the install folder")
	}
	var checked []string
	v := install.VerifiedExe{Check: func(final string) error { checked = append(checked, final); return nil }}
	gone, err := v.RemoveExe(exe, dir)
	if err != nil || !gone || f.Exists(`Local/Programs/oow`) {
		t.Fatalf("gone=%v err=%v", gone, err)
	}
	if len(checked) != 2 {
		t.Errorf("guard consulted %d times, want 2 (file and folder)", len(checked))
	}
	if !f.Exists(`elsewhere/oow.exe`) || !f.Exists(`Local/Programs`) {
		t.Error("removed more than the executable and its folder")
	}

	// A folder with anything else in it stays.
	exe = f.File(`Local/Programs/oow/oow.exe`, 4096, time.Hour)
	f.File(`Local/Programs/oow/notes.txt`, 10, time.Hour)
	gone, err = (install.VerifiedExe{}).RemoveExe(exe, dir)
	if err != nil || gone || !f.Exists(`Local/Programs/oow/notes.txt`) || f.Exists(`Local/Programs/oow/oow.exe`) {
		t.Errorf("non-empty folder: gone=%v err=%v", gone, err)
	}

	// A check that refuses keeps the file.
	exe = f.File(`Local/Programs/oow/oow.exe`, 4096, time.Hour)
	refuse := install.VerifiedExe{Check: func(string) error { return errors.New("no") }}
	if _, err := refuse.RemoveExe(exe, dir); err == nil || !f.Exists(`Local/Programs/oow/oow.exe`) {
		t.Errorf("refusing check: err = %v", err)
	}
}

func TestRemoveOwnFileRefusesFoldersAndLinks(t *testing.T) {
	f := testutil.NewFixture(t)
	f.File(`target/keep.txt`, 10, time.Hour)
	link := f.Junction(`app/oow.exe.old`, f.Path(`target`))
	if err := install.RemoveOwnFile(link, nil); err == nil {
		t.Error("removed a junction")
	}
	if err := install.RemoveOwnFile(f.Dir(`app/oow.exe.new`, time.Hour), nil); err == nil {
		t.Error("removed a folder")
	}
	if !f.Exists(`target/keep.txt`) || !f.Exists(`app/oow.exe.old`) {
		t.Error("link or its target changed")
	}
	if err := install.RemoveOwnFile(f.Path(`app/missing`), nil); err != nil {
		t.Errorf("missing file: %v", err)
	}
	if _, err := install.RemoveEmptyDir(f.Path(`app/oow.exe.old`), nil); err == nil {
		t.Error("RemoveEmptyDir accepted a junction")
	}
	if filepath.Base(link) != "oow.exe.old" {
		t.Fatal("fixture")
	}
}
