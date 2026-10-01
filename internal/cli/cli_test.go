package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// env is a seeded sandbox the CLI runs against via OOW_SANDBOX.
type env struct {
	t    *testing.T
	root string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := testutil.Dir(t)
	if err := sandbox.Seed(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sandbox.EnvVar, root)
	t.Setenv("NO_COLOR", "1")
	return &env{t: t, root: root}
}

func (e *env) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code = cli.Run(context.Background(), args, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func (e *env) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(e.root, rel))
	return err == nil
}

type cleanResult struct {
	Schema  string `json:"schema"`
	DryRun  bool   `json:"dry_run"`
	Sandbox bool   `json:"sandbox"`
	Rules   []struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Reason   string `json:"reason"`
		Selected bool   `json:"selected"`
		Files    int    `json:"files"`
		Bytes    int64  `json:"bytes"`
		Items    []struct {
			Path string `json:"path"`
		} `json:"items"`
		Result *struct {
			Removed int `json:"removed"`
		} `json:"result"`
	} `json:"rules"`
	Summary struct {
		ReclaimableFiles int   `json:"reclaimable_files"`
		ReclaimableBytes int64 `json:"reclaimable_bytes"`
		SelectedBytes    int64 `json:"selected_bytes"`
		Executed         bool  `json:"executed"`
		Removed          int   `json:"removed"`
		ReclaimedBytes   int64 `json:"reclaimed_bytes"`
		Errors           int   `json:"errors"`
	} `json:"summary"`
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, s)
	}
	return v
}

func TestVersion(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("--version")
	if code != 0 || !strings.HasPrefix(out, "oow ") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHelpListsCommands(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("--help")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, c := range []string{"clean", "uninstall", "analyze", "optimize", "status", "installer",
		"purge", "startup", "doctor", "history", "config", "update", "remove"} {
		if !strings.Contains(out, "  "+c+" ") {
			t.Errorf("help does not list %s", c)
		}
	}
	if strings.Contains(out, "sandbox") {
		t.Error("hidden sandbox command shown in help")
	}
}

func TestNoArgsNonInteractivePrintsHelp(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run()
	if code != 0 || !strings.Contains(out, "Usage:") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCleanDryRunJSONChangesNothing(t *testing.T) {
	e := newEnv(t)
	before := testutil.SnapshotDir(t, e.system())
	out, _, code := e.run("clean", "--dry-run", "--json", "--details")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	r := decode[cleanResult](t, out)
	if r.Schema != "oow.clean/v1" || !r.DryRun || !r.Sandbox || r.Summary.Executed {
		t.Errorf("header = %+v", r)
	}
	if r.Summary.ReclaimableFiles < 30 {
		t.Errorf("reclaimable files = %d, want at least 30", r.Summary.ReclaimableFiles)
	}
	for _, rule := range r.Rules {
		for _, it := range rule.Items {
			testutil.AssertInSandbox(t, it.Path)
			if strings.Contains(it.Path, `\Documents\`) || strings.Contains(it.Path, `\System32\`) {
				t.Errorf("dangerous candidate %s", it.Path)
			}
		}
	}
	after := testutil.SnapshotDir(t, e.system())
	if len(before) != len(after) {
		t.Fatal("dry run changed the filesystem")
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("dry run changed %s", k)
		}
	}
}

func TestCleanRefusesWithoutConfirmation(t *testing.T) {
	e := newEnv(t)
	before := testutil.SnapshotDir(t, e.system())
	_, _, code := e.run("clean")
	if code != cli.ExitNeedsConfirm {
		t.Fatalf("code = %d, want %d", code, cli.ExitNeedsConfirm)
	}
	out, _, code := e.run("clean", "--json")
	if code != cli.ExitNeedsConfirm || !strings.Contains(out, `"exit_code": 4`) {
		t.Fatalf("json: code=%d out=%s", code, out)
	}
	if len(testutil.SnapshotDir(t, e.system())) != len(before) {
		t.Fatal("files removed without confirmation")
	}
}

func TestCleanYesRemovesJunkAndRecordsHistory(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("clean", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	r := decode[cleanResult](t, out)
	if !r.Summary.Executed || r.Summary.Errors != 0 || r.Summary.ReclaimedBytes != r.Summary.SelectedBytes {
		t.Errorf("summary = %+v", r.Summary)
	}
	for _, gone := range []string{
		`C\Users\sandbox\AppData\Local\Temp\chrome_installer.exe`,
		`C\Windows\Temp\MpCmdRun.log`,
	} {
		if e.exists(gone) {
			t.Errorf("%s not removed", gone)
		}
	}
	for _, kept := range []string{
		`C\Windows\System32\kernel32.dll`,
		`C\Users\sandbox\Documents\thesis.docx`,
		`C\Users\sandbox\AppData\Local\Temp\fresh-download.part`,
		`C\Users\sandbox\AppData\Local\Temp\link-to-documents`,
		`C\Program Files\Contoso\contoso.exe`,
	} {
		if !e.exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}

	hist, _, code := e.run("history", "--json")
	if code != 0 {
		t.Fatalf("history code = %d", code)
	}
	h := decode[struct {
		Records []struct {
			Command   string `json:"command"`
			Reclaimed int64  `json:"reclaimed_bytes"`
			Sandbox   bool   `json:"sandbox"`
		} `json:"records"`
	}](t, hist)
	if len(h.Records) != 1 || h.Records[0].Command != "clean" || !h.Records[0].Sandbox ||
		h.Records[0].Reclaimed != r.Summary.ReclaimedBytes {
		t.Errorf("history = %+v", h.Records)
	}
}

func TestOptInTargetsNeedExplicitSelection(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("clean", "--yes", "--json"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, kept := range []string{
		`C\$Recycle.Bin\S-1-5-21-sandbox\$RDEF456\old-photo.jpg`,
		`C\Users\sandbox\AppData\Local\JetBrains\IntelliJIdea2025.2\caches\content.dat`,
	} {
		if !e.exists(kept) {
			t.Errorf("opt-in target cleaned by default: %s", kept)
		}
	}
	// Chrome cache (default-on) is gone; profile data is kept.
	if e.exists(`C\Users\sandbox\AppData\Local\Google\Chrome\User Data\Default\Cache\Cache_Data\data_1`) {
		t.Error("Chrome cache not cleaned")
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Google\Chrome\User Data\Default\Login Data`) {
		t.Error("Chrome Login Data removed")
	}

	out, _, code := e.run("clean", "--rule", "windows.recycle-bin", "--yes", "--json")
	if code != 0 {
		t.Fatalf("recycle bin: code = %d", code)
	}
	r := decode[cleanResult](t, out)
	if r.Summary.Removed != 3 || r.Summary.ReclaimedBytes != 2006100 {
		t.Errorf("recycle bin summary = %+v", r.Summary)
	}
	if e.exists(`C\$Recycle.Bin\S-1-5-21-sandbox\$RDEF456\old-photo.jpg`) {
		t.Error("Recycle Bin not emptied")
	}
}

func TestDryRunEnvForcesPreview(t *testing.T) {
	e := newEnv(t)
	t.Setenv("OOW_DRY_RUN", "1")
	before := testutil.SnapshotDir(t, e.system())
	out, _, code := e.run("clean", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	r := decode[cleanResult](t, out)
	if !r.DryRun || r.Summary.Executed {
		t.Fatalf("OOW_DRY_RUN ignored: %+v", r.Summary)
	}
	if len(testutil.SnapshotDir(t, e.system())) != len(before) {
		t.Fatal("files removed with OOW_DRY_RUN=1")
	}
}

func TestCleanRuleFilter(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("clean", "--rule", "windows.directx-shader-cache", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	r := decode[cleanResult](t, out)
	if len(r.Rules) != 1 || r.Rules[0].ID != "windows.directx-shader-cache" {
		t.Fatalf("rules = %+v", r.Rules)
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Temp\chrome_installer.exe`) {
		t.Error("filter ignored: temp file removed")
	}
	if _, _, code := e.run("clean", "--rule", "no.such.rule", "--dry-run"); code != cli.ExitUsage {
		t.Errorf("unknown rule: code = %d, want %d", code, cli.ExitUsage)
	}
}

func TestWhitelistRuleAndPath(t *testing.T) {
	e := newEnv(t)
	keep := filepath.Join(e.root, `C\Users\sandbox\AppData\Local\Temp\7zS1A2B.tmp`)
	if _, _, code := e.run("config", "whitelist", "add", "logs", keep); code != 0 {
		t.Fatalf("whitelist add: code = %d", code)
	}
	wl, _, _ := e.run("config", "whitelist", "--json")
	if !strings.Contains(wl, `"logs"`) || !strings.Contains(wl, "7zS1A2B.tmp") {
		t.Fatalf("whitelist = %s", wl)
	}
	out, _, code := e.run("clean", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	r := decode[cleanResult](t, out)
	for _, rule := range r.Rules {
		if rule.ID == "logs.windows-error-reports" && (rule.Status != "skipped" || !strings.Contains(rule.Reason, "whitelist")) {
			t.Errorf("whitelisted rule: %+v", rule)
		}
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Temp\7zS1A2B.tmp\payload.bin`) {
		t.Error("whitelisted path cleaned")
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Microsoft\Windows\WER\ReportArchive\AppCrash_contoso.exe_1\Report.wer`) {
		t.Error("whitelisted rule cleaned")
	}
	if _, _, code := e.run("config", "whitelist", "remove", "logs"); code != 0 {
		t.Errorf("remove: code = %d", code)
	}
	if _, _, code := e.run("config", "whitelist", "add", `relative\dir`); code != cli.ExitUsage {
		t.Errorf("relative path: code = %d", code)
	}
}

func TestMalformedConfigBlocksClean(t *testing.T) {
	e := newEnv(t)
	cfg := filepath.Join(sandbox.DataDir(e.root), "config.json")
	if err := os.WriteFile(cfg, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := e.run("clean", "--yes"); code != cli.ExitError {
		t.Fatalf("code = %d, want %d", code, cli.ExitError)
	}
	if !e.exists(`C\Users\sandbox\AppData\Local\Temp\chrome_installer.exe`) {
		t.Fatal("cleaned despite unreadable whitelist")
	}
	// Read-only commands still work.
	if _, _, code := e.run("config", "path"); code != 0 {
		t.Errorf("config path: code = %d", code)
	}
}

func TestPlannedCommandsExitCode(t *testing.T) {
	e := newEnv(t)
	for _, c := range []string{"status", "doctor", "purge", "installer"} {
		_, errOut, code := e.run(c, "--some-flag")
		if code != cli.ExitNotImplemented || !strings.Contains(errOut, "planned for Phase") {
			t.Errorf("%s: code=%d err=%q", c, code, errOut)
		}
	}
}

func TestTextOutputHasNoColorWithNoColor(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("clean", "--dry-run")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI escapes in NO_COLOR output")
	}
	for _, want := range []string{"Dry run: nothing was changed", "User temporary files", "Reclaimable", "SANDBOX"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestListRulesAndProtected(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("clean", "--list", "--json")
	if code != 0 || !strings.Contains(out, `"temp.user"`) || !strings.Contains(out, `"why_safe"`) {
		t.Fatalf("list: code=%d out=%s", code, out)
	}
	out, _, code = e.run("config", "protected", "--json")
	if code != 0 || !strings.Contains(out, "System32") && !strings.Contains(out, `C\\Windows`) {
		t.Fatalf("protected: code=%d", code)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("frobnicate"); code != cli.ExitUsage {
		t.Errorf("code = %d, want %d", code, cli.ExitUsage)
	}
}

// system is the simulated C:\ tree (excluding the tool's own oow-data dir,
// where logs are written).
func (e *env) system() string { return filepath.Join(e.root, "C") }
