package cleanup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cleanup"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func (w *world) withRecycleBin() *world {
	w.env.Specials = map[string]cleanup.Special{
		cleanup.SpecialRecycleBin: sandbox.RecycleBin{Dir: sandbox.RecycleBinDir(w.Root)},
	}
	return w
}

// relFiles returns the scanned file paths relative to the sandbox root.
func (w *world) relFiles(rs *cleanup.RuleScan) []string {
	var out []string
	for _, it := range rs.Files {
		out = append(out, w.rel(it.Path))
	}
	return out
}

func hasSuffix(list []string, suffix string) bool {
	for _, s := range list {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

// execute scans one rule and cleans it, failing on unexpected errors.
func (w *world) execute(t *testing.T, id string) *cleanup.RuleScan {
	t.Helper()
	rs := scanOne(t, w.env, id)
	if rs.Status != cleanup.StatusReady {
		t.Fatalf("%s: status = %s (%s)", id, rs.Status, rs.Reason)
	}
	out := cleanup.Execute(context.Background(), w.env, []*cleanup.RuleScan{rs}, nil)
	if out.Errors != 0 {
		t.Fatalf("%s: %d errors", id, out.Errors)
	}
	return rs
}

func (w *world) mustExist(t *testing.T, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if !w.Exists(r) {
			t.Errorf("%s was removed", r)
		}
	}
}

func (w *world) mustBeGone(t *testing.T, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if w.Exists(r) {
			t.Errorf("%s still exists", r)
		}
	}
}

const chromeUD = `C\Users\sandbox\AppData\Local\Google\Chrome\User Data`

func TestChromiumCleansOnlyCacheFoldersOfRealProfiles(t *testing.T) {
	w := newWorld(t)
	docsBefore := len(snapshot(t, w.locs.UserContent[1]))
	rs := w.execute(t, "browser.chrome.cache")
	files := w.relFiles(rs)
	for _, want := range []string{`Default\Cache\Cache_Data\data_1`, `Default\Code Cache\js\4f1e2d_0`,
		`Default\GPUCache\data_0`, `Profile 1\Cache\Cache_Data\f_000001`, `ShaderCache\data_0`, `GrShaderCache\data_1`} {
		if !hasSuffix(files, want) {
			t.Errorf("missing candidate %s", want)
		}
	}
	for _, f := range files {
		if strings.Contains(f, "Crashpad") || strings.Contains(f, "Profile 9") {
			t.Errorf("non-profile or junction path selected: %s", f)
		}
	}
	w.mustExist(t,
		chromeUD+`\Local State`, chromeUD+`\Default\Preferences`, chromeUD+`\Default\Cookies`,
		chromeUD+`\Default\Login Data`, chromeUD+`\Default\History`, chromeUD+`\Default\Bookmarks`,
		chromeUD+`\Default\Local Storage\leveldb\000003.log`,
		chromeUD+`\Default\Service Worker\CacheStorage\a1\index`,
		chromeUD+`\Default\Extensions\abcdef\1.0\manifest.json`,
		chromeUD+`\Crashpad\Cache\notaprofile.bin`,
		chromeUD+`\Default\Cache`, // roots themselves are kept
	)
	w.mustBeGone(t, chromeUD+`\Default\Cache\Cache_Data\data_1`, chromeUD+`\ShaderCache\data_0`)
	if got := len(snapshot(t, w.locs.UserContent[1])); got != docsBefore {
		t.Fatalf("Documents changed through the Profile 9 junction: %d -> %d entries", docsBefore, got)
	}
}

func TestRunningAppIsSkipped(t *testing.T) {
	w := newWorld(t)
	w.env.Running = func() map[string]bool { return map[string]bool{"chrome.exe": true} }
	rs := scanOne(t, w.env, "browser.chrome.cache")
	if rs.Status != cleanup.StatusSkipped || !strings.Contains(rs.Reason, "Google Chrome is running") {
		t.Fatalf("status = %s (%s)", rs.Status, rs.Reason)
	}
	if rs := scanOne(t, w.env, "apps.discord.cache"); rs.Status != cleanup.StatusReady {
		t.Errorf("unrelated app skipped: %s", rs.Reason)
	}
}

func TestFirefoxCacheOnly(t *testing.T) {
	w := newWorld(t)
	w.execute(t, "browser.firefox.cache")
	ff := `C\Users\sandbox\AppData\%s\Mozilla\Firefox\Profiles\k3x9.default-release\`
	local, roaming := strings.Replace(ff, "%s", "Local", 1), strings.Replace(ff, "%s", "Roaming", 1)
	w.mustBeGone(t, local+`cache2\entries\0A1B2C3D`, local+`startupCache\startupCache.8.little`)
	w.mustExist(t, roaming+"logins.json", roaming+"key4.db", roaming+"places.sqlite")
}

func TestElectronAppsKeepStateAndSettings(t *testing.T) {
	w := newWorld(t)
	w.execute(t, "apps.discord.cache")
	w.execute(t, "apps.vscode.cache")
	roam := `C\Users\sandbox\AppData\Roaming\`
	w.mustBeGone(t, roam+`discord\Cache\Cache_Data\f_000042`, roam+`Code\CachedExtensionVSIXs\ms-python.python-2025.1.0`)
	w.mustExist(t,
		roam+`discord\Local Storage\leveldb\000005.ldb`, roam+`discord\settings.json`,
		roam+`Code\User\settings.json`, roam+`Code\User\workspaceStorage\9f8e\state.vscdb`)
}

func TestDeveloperCachesFollowRecoveryContract(t *testing.T) {
	w := newWorld(t)
	for _, id := range []string{"dev.npm.cache", "dev.go.build-cache", "dev.cargo.registry-cache"} {
		w.execute(t, id)
	}
	local, prof := `C\Users\sandbox\AppData\Local\`, `C\Users\sandbox\`
	w.mustBeGone(t, local+`npm-cache\_cacache\content-v2\sha512\ab\cd\ef01`, local+`go-build\3f\3fa1b2-d`,
		prof+`.cargo\registry\cache\index.crates.io-6f17\serde-1.0.200.crate`)
	w.mustExist(t, local+`npm-cache\_npx\8e1f\package.json`,
		prof+`go\pkg\mod\golang.org\x\sys@v0.48.0\go.mod`,
		prof+`.cargo\registry\src\index.crates.io-6f17\serde-1.0.200\Cargo.toml`,
		prof+`.cargo\bin\cargo.exe`)
}

func TestJetBrainsIsOptInAndKeepsLocalHistory(t *testing.T) {
	w := newWorld(t)
	if cleanup.FindRule("apps.jetbrains.caches").DefaultSelected {
		t.Error("JetBrains caches must be opt-in (reindexing is expensive)")
	}
	w.execute(t, "apps.jetbrains.caches")
	jb := `C\Users\sandbox\AppData\Local\JetBrains\`
	w.mustBeGone(t, jb+`IntelliJIdea2025.2\caches\content.dat`, jb+`IntelliJIdea2025.2\index\stubs\stubs.dat`)
	w.mustExist(t, jb+`IntelliJIdea2025.2\LocalHistory\changes.storageData`, jb+`Toolbox\caches\toolbox.cache`)
}

func TestINetCacheKeepsOutlookAttachments(t *testing.T) {
	w := newWorld(t)
	w.execute(t, "windows.inetcache")
	inet := `C\Users\sandbox\AppData\Local\Microsoft\Windows\INetCache\`
	w.mustBeGone(t, inet+`IE\X1Y2Z3\logo[1].png`)
	w.mustExist(t, inet+`Content.Outlook\QWER1234\contract-edited.docx`)
}

func TestRecycleBinSpecial(t *testing.T) {
	w := newWorld(t).withRecycleBin()
	rs := scanOne(t, w.env, "windows.recycle-bin")
	if rs.Status != cleanup.StatusReady || rs.ItemCount() != 3 || rs.Bytes != 2006100 {
		t.Fatalf("scan = %s items=%d bytes=%d (%s)", rs.Status, rs.ItemCount(), rs.Bytes, rs.Reason)
	}
	if cleanup.FindRule("windows.recycle-bin").DefaultSelected {
		t.Error("emptying the Recycle Bin must be opt-in")
	}
	out := cleanup.Execute(context.Background(), w.env, []*cleanup.RuleScan{rs}, nil)
	if out.Removed != 3 || out.Reclaimed != 2006100 || out.Errors != 0 {
		t.Fatalf("outcome = %+v", out)
	}
	sid := `C\$Recycle.Bin\S-1-5-21-sandbox`
	w.mustExist(t, sid)
	entries, _ := os.ReadDir(filepath.Join(w.Root, sid))
	if len(entries) != 0 {
		t.Errorf("Recycle Bin not empty: %d entries", len(entries))
	}
	if again := scanOne(t, w.env, "windows.recycle-bin"); again.Status != cleanup.StatusEmpty {
		t.Errorf("after emptying: %s", again.Status)
	}
}

func TestSpecialWithoutImplementationIsSkipped(t *testing.T) {
	w := newWorld(t)
	if rs := scanOne(t, w.env, "windows.recycle-bin"); rs.Status != cleanup.StatusSkipped {
		t.Fatalf("status = %s", rs.Status)
	}
}

func TestNotInstalledAppsAreQuiet(t *testing.T) {
	w := newWorld(t)
	for _, id := range []string{"apps.slack.cache", "browser.brave.cache", "apps.teams.cache"} {
		if rs := scanOne(t, w.env, id); rs.Status != cleanup.StatusEmpty {
			t.Errorf("%s: status = %s (%s), want empty", id, rs.Status, rs.Reason)
		}
	}
}

func TestRuleSchemaValidation(t *testing.T) {
	base := cleanup.Rule{ID: "x.y", Name: "n", What: "w", WhySafe: "s", Impact: "i"}
	bad := map[string]cleanup.Rule{}
	r := base
	r.Roots = []string{`{LocalAppData}\a\{profile}\Cache`}
	bad["profile without marker"] = r
	r = base
	r.Roots, r.ProfileMarker = []string{`{LocalAppData}\{profile}\a\{profile}\b`}, "m"
	bad["two profiles"] = r
	r = base
	r.Roots, r.ProfileMarker = []string{`{LocalAppData}\a\{profile}`}, "m"
	bad["profile as last component"] = r
	r = base
	r.Roots = []string{`{profile}\Cache`}
	bad["profile as first component"] = r
	r = base
	r.Special, r.Roots = "recycle-bin", []string{`{LocalAppData}\x`}
	bad["special with roots"] = r
	r = base
	r.Roots = []string{`{LocalAppData}`}
	bad["bare location"] = r
	r = base
	r.Roots = []string{`{LocalAppData}\*\Cache`}
	bad["wildcard"] = r
	for name, rule := range bad {
		if err := rule.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	good := base
	good.Roots, good.ProfileMarker = []string{`{LocalAppData}\a\{profile}\Cache`}, "Preferences"
	if err := good.Validate(); err != nil {
		t.Errorf("valid profile rule rejected: %v", err)
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	return testutil.SnapshotDir(t, dir)
}
