package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

type removeDoc struct {
	Schema    string `json:"schema"`
	DryRun    bool   `json:"dry_run"`
	KeepData  bool   `json:"keep_data"`
	Executed  bool   `json:"executed"`
	ManagedBy *struct {
		Manager       string `json:"manager"`
		RemoveCommand string `json:"remove_command"`
	} `json:"managed_by"`
	Items []struct {
		Kind   string `json:"kind"`
		Path   string `json:"path"`
		Action string `json:"action"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"items"`
	ManualSteps []struct {
		Shell   string `json:"shell"`
		Command string `json:"command"`
	} `json:"manual_steps"`
	Errors  int    `json:"errors"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func (d removeDoc) item(kind string) (action, status, reason string) {
	for _, it := range d.Items {
		if it.Kind == kind {
			return it.Action, it.Status, it.Reason
		}
	}
	return "", "", ""
}

func newRemoveEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	if err := sandbox.SeedInstall(e.root); err != nil {
		t.Fatal(err)
	}
	// Some history, so the data folder has content.
	if _, _, code := e.run("clean", "--rule", "temp.user", "--yes"); code != 0 {
		t.Fatal("seed history")
	}
	return e
}

func (e *env) userPath() string {
	e.t.Helper()
	v, err := sandbox.Paths{Root: e.root}.Read(envpath.User)
	if err != nil {
		e.t.Fatal(err)
	}
	return v.Raw
}

func (e *env) dataDir() string { return sandbox.DataDir(e.root) }

func TestRemoveDryRunChangesNothing(t *testing.T) {
	e := newRemoveEnv(t)
	before := testutil.SnapshotDir(t, filepath.Join(e.root, "C"))
	pathBefore := e.userPath()
	out, _, code := e.run("remove", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[removeDoc](t, out)
	if d.Schema != "oow.remove/v1" || !d.DryRun || d.Executed || len(d.ManualSteps) != 0 {
		t.Fatalf("doc = %+v", d)
	}
	for kind, action := range map[string]string{"executable": "remove", "install-dir": "remove-if-empty",
		"path-entry": "remove-from-path", "data-dir": "recycle"} {
		if a, s, _ := d.item(kind); a != action || s != "planned" {
			t.Errorf("%s: action=%q status=%q", kind, a, s)
		}
	}
	after := testutil.SnapshotDir(t, filepath.Join(e.root, "C"))
	if len(after) != len(before) || e.userPath() != pathBefore {
		t.Fatal("dry run changed something")
	}
}

func TestRemoveNeedsConfirmation(t *testing.T) {
	e := newRemoveEnv(t)
	_, errOut, code := e.run("remove")
	if code != cli.ExitNeedsConfirm || !strings.Contains(errOut, "--yes") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Programs\oow\oow.exe`) {
		t.Fatal("removed without confirmation")
	}
}

func TestRemoveYesRemovesEverythingItInstalled(t *testing.T) {
	e := newRemoveEnv(t)
	binDir := filepath.Join(sandbox.RecycleBinDir(e.root), "S-1-5-21-sandbox")
	binBefore, _ := os.ReadDir(binDir)
	pathBefore := e.userPath()
	if !strings.HasSuffix(pathBefore, ";"+sandbox.InstallPathEntry) {
		t.Fatalf("seeded user PATH = %q, want the installer's entry last", pathBefore)
	}
	out, _, code := e.run("remove", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[removeDoc](t, out)
	if !d.Executed || d.Errors != 0 || len(d.ManualSteps) != 0 {
		t.Fatalf("doc = %+v", d)
	}
	for _, kind := range []string{"executable", "install-dir", "path-entry", "data-dir"} {
		if _, s, r := d.item(kind); s != "done" {
			t.Errorf("%s: status=%q reason=%q", kind, s, r)
		}
	}
	if e.exists(`C\Users\sandbox\AppData\Local\Programs\oow`) {
		t.Error("install folder still there")
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Programs`) {
		t.Error("removed the Programs folder itself")
	}
	// Only the installer's entry goes; every other entry (unexpanded
	// variables, a duplicate, an undefined %TOOLS_HOME%) stays byte for byte.
	if got, want := e.userPath(), strings.TrimSuffix(pathBefore, ";"+sandbox.InstallPathEntry); got != want {
		t.Errorf("user PATH = %q, want %q", got, want)
	}
	if _, err := os.Stat(e.dataDir()); !os.IsNotExist(err) {
		t.Error("data folder not moved")
	}
	binAfter, _ := os.ReadDir(binDir)
	if len(binAfter) != len(binBefore)+1 {
		t.Errorf("Recycle Bin went from %d to %d entries, want one more (the data folder)", len(binBefore), len(binAfter))
	}
	// Everything else is untouched.
	for _, kept := range []string{`C\Users\sandbox\Documents\thesis.docx`, `C\Windows\System32\kernel32.dll`,
		`C\Users\sandbox\AppData\Local\Contoso\settings.json`} {
		if !e.exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
}

func TestRemoveKeepDataRecordsHistory(t *testing.T) {
	e := newRemoveEnv(t)
	out, _, code := e.run("remove", "--yes", "--keep-data", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[removeDoc](t, out)
	if a, s, _ := d.item("data-dir"); a != "keep" || s != "kept" || !d.KeepData {
		t.Errorf("data-dir: action=%q status=%q", a, s)
	}
	if e.exists(`C\Users\sandbox\AppData\Local\Programs\oow\oow.exe`) {
		t.Error("program not removed")
	}
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `"command": "remove"`) || !strings.Contains(hist, `"command": "clean"`) {
		t.Errorf("history:\n%s", hist)
	}
}

func TestRemovePointsToThePackageManager(t *testing.T) {
	e := newRemoveEnv(t)
	exe := filepath.Join(e.root, `C\ProgramData\chocolatey\lib\oow\tools\oow.exe`)
	if err := sandbox.WriteFile(exe, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cli.SetSelfExe(exe))
	before := testutil.SnapshotDir(t, filepath.Join(e.root, "C"))
	out, _, code := e.run("remove", "--yes", "--json")
	d := decode[removeDoc](t, out)
	if code != cli.ExitError || d.ManagedBy == nil || d.ManagedBy.Manager != "chocolatey" ||
		!strings.Contains(d.Error, "choco uninstall oow") || d.Executed {
		t.Fatalf("code = %d, doc = %+v", code, d)
	}
	if len(testutil.SnapshotDir(t, filepath.Join(e.root, "C"))) != len(before) || !strings.Contains(e.userPath(), `\Programs\oow`) {
		t.Fatal("files or PATH changed for a package-manager install")
	}
}

// A copy outside the installer's folder (a source build) is never deleted,
// and the PATH entry of the real installed copy stays.
func TestRemoveKeepsCopiesOutsideTheInstallFolder(t *testing.T) {
	e := newRemoveEnv(t)
	exe := filepath.Join(e.root, `D\dev\out-of-windows\bin\oow.exe`)
	if err := sandbox.WriteFile(exe, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cli.SetSelfExe(exe))
	out, _, code := e.run("remove", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[removeDoc](t, out)
	if a, _, r := d.item("executable"); a != "keep" || !strings.Contains(r, "installer's folder") {
		t.Errorf("executable: action=%q reason=%q", a, r)
	}
	if a, _, _ := d.item("path-entry"); a != "keep" {
		t.Errorf("path-entry action = %q", a)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Error("source build deleted")
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Programs\oow\oow.exe`) || !strings.Contains(e.userPath(), `\Programs\oow`) {
		t.Error("the installed copy was touched")
	}
}

// A data folder the user whitelisted is kept, whatever remove wants.
func TestRemoveHonoursTheWhitelist(t *testing.T) {
	e := newRemoveEnv(t)
	if _, _, code := e.run("config", "whitelist", "add", filepath.Join(e.dataDir(), "logs")); code != 0 {
		t.Fatal("whitelist add failed")
	}
	out, _, code := e.run("remove", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[removeDoc](t, out)
	if a, _, r := d.item("data-dir"); a != "keep" || !strings.Contains(r, "whitelist") {
		t.Errorf("data-dir: action=%q reason=%q", a, r)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir(), "config.json")); err != nil {
		t.Error("whitelisted data folder moved")
	}
}

func TestRemoveTextOutput(t *testing.T) {
	e := newRemoveEnv(t)
	out, _, code := e.run("remove", "--dry-run")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"Remove oow", "DRY RUN", "SANDBOX", "Program", "PATH entry",
		"Recycle Bin", "Dry run: nothing was changed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI escapes with NO_COLOR")
	}
}
