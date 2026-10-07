package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

type purgeDoc struct {
	Schema  string `json:"schema"`
	DryRun  bool   `json:"dry_run"`
	Sandbox bool   `json:"sandbox"`
	Roots   []struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		Status string `json:"status"`
	} `json:"roots"`
	Projects []struct {
		Path      string `json:"path"`
		Artifacts []struct {
			Path     string   `json:"path"`
			Kind     string   `json:"kind"`
			Status   string   `json:"status"`
			Selected bool     `json:"selected"`
			Bytes    int64    `json:"bytes"`
			Reasons  []string `json:"reasons"`
			Result   *struct {
				Complete  bool  `json:"complete"`
				Reclaimed int64 `json:"reclaimed_bytes"`
			} `json:"result"`
		} `json:"artifacts"`
	} `json:"projects"`
	Summary struct {
		Artifacts      int   `json:"artifacts"`
		Selected       int   `json:"selected"`
		SelectedBytes  int64 `json:"selected_bytes"`
		Kept           int   `json:"kept"`
		Executed       bool  `json:"executed"`
		Removed        int   `json:"removed"`
		ReclaimedBytes int64 `json:"reclaimed_bytes"`
		Recycled       int   `json:"recycled"`
		Errors         int   `json:"errors"`
	} `json:"summary"`
}

func needGitCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
}

func repos(e *env, rel string) string { return filepath.Join(sandbox.ProjectsDir(e.root), rel) }

func TestPurgeDryRunJSONChangesNothing(t *testing.T) {
	needGitCLI(t)
	e := newEnv(t)
	before := testutil.SnapshotDir(t, e.system())
	out, _, code := e.run("purge", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[purgeDoc](t, out)
	if d.Schema != "oow.purge/v1" || !d.DryRun || !d.Sandbox || d.Summary.Executed {
		t.Errorf("header = %+v", d)
	}
	// 13 selected, 2 for review (recent; dist outside Git), 4 kept (tracked,
	// deployment key, workspace junction, nested repository).
	if len(d.Roots) != 2 || d.Summary.Artifacts != 15 || d.Summary.Selected != 13 || d.Summary.Kept != 4 {
		t.Errorf("roots %d, summary %+v", len(d.Roots), d.Summary)
	}
	for _, p := range d.Projects {
		for _, a := range p.Artifacts {
			testutil.AssertInSandbox(t, a.Path)
			if a.Reasons == nil {
				t.Errorf("%s: reasons is null", a.Path)
			}
		}
	}
	after := testutil.SnapshotDir(t, e.system())
	if len(after) != len(before) {
		t.Fatal("dry run changed the filesystem")
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("dry run changed %s", k)
		}
	}
	text, _, code := e.run("purge", "--dry-run")
	for _, want := range []string{"Project artifacts", "node_modules", "Kept", "contains files tracked by Git", "Reclaimable", "Dry run"} {
		if code != 0 || !strings.Contains(text, want) {
			t.Errorf("text output lacks %q:\n%s", want, text)
		}
	}
}

func TestPurgeRefusesWithoutConfirmation(t *testing.T) {
	e := newEnv(t)
	before := testutil.SnapshotDir(t, e.system())
	if _, _, code := e.run("purge"); code != cli.ExitNeedsConfirm {
		t.Fatalf("code = %d, want %d", code, cli.ExitNeedsConfirm)
	}
	out, _, code := e.run("purge", "--json")
	if code != cli.ExitNeedsConfirm || !strings.Contains(out, `"exit_code": 4`) {
		t.Fatalf("json: code=%d out=%s", code, out)
	}
	if len(testutil.SnapshotDir(t, e.system())) != len(before) {
		t.Fatal("files removed without confirmation")
	}
}

func TestPurgeYesRemovesSelectedAndRecordsHistory(t *testing.T) {
	needGitCLI(t)
	e := newEnv(t)
	out, _, code := e.run("purge", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[purgeDoc](t, out)
	// --yes takes only preselected artifacts, which are deleted, never recycled.
	if !d.Summary.Executed || d.Summary.Errors != 0 || d.Summary.Removed != d.Summary.Selected ||
		d.Summary.ReclaimedBytes != d.Summary.SelectedBytes || d.Summary.Recycled != 0 {
		t.Errorf("summary = %+v", d.Summary)
	}
	for _, gone := range []string{`webapp\node_modules`, `rustapp\target`, `Api\bin`, `pyproj\.venv`, `pyproj\app\__pycache__`} {
		if e.exists(filepath.Join(`C\Users\sandbox\source\repos`, gone)) {
			t.Errorf("%s not removed", gone)
		}
	}
	for _, kept := range []string{`webapp\dist\bundle.js`, `webapp\src\index.js`, `fresh-app\node_modules\lodash\lodash.js`,
		`deploy-site\build\deploy_key.pem`, `handmade\dist\index.html`, `monorepo\packages\ui\src\button.js`,
		`nested-repo\node_modules\private-dep\.git\HEAD`, `gosvc\vendor\example.com\widget\node_modules\x\x.js`} {
		if !e.exists(filepath.Join(`C\Users\sandbox\source\repos`, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `"command": "purge"`) {
		t.Errorf("history lacks the purge run:\n%s", hist)
	}
}

func TestPurgeExplicitFoldersAndRefusals(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("purge", filepath.Join(e.system(), "Windows"), "--dry-run"); code != cli.ExitUsage {
		t.Errorf("purge of the Windows folder: code %d, want %d", code, cli.ExitUsage)
	}
	if _, _, code := e.run("purge", filepath.Join(e.system(), `Users\sandbox\AppData\Roaming`), "--dry-run"); code != cli.ExitUsage {
		t.Errorf("purge of AppData: code %d", code)
	}
	out, _, code := e.run("purge", repos(e, "rustapp"), "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[purgeDoc](t, out)
	if len(d.Roots) != 1 || d.Roots[0].Source != "argument" || len(d.Projects) != 1 ||
		len(d.Projects[0].Artifacts) != 1 || d.Projects[0].Artifacts[0].Kind != "cargo-target" {
		t.Errorf("doc = %+v", d)
	}
}

func TestPurgePathsConfig(t *testing.T) {
	e := newEnv(t)
	gh := filepath.Join(sandbox.DocumentsDir(e.root), "GitHub")
	if _, _, code := e.run("config", "purge", "add", gh); code != 0 {
		t.Fatalf("add: code %d", code)
	}
	if _, _, code := e.run("config", "purge", "add", filepath.Join(e.system(), "Windows")); code != cli.ExitUsage {
		t.Errorf("adding the Windows folder: code %d", code)
	}
	out, _, code := e.run("purge", "--paths", "--json")
	type pathsDoc struct {
		Schema     string   `json:"schema"`
		Configured []string `json:"configured"`
		Roots      []struct {
			Source string `json:"source"`
			Status string `json:"status"`
		} `json:"roots"`
	}
	doc := decode[pathsDoc](t, out)
	if code != 0 || doc.Schema != "oow.purge-paths/v1" || len(doc.Configured) != 1 || len(doc.Roots) != 1 ||
		doc.Roots[0].Source != "config" || doc.Roots[0].Status != "ok" {
		t.Errorf("paths = %+v", doc)
	}
	// Only the configured folder is scanned now.
	pd := decode[purgeDoc](t, mustRun(t, e, "purge", "--dry-run", "--json"))
	if len(pd.Projects) != 1 || !strings.HasSuffix(pd.Projects[0].Path, `\game`) {
		t.Errorf("projects = %+v", pd.Projects)
	}
	if _, _, code := e.run("config", "purge", "remove", gh); code != 0 {
		t.Errorf("remove: code %d", code)
	}
	cfg := mustRun(t, e, "config", "--json")
	if strings.Contains(cfg, "GitHub") {
		t.Errorf("purge path still configured:\n%s", cfg)
	}
}

func TestPurgeHonoursForcedDryRun(t *testing.T) {
	e := newEnv(t)
	t.Setenv("OOW_DRY_RUN", "1")
	before := testutil.SnapshotDir(t, e.system())
	out, _, code := e.run("purge", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if d := decode[purgeDoc](t, out); !d.DryRun || d.Summary.Executed {
		t.Fatalf("OOW_DRY_RUN ignored: %+v", d.Summary)
	}
	if len(testutil.SnapshotDir(t, e.system())) != len(before) {
		t.Fatal("files removed with OOW_DRY_RUN=1")
	}
}

func mustRun(t *testing.T, e *env, args ...string) string {
	t.Helper()
	out, errOut, code := e.run(args...)
	if code != 0 {
		t.Fatalf("%v: code %d\n%s%s", args, code, out, errOut)
	}
	return out
}
