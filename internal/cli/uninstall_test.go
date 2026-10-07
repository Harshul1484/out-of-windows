package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

type uninstallDoc struct {
	Schema  string `json:"schema"`
	DryRun  bool   `json:"dry_run"`
	Results []struct {
		App struct {
			Name string `json:"name"`
		} `json:"app"`
		Plan *struct {
			Command string `json:"command"`
			Method  string `json:"method"`
		} `json:"plan"`
		Outcome *struct {
			Removed  bool `json:"removed"`
			ExitCode int  `json:"exit_code"`
		} `json:"outcome"`
		Error     string `json:"error"`
		Leftovers *struct {
			Candidates []struct {
				Path       string `json:"path"`
				Confidence string `json:"confidence"`
			} `json:"candidates"`
		} `json:"leftovers"`
		Recycled *struct {
			Recycled []struct {
				Path string `json:"path"`
			} `json:"recycled"`
		} `json:"recycled"`
	} `json:"results"`
}

func TestUninstallList(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("uninstall", "--list", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var doc struct {
		Schema string `json:"schema"`
		Apps   []struct {
			Name     string   `json:"name"`
			Problems []string `json:"problems"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != "oow.apps/v1" || len(doc.Apps) != 6 {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
	text, _, _ := e.run("uninstall", "--list")
	if !strings.Contains(text, "Litware Tool") || !strings.Contains(text, "its uninstaller is missing") {
		t.Errorf("text list:\n%s", text)
	}
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	before := snapshotAll(t, e)
	out, _, code := e.run("uninstall", "Contoso Studio", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	doc := decode[uninstallDoc](t, out)
	r := doc.Results[0]
	if !doc.DryRun || r.Plan == nil || r.Outcome != nil || r.Leftovers == nil || len(r.Leftovers.Candidates) != 4 {
		t.Fatalf("doc = %+v", doc)
	}
	if after := snapshotAll(t, e); len(after) != len(before) {
		t.Fatal("dry run changed files")
	}
}

func TestUninstallRequiresConfirmation(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("uninstall", "Contoso Studio"); code != cli.ExitNeedsConfirm {
		t.Fatalf("code = %d, want %d", code, cli.ExitNeedsConfirm)
	}
	if !e.exists(`C\Program Files\Contoso\Studio\studio.exe`) {
		t.Fatal("uninstalled without confirmation")
	}
	if _, _, code := e.run("uninstall"); code != cli.ExitUsage {
		t.Errorf("no app named: code = %d", code)
	}
	if _, _, code := e.run("uninstall", "contoso", "--dry-run"); code != cli.ExitUsage {
		t.Errorf("ambiguous name: code = %d", code)
	}
	if _, _, code := e.run("uninstall", "Nonexistent App", "--dry-run"); code != cli.ExitUsage {
		t.Errorf("unknown app: code = %d", code)
	}
}

func TestUninstallEndToEnd(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("uninstall", "Contoso Studio", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	doc := decode[uninstallDoc](t, out)
	r := doc.Results[0]
	if r.Outcome == nil || !r.Outcome.Removed || r.Recycled == nil || len(r.Recycled.Recycled) != 3 {
		t.Fatalf("result = %+v", r)
	}
	// High-confidence leftovers are gone; the medium one and other Contoso
	// products remain.
	for _, gone := range []string{`C\Program Files\Contoso\Studio`, `C\ProgramData\Contoso\Studio`,
		`C\Users\sandbox\AppData\Roaming\Contoso\Studio`} {
		if e.exists(gone) {
			t.Errorf("%s not recycled", gone)
		}
	}
	for _, kept := range []string{`C\Users\sandbox\AppData\Local\Contoso Studio\logs\studio.log`,
		`C\Program Files\Contoso\Agent\agent.exe`, `C\ProgramData\Contoso\license.dat`} {
		if !e.exists(kept) {
			t.Errorf("%s removed", kept)
		}
	}
	// The uninstall is recorded with the app's identity.
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `"command": "uninstall"`) || !strings.Contains(hist, `"name": "Contoso Studio"`) {
		t.Errorf("history:\n%s", hist)
	}
	// A later leftovers scan still finds the medium-confidence folder.
	lo, _, code := e.run("leftovers", "--dry-run", "--json")
	if code != 0 || !strings.Contains(lo, `Contoso Studio`) || !strings.Contains(lo, `"medium"`) {
		t.Errorf("leftovers after uninstall: code=%d\n%s", code, lo)
	}
}

func TestUninstallFailureIsReported(t *testing.T) {
	e := newEnv(t)
	out, errOut, code := e.run("uninstall", "Northwind Sync", "--yes", "--wait", "200ms", "--json")
	if code != cli.ExitError {
		t.Fatalf("code = %d", code)
	}
	if strings.Count(out, `"schema"`) != 1 || errOut != "" {
		t.Fatalf("expected exactly one JSON document and no stderr:\n%s\n%s", out, errOut)
	}
	doc := decode[uninstallDoc](t, out)
	if r := doc.Results[0]; r.Outcome == nil || r.Outcome.Removed || r.Outcome.ExitCode != 1603 || r.Error == "" {
		t.Fatalf("result = %+v", r)
	}
	if !e.exists(`C\Program Files\Northwind\Sync\sync.exe`) {
		t.Fatal("files removed after failed uninstall")
	}
}

func TestBrokenEntryCannotBeUninstalled(t *testing.T) {
	e := newEnv(t)
	_, errOut, code := e.run("uninstall", "Litware Tool", "--yes")
	if code != cli.ExitError || !strings.Contains(errOut, "uninstaller is missing") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestLeftoversCommand(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("leftovers", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var doc struct {
		Schema string `json:"schema"`
		Result struct {
			Candidates []struct {
				Path       string `json:"path"`
				App        string `json:"app"`
				Confidence string `json:"confidence"`
				Source     string `json:"source"`
			} `json:"candidates"`
			Kept []struct {
				Path   string `json:"path"`
				Reason string `json:"reason"`
			} `json:"kept"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	apps := map[string]string{}
	for _, c := range doc.Result.Candidates {
		apps[c.App] = c.Confidence
		if strings.Contains(c.Path, "Wingtip") || strings.Contains(c.Path, "Northwind") || strings.Contains(c.Path, "Temp") {
			t.Errorf("unexpected candidate %s", c.Path)
		}
	}
	if apps["Litware Tool"] != "high" || apps["Old Editor"] != "medium" || len(apps) != 2 {
		t.Errorf("candidate apps = %v", apps)
	}
	// The simulated scheduled task that loads a DLL from Old Editor's
	// program folder keeps that folder.
	taskKept := false
	for _, k := range doc.Result.Kept {
		taskKept = taskKept || strings.HasSuffix(k.Path, `\Program Files\OldEditor`) && strings.Contains(k.Reason, "scheduled task")
	}
	for _, c := range doc.Result.Candidates {
		if strings.HasSuffix(c.Path, `\Program Files\OldEditor`) {
			t.Error("folder used by a scheduled task offered")
		}
	}
	if !taskKept {
		t.Errorf("kept = %+v", doc.Result.Kept)
	}

	// Non-interactive real run needs --yes and moves only high confidence.
	if _, _, code := e.run("leftovers"); code != cli.ExitNeedsConfirm {
		t.Errorf("without --yes: code = %d", code)
	}
	if _, _, code := e.run("leftovers", "--yes"); code != 0 {
		t.Fatalf("--yes: code = %d", code)
	}
	if e.exists(`C\Program Files\Litware Tool`) {
		t.Error("high-confidence leftover kept")
	}
	if !e.exists(`C\Program Files\OldEditor\plugins\spell.dll`) {
		t.Error("medium-confidence leftover removed without explicit choice")
	}
	if !e.exists(`C\Users\sandbox\AppData\Roaming\Wingtip Toys\save.dat`) {
		t.Error("installed app's data removed")
	}
	bin, _ := os.ReadDir(filepath.Join(sandbox.RecycleBinDir(e.root), "S-1-5-21-sandbox"))
	found := false
	for _, b := range bin {
		found = found || strings.HasPrefix(b.Name(), "$R") && b.IsDir()
	}
	if !found {
		t.Error("leftover not in the Recycle Bin")
	}
}

func snapshotAll(t *testing.T, e *env) map[string]string {
	t.Helper()
	return snapshotDir(t, e.system())
}

func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	return testutil.SnapshotDir(t, dir)
}
