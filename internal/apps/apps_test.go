package apps

import "testing"

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"7-Zip 23.01 (x64)":             "7zip",
		"7-Zip":                         "7zip",
		"Mozilla Firefox (x64 en-US)":   "mozillafirefox",
		"Python 3.12.0 (64-bit)":        "python",
		"Microsoft 365 Apps":            "microsoft365apps",
		"Unity 2022.3.1f1":              "unity",
		"Contoso Studio v4":             "contosostudio",
		"Node.js":                       "nodejs",
		"Visual Studio Code [User] x64": "visualstudiocode",
		"  Ünïcode App  ":               "ünïcodeapp",
	} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Contoso Ltd.":        "contoso",
		"Fabrikam, Inc.":      "fabrikam",
		"Northwind Traders":   "northwindtraders",
		"Example Corporation": "example",
	} {
		if got := NormalizePublisher(in); got != want {
			t.Errorf("NormalizePublisher(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsDistinctive(t *testing.T) {
	for _, n := range []string{"app", "data", "cache", "launcher", "microsoft", "abc", "studio", "updater"} {
		if IsDistinctive(n) {
			t.Errorf("%q considered distinctive", n)
		}
	}
	for _, n := range []string{"contosostudio", "7zip", "discord", "fabrikamplayer"} {
		if !IsDistinctive(n) {
			t.Errorf("%q not distinctive", n)
		}
	}
}

func TestExeNameAndKeys(t *testing.T) {
	if got := ExeName(`"C:\Program Files\Contoso\Studio\studio.exe",0`); got != "studio.exe" {
		t.Errorf("ExeName = %q", got)
	}
	if got := ExeName(`C:\x\app.ico`); got != "" {
		t.Errorf("ExeName(icon) = %q", got)
	}
	a := App{Name: "Fabrikam Player 2.0", InstallLocation: `C:\Program Files\Fabrikam Player\`, DisplayIcon: `C:\Program Files\Fabrikam Player\fplayer.exe,0`}
	keys := a.Keys()
	if len(keys) != 2 || keys[0] != "fabrikamplayer" || keys[1] != "fplayer" {
		t.Errorf("Keys = %v", keys)
	}
}

func TestParseCommandLine(t *testing.T) {
	exists := func(p string) bool {
		return p == `C:\Program Files\App\uninstall.exe` || p == `C:\Program Files (x86)\Old App\unins000.exe`
	}
	cases := []struct {
		in   string
		exe  string
		args []string
	}{
		{`"C:\Program Files\App\uninstall.exe" /S`, `C:\Program Files\App\uninstall.exe`, []string{"/S"}},
		{`C:\Program Files\App\uninstall.exe /S --force`, `C:\Program Files\App\uninstall.exe`, []string{"/S", "--force"}},
		{`C:\Program Files (x86)\Old App\unins000.exe`, `C:\Program Files (x86)\Old App\unins000.exe`, nil},
		{`MsiExec.exe /X{12345678-1234-1234-1234-123456789ABC}`, `MsiExec.exe`, []string{"/X{12345678-1234-1234-1234-123456789ABC}"}},
		{`"C:\Program Files\App\uninstall.exe" --path "C:\My Data"`, `C:\Program Files\App\uninstall.exe`, []string{"--path", `C:\My Data`}},
	}
	for _, c := range cases {
		got, err := ParseCommandLine(c.in, exists)
		if err != nil || got.Exe != c.exe || !equal(got.Args, c.args) {
			t.Errorf("ParseCommandLine(%q) = %+v, %v; want %s %v", c.in, got, err, c.exe, c.args)
		}
	}
	if _, err := ParseCommandLine(`"C:\unbalanced`, exists); err == nil {
		t.Error("unbalanced quotes accepted")
	}
	if code := ProductCodeIn("MsiExec.exe /I{6f1d5c3a-2b4e-4c8d-9a7f-1e2d3c4b5a69}"); code != "{6F1D5C3A-2B4E-4C8D-9A7F-1E2D3C4B5A69}" {
		t.Errorf("ProductCodeIn = %q", code)
	}
}

func TestRemovable(t *testing.T) {
	if ok, _ := (App{Source: SourceEXE, UninstallString: "x"}).Removable(); !ok {
		t.Error("normal app not removable")
	}
	for _, a := range []App{
		{Source: SourceEXE},
		{Source: SourceEXE, UninstallString: "x", NoRemove: true},
		{Source: SourceEXE, UninstallString: "x", Problems: []string{"gone"}},
	} {
		if ok, why := a.Removable(); ok || why == "" {
			t.Errorf("%+v removable", a)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
