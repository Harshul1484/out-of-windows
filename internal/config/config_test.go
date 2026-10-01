package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/config"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func TestLoadMissingReturnsDefaults(t *testing.T) {
	c, err := config.Load(filepath.Join(testutil.Dir(t), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != config.CurrentVersion || c.UI.Color != "auto" {
		t.Errorf("defaults = %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(testutil.Dir(t), "sub", "config.json")
	c := config.Default()
	if _, err := c.AddPath(`c:\keep\THIS\`); err != nil {
		t.Fatal(err)
	}
	c.AddRule("Browser.Chrome")
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Whitelist.Paths) != 1 || got.Whitelist.Paths[0] != `C:\keep\THIS` {
		t.Errorf("paths = %v", got.Whitelist.Paths)
	}
	if len(got.Whitelist.Rules) != 1 || got.Whitelist.Rules[0] != "browser.chrome" {
		t.Errorf("rules = %v", got.Whitelist.Rules)
	}
}

func TestMalformedConfigIsAnError(t *testing.T) {
	p := filepath.Join(testutil.Dir(t), "config.json")
	if err := os.WriteFile(p, []byte(`{"whitelist": [`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidWhitelistPathRejected(t *testing.T) {
	p := filepath.Join(testutil.Dir(t), "config.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"whitelist":{"paths":["relative\\dir"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err == nil {
		t.Fatal("relative whitelist path accepted")
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	p := filepath.Join(testutil.Dir(t), "config.json")
	if err := os.WriteFile(p, []byte(`{"version":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err == nil {
		t.Fatal("newer schema accepted")
	}
}

func TestWhitelistDedupAndRemove(t *testing.T) {
	c := config.Default()
	if added, _ := c.AddPath(`C:\Data`); !added {
		t.Fatal("first add failed")
	}
	if added, _ := c.AddPath(`c:\data\sub`); added {
		t.Error("child of protected path added again")
	}
	if !c.AddRule("temp.user") || c.AddRule("TEMP.USER") {
		t.Error("rule dedupe failed")
	}
	if !c.Remove(`C:\DATA\`) || len(c.Whitelist.Paths) != 0 {
		t.Errorf("remove path failed: %v", c.Whitelist.Paths)
	}
	if !c.Remove("temp.user") || c.Remove("temp.user") {
		t.Error("remove rule failed")
	}
}

func TestDirsOverrides(t *testing.T) {
	t.Setenv("OOW_CONFIG_DIR", `X:\cfg`)
	t.Setenv("OOW_DATA_DIR", `X:\data`)
	d, err := config.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if d.ConfigFile() != `X:\cfg\config.json` || d.LogDir() != `X:\data\logs` {
		t.Errorf("dirs = %+v", d)
	}
}

func TestIsPathEntry(t *testing.T) {
	for in, want := range map[string]bool{`C:\x`: true, `D:/x`: true, `temp.user`: false, `browser`: false} {
		if config.IsPathEntry(in) != want {
			t.Errorf("IsPathEntry(%q) != %v", in, want)
		}
	}
}
