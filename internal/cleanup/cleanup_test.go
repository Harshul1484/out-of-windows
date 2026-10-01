package cleanup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/cleanup"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// world is a seeded sandbox with a matching cleanup environment.
type world struct {
	*testutil.Fixture
	locs safety.Locations
	env  *cleanup.Env
}

func newWorld(t *testing.T, whitelist ...string) *world {
	t.Helper()
	f := testutil.NewFixture(t)
	if err := sandbox.Seed(f.Root); err != nil {
		t.Fatal(err)
	}
	locs := sandbox.Locations(f.Root)
	return &world{
		Fixture: f,
		locs:    locs,
		env: &cleanup.Env{
			Locations: locs,
			Guard:     safety.NewGuard(locs, whitelist),
			Elevated:  true,
		},
	}
}

func (w *world) rel(p string) string {
	r, err := filepath.Rel(w.Root, p)
	if err != nil {
		w.T.Fatal(err)
	}
	return r
}

func scanOne(t *testing.T, env *cleanup.Env, id string) *cleanup.RuleScan {
	t.Helper()
	r := cleanup.FindRule(id)
	if r == nil {
		t.Fatalf("rule %s not found", id)
	}
	res := cleanup.Scan(context.Background(), env, []*cleanup.Rule{r}, nil)
	return res.Rules[0]
}

func paths(items []cleanup.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

func TestBuiltinRulesAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range cleanup.BuiltinRules() {
		if err := r.Validate(); err != nil {
			t.Error(err)
		}
		if seen[r.ID] {
			t.Errorf("duplicate rule ID %s", r.ID)
		}
		seen[r.ID] = true
		for _, root := range r.Roots {
			// %TEMP% comes from the environment and can point anywhere, so
			// rules use the Known Folder based {LocalAppData}\Temp instead.
			if strings.HasPrefix(root, "{Temp}") {
				t.Errorf("rule %s uses {Temp}; use {LocalAppData}\\Temp", r.ID)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no built-in rules")
	}
}

func TestBuiltinRootsAreValidInSimulatedLayout(t *testing.T) {
	w := newWorld(t)
	for _, r := range cleanup.BuiltinRules() {
		for _, tmpl := range r.Roots {
			p, err := w.locs.Expand(strings.ReplaceAll(tmpl, "{profile}", "Default"))
			if err != nil {
				t.Errorf("%s: %v", r.ID, err)
				continue
			}
			if _, err := w.env.Guard.ValidateRoot(p); err != nil {
				t.Errorf("%s: root %s rejected: %v", r.ID, tmpl, err)
			}
		}
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	w := newWorld(t)
	before := w.Snapshot()
	res := cleanup.Scan(context.Background(), w.env, cleanup.BuiltinRules(), &cleanup.Progress{})
	if res.Files() == 0 || res.Bytes() == 0 {
		t.Fatalf("scan found nothing: %+v", res)
	}
	after := w.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("snapshot size changed: %d -> %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed during scan: %q -> %q", k, v, after[k])
		}
	}
}

func TestUserTempScan(t *testing.T) {
	w := newWorld(t)
	rs := scanOne(t, w.env, "temp.user")
	if rs.Status != cleanup.StatusReady {
		t.Fatalf("status = %s (%s)", rs.Status, rs.Reason)
	}
	var got []string
	for _, p := range paths(rs.Files) {
		got = append(got, filepath.Base(p))
	}
	for _, want := range []string{"setup_4f2a.log", "chrome_installer.exe", "payload.bin", "data.cab"} {
		if !contains(got, want) {
			t.Errorf("missing candidate %s in %v", want, got)
		}
	}
	for _, never := range []string{"fresh-download.part", "vscode-ipc.sock.lock", "thesis.docx", "old-notes.txt"} {
		if contains(got, never) {
			t.Errorf("unexpected candidate %s", never)
		}
	}
	if rs.KeptRecent != 2 {
		t.Errorf("KeptRecent = %d, want 2", rs.KeptRecent)
	}
	counts := map[string]int{}
	for _, r := range rs.Skipped.Reasons() {
		counts[r.Reason] = r.Count
	}
	if counts["link or junction (not followed)"] != 1 || counts["sensitive data (keys, credentials, VM disks)"] != 2 || len(counts) != 2 {
		t.Errorf("skipped reasons = %+v, want 1 junction and 2 sensitive files", counts)
	}
	for _, never := range []string{"signing.pfx", "ext4.vhdx"} {
		if contains(got, never) {
			t.Errorf("sensitive file %s selected", never)
		}
	}
}

func TestExecuteRemovesOnlyCandidates(t *testing.T) {
	w := newWorld(t)
	docs := w.locs.UserContent[1]
	docsBefore := testutil.SnapshotDir(t, docs)

	res := cleanup.Scan(context.Background(), w.env, cleanup.BuiltinRules(), nil)
	out := cleanup.Execute(context.Background(), w.env, res.Rules, &cleanup.Progress{})
	if out.Errors != 0 {
		t.Fatalf("errors: %+v", out)
	}
	if out.Reclaimed != res.Bytes() {
		t.Errorf("reclaimed %d, scanned %d", out.Reclaimed, res.Bytes())
	}

	// Junk is gone, including the emptied old folder.
	for _, gone := range []string{
		`C\Users\sandbox\AppData\Local\Temp\setup_4f2a.log`,
		`C\Users\sandbox\AppData\Local\Temp\7zS1A2B.tmp`,
		`C\Windows\Temp\MpCmdRun.log`,
		`C\Users\sandbox\AppData\Local\D3DSCache\6a1b2c\shader.val`,
		`C\Users\sandbox\AppData\Local\Microsoft\Windows\WER\ReportQueue\AppHang_contoso.exe_2\memory.hdmp`,
	} {
		if w.Exists(gone) {
			t.Errorf("%s still exists", gone)
		}
	}
	// Recent temp files, roots, links and everything precious remain.
	for _, kept := range []string{
		`C\Users\sandbox\AppData\Local\Temp`,
		`C\Users\sandbox\AppData\Local\Temp\fresh-download.part`,
		`C\Users\sandbox\AppData\Local\Temp\link-to-documents`,
		`C\Windows\Temp`,
		`C\Users\sandbox\AppData\Local\D3DSCache`,
		`C\Windows\System32\kernel32.dll`,
		`C\Windows\System32\drivers\etc\hosts`,
		`C\Program Files\Contoso\contoso.exe`,
		`C\ProgramData\Contoso\license.dat`,
		`C\Users\sandbox\AppData\Local\Contoso\settings.json`,
		`C\Users\sandbox\AppData\Local\Temp\cert-export\signing.pfx`,
		`C\Users\sandbox\AppData\Local\Temp\wsl-import\ext4.vhdx`,
		`C\Users\sandbox\.ssh\id_ed25519`,
		`C\Users\sandbox\AppData\Roaming\Microsoft\Protect\S-1-5-21-1\masterkey`,
	} {
		if !w.Exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
	docsAfter := testutil.SnapshotDir(t, docs)
	if len(docsBefore) != len(docsAfter) {
		t.Fatalf("Documents changed through the junction: %v -> %v", docsBefore, docsAfter)
	}
}

func TestSecondRunFindsOnlyRecentFiles(t *testing.T) {
	w := newWorld(t)
	res := cleanup.Scan(context.Background(), w.env, cleanup.BuiltinRules(), nil)
	cleanup.Execute(context.Background(), w.env, res.Rules, nil)
	again := cleanup.Scan(context.Background(), w.env, cleanup.BuiltinRules(), nil)
	if again.Files() != 0 {
		t.Errorf("second scan found %d files: %v", again.Files(), again.Rules)
	}
}

func TestAdminRuleSkippedWhenNotElevated(t *testing.T) {
	w := newWorld(t)
	w.env.Elevated = false
	rs := scanOne(t, w.env, "temp.windows")
	if rs.Status != cleanup.StatusSkipped || !strings.Contains(rs.Reason, "administrator") {
		t.Fatalf("status = %s (%s)", rs.Status, rs.Reason)
	}
	if len(rs.Files) != 0 {
		t.Fatal("skipped rule has candidates")
	}
}

func TestWhitelistedRuleSkipped(t *testing.T) {
	w := newWorld(t)
	w.env.Disabled = func(r *cleanup.Rule) bool { return r.MatchesID("windows") }
	if rs := scanOne(t, w.env, "windows.directx-shader-cache"); rs.Status != cleanup.StatusSkipped {
		t.Errorf("status = %s, want skipped", rs.Status)
	}
	if rs := scanOne(t, w.env, "temp.user"); rs.Status != cleanup.StatusReady {
		t.Errorf("unrelated rule status = %s", rs.Status)
	}
}

func TestWhitelistedPathKept(t *testing.T) {
	f := testutil.NewFixture(t)
	if err := sandbox.Seed(f.Root); err != nil {
		t.Fatal(err)
	}
	locs := sandbox.Locations(f.Root)
	keep := filepath.Join(locs.LocalAppData, "Temp", "7zS1A2B.tmp")
	env := &cleanup.Env{Locations: locs, Guard: safety.NewGuard(locs, []string{keep}), Elevated: true}
	rs := scanOne(t, env, "temp.user")
	for _, p := range paths(append(rs.Files, rs.Dirs...)) {
		if safety.IsWithin(safety.MustNormalize(p), safety.MustNormalize(keep)) {
			t.Errorf("whitelisted item selected: %s", p)
		}
	}
	cleanup.Execute(context.Background(), env, []*cleanup.RuleScan{rs}, nil)
	if _, err := os.Stat(filepath.Join(keep, "payload.bin")); err != nil {
		t.Error("whitelisted file removed")
	}
}

func TestLockedFileSkippedOthersRemoved(t *testing.T) {
	w := newWorld(t)
	rs := scanOne(t, w.env, "temp.user")
	locked := filepath.Join(w.locs.LocalAppData, "Temp", "setup_4f2a.log")
	testutil.LockFile(t, locked)
	out := cleanup.Execute(context.Background(), w.env, []*cleanup.RuleScan{rs}, nil)
	if out.Errors != 0 {
		t.Fatalf("errors = %d", out.Errors)
	}
	if out.Rules[0].Skipped.Reasons()[0].Reason != "in use by another program" {
		t.Errorf("reasons = %+v", out.Rules[0].Skipped.Reasons())
	}
	if !w.Exists(w.rel(locked)) {
		t.Error("locked file deleted")
	}
	if w.Exists(`C\Users\sandbox\AppData\Local\Temp\chrome_installer.exe`) {
		t.Error("other files not removed")
	}
}

func TestFileChangedAfterScanIsKept(t *testing.T) {
	w := newWorld(t)
	rs := scanOne(t, w.env, "temp.user")
	p := filepath.Join(w.locs.LocalAppData, "Temp", "setup_4f2a.log")
	if err := os.WriteFile(p, []byte("rewritten by an installer"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := cleanup.Execute(context.Background(), w.env, []*cleanup.RuleScan{rs}, nil)
	if !w.Exists(w.rel(p)) {
		t.Fatal("file modified after scan was deleted")
	}
	if out.Rules[0].Skipped.Reasons()[0].Reason != "changed since it was scanned" {
		t.Errorf("reasons = %+v", out.Rules[0].Skipped.Reasons())
	}
}

func TestJunctionSwapAfterScan(t *testing.T) {
	w := newWorld(t)
	rs := scanOne(t, w.env, "temp.user")
	tmp := filepath.Join(w.locs.LocalAppData, "Temp")
	docs := w.locs.UserContent[1]

	// Replace the scanned folder with a junction to Documents holding files
	// with the same names, sizes and timestamps as the scanned ones.
	scanned := filepath.Join(tmp, "7zS1A2B.tmp")
	for _, it := range rs.Files {
		if !strings.HasPrefix(it.Path, scanned) {
			continue
		}
		rel, _ := filepath.Rel(scanned, it.Path)
		dst := filepath.Join(docs, rel)
		if err := sandbox.WriteFile(dst, int(it.Fingerprint.Size), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := filesystem.SetTimes(dst, it.Fingerprint.CreationTime(), it.Fingerprint.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(scanned, filepath.Join(w.Root, "moved-away")); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.MakeJunction(scanned, docs); err != nil {
		t.Fatal(err)
	}
	before := testutil.SnapshotDir(t, docs)

	cleanup.Execute(context.Background(), w.env, []*cleanup.RuleScan{rs}, nil)

	after := testutil.SnapshotDir(t, docs)
	if len(before) != len(after) {
		t.Fatalf("Documents modified through swapped junction:\nbefore %v\nafter  %v", before, after)
	}
}

func TestRedirectedRootIsNotCleaned(t *testing.T) {
	w := newWorld(t)
	// Turn the D3DSCache root itself into a junction pointing at Documents.
	cache := filepath.Join(w.locs.LocalAppData, "D3DSCache")
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.MakeJunction(cache, w.locs.UserContent[1]); err != nil {
		t.Fatal(err)
	}
	rs := scanOne(t, w.env, "windows.directx-shader-cache")
	if rs.Status != cleanup.StatusReview || len(rs.Files) != 0 {
		t.Fatalf("status = %s (%s), files = %d", rs.Status, rs.Reason, len(rs.Files))
	}
}

func TestMissingRootIsEmpty(t *testing.T) {
	w := newWorld(t)
	if err := os.RemoveAll(filepath.Join(w.locs.LocalAppData, "D3DSCache")); err != nil {
		t.Fatal(err)
	}
	if rs := scanOne(t, w.env, "windows.directx-shader-cache"); rs.Status != cleanup.StatusEmpty {
		t.Fatalf("status = %s (%s)", rs.Status, rs.Reason)
	}
}

func TestCancelledExecuteRemovesNothing(t *testing.T) {
	w := newWorld(t)
	res := cleanup.Scan(context.Background(), w.env, cleanup.BuiltinRules(), nil)
	before := w.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := cleanup.Execute(ctx, w.env, res.Rules, nil)
	if !out.Cancelled || out.Removed != 0 {
		t.Fatalf("cancelled=%v removed=%d", out.Cancelled, out.Removed)
	}
	if len(w.Snapshot()) != len(before) {
		t.Fatal("files removed after cancellation")
	}
}

func TestCancelledScan(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := cleanup.Scan(ctx, w.env, cleanup.BuiltinRules(), nil)
	if !res.Cancelled {
		t.Fatal("scan not marked cancelled")
	}
}

func TestUnreadableFolderInsideRoot(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.locs.LocalAppData, "Temp", "7zS1A2B.tmp", "nested")
	out, err := execIcacls(dir, "/deny", "*S-1-1-0:(RD)")
	if err != nil {
		t.Fatalf("icacls: %v %s", err, out)
	}
	t.Cleanup(func() { _, _ = execIcacls(dir, "/remove:d", "*S-1-1-0") })
	rs := scanOne(t, w.env, "temp.user")
	if rs.Status != cleanup.StatusReady {
		t.Fatalf("status = %s", rs.Status)
	}
	found := false
	for _, r := range rs.Skipped.Reasons() {
		if strings.Contains(r.Reason, "could not read folder") {
			found = true
		}
	}
	if !found {
		t.Errorf("unreadable folder not reported: %+v", rs.Skipped.Reasons())
	}
}

func TestRuleIDMatching(t *testing.T) {
	r := &cleanup.Rule{ID: "browser.chrome.cache"}
	for entry, want := range map[string]bool{
		"browser.chrome.cache": true, "browser.chrome": true, "browser": true, "BROWSER": true,
		"browser.chr": false, "chrome": false, "": false, "browser.chrome.cache.x": false,
	} {
		if got := r.MatchesID(entry); got != want {
			t.Errorf("MatchesID(%q) = %v, want %v", entry, got, want)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
