package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// releaseServer is a fake GitHub API with one latest release.
type releaseServer struct {
	tag    string
	assets map[string][]byte
	sums   map[string]string // name -> hex; nil means no SHA256SUMS asset
}

func (rs *releaseServer) handler(srvURL *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+buildinfo.Repo+"/releases/latest":
			var list []map[string]any
			add := func(name string, size int) {
				list = append(list, map[string]any{"name": name, "size": size,
					"url": *srvURL + "/asset/" + name, "browser_download_url": *srvURL + "/asset/" + name})
			}
			for n, b := range rs.assets {
				add(n, len(b))
			}
			if rs.sums != nil {
				add("SHA256SUMS", len(rs.sumsFile()))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": rs.tag,
				"html_url": "https://example.invalid/releases/" + rs.tag, "assets": list})
		case r.URL.Path == "/asset/SHA256SUMS" && rs.sums != nil:
			_, _ = w.Write([]byte(rs.sumsFile()))
		case strings.HasPrefix(r.URL.Path, "/asset/"):
			b, ok := rs.assets[strings.TrimPrefix(r.URL.Path, "/asset/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}
}

func (rs *releaseServer) sumsFile() string {
	var b strings.Builder
	for n, s := range rs.sums {
		fmt.Fprintf(&b, "%s  %s\n", s, n)
	}
	return b.String()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// newUpdateEnv is a sandbox with a simulated installed executable, a fake
// release server publishing version tag, and the running version current.
func newUpdateEnv(t *testing.T, current, tag string) (*env, *releaseServer) {
	t.Helper()
	e := newEnv(t)
	if err := sandbox.SeedInstall(e.root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sandbox.SelfExe(e.root), []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs := &releaseServer{tag: tag, assets: map[string][]byte{}, sums: map[string]string{}}
	v := strings.TrimPrefix(tag, "v")
	for _, arch := range []string{"amd64", "arm64"} {
		name := fmt.Sprintf("oow-%s-windows-%s.exe", v, arch)
		rs.assets[name] = []byte("new build for " + arch)
		rs.sums[name] = sum(rs.assets[name])
	}
	var url string
	srv := httptest.NewServer(rs.handler(&url))
	url = srv.URL
	t.Cleanup(srv.Close)
	t.Cleanup(cli.SetUpdateAPIBase(srv.URL))
	old := buildinfo.Version
	buildinfo.Version = current
	t.Cleanup(func() { buildinfo.Version = old })
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	return e, rs
}

type updateDoc struct {
	Schema          string `json:"schema"`
	CheckOnly       bool   `json:"check_only"`
	DryRun          bool   `json:"dry_run"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	DevBuild        bool   `json:"dev_build"`
	ManagedBy       *struct {
		Manager       string `json:"manager"`
		UpdateCommand string `json:"update_command"`
	} `json:"managed_by"`
	Asset *struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"asset"`
	Executable string `json:"executable"`
	Updated    bool   `json:"updated"`
	Message    string `json:"message"`
	Error      string `json:"error"`
}

func (e *env) exe() string { return sandbox.SelfExe(e.root) }

func (e *env) read(p string) string {
	e.t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func TestUpdateCheckComparesVersions(t *testing.T) {
	for _, c := range []struct {
		current, tag string
		available    bool
		msg          string
	}{
		{"1.0.0", "v1.1.0", true, "1.1.0 is available"},
		{"1.0.0", "v1.0.0", false, "is up to date"},
		{"1.2.0", "v1.1.0", false, "newer than the latest release"},
		{"1.0.0-rc.1", "v1.0.0", true, "1.0.0 is available"},
	} {
		e, _ := newUpdateEnv(t, c.current, c.tag)
		out, _, code := e.run("update", "--check", "--json")
		if code != 0 {
			t.Fatalf("%s vs %s: code = %d\n%s", c.current, c.tag, code, out)
		}
		d := decode[updateDoc](t, out)
		if d.Schema != "oow.update/v1" || !d.CheckOnly || d.UpdateAvailable != c.available ||
			d.CurrentVersion != c.current || !strings.Contains(d.Message, c.msg) {
			t.Errorf("%s vs %s: %+v", c.current, c.tag, d)
		}
		if e.read(e.exe()) != "old build" {
			t.Error("--check changed the executable")
		}
	}
}

func TestUpdateDryRunChangesNothing(t *testing.T) {
	e, rs := newUpdateEnv(t, "1.0.0", "v1.1.0")
	before := testutil.SnapshotDir(t, e.root)
	out, _, code := e.run("update", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[updateDoc](t, out)
	want := fmt.Sprintf("oow-1.1.0-windows-%s.exe", runtime.GOARCH)
	if !d.DryRun || d.Updated || d.Asset == nil || d.Asset.Name != want || d.Asset.SHA256 != rs.sums[want] {
		t.Fatalf("doc = %+v", d)
	}
	after := testutil.SnapshotDir(t, e.root)
	for k, v := range before {
		if after[k] != v && !strings.HasPrefix(k, `oow-data\`) {
			t.Errorf("dry run changed %s", k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok && !strings.HasPrefix(k, `oow-data\`) {
			t.Errorf("dry run created %s", k)
		}
	}
}

func TestUpdateNeedsConfirmation(t *testing.T) {
	e, _ := newUpdateEnv(t, "1.0.0", "v1.1.0")
	_, errOut, code := e.run("update")
	if code != cli.ExitNeedsConfirm || !strings.Contains(errOut, "--yes") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if e.read(e.exe()) != "old build" {
		t.Fatal("updated without confirmation")
	}
}

func TestUpdateReplacesAndCleansUpOnNextStart(t *testing.T) {
	e, _ := newUpdateEnv(t, "1.0.0", "v1.1.0")
	out, _, code := e.run("update", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	if d := decode[updateDoc](t, out); !d.Updated || d.Error != "" {
		t.Fatalf("doc = %+v", d)
	}
	if e.read(e.exe()) != "new build for "+runtime.GOARCH {
		t.Fatal("executable not replaced with the build for this architecture")
	}
	if e.read(e.exe()+".old") != "old build" {
		t.Fatal("previous version not kept")
	}
	if _, err := os.Stat(e.exe() + ".new"); !os.IsNotExist(err) {
		t.Error("staged download left behind")
	}
	// The next start removes the previous version.
	if _, _, code := e.run("history", "--json"); code != 0 {
		t.Fatal("history failed")
	}
	if _, err := os.Stat(e.exe() + ".old"); !os.IsNotExist(err) {
		t.Error(".old not removed on the next start")
	}
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `"command": "update"`) {
		t.Errorf("update not recorded:\n%s", hist)
	}
}

func TestUpdateFailsClosed(t *testing.T) {
	for name, tamper := range map[string]func(rs *releaseServer){
		"checksum mismatch": func(rs *releaseServer) {
			for n := range rs.assets {
				rs.assets[n] = []byte(strings.ToUpper(string(rs.assets[n])))
			}
		},
		"no SHA256SUMS":   func(rs *releaseServer) { rs.sums = nil },
		"no entry":        func(rs *releaseServer) { rs.sums = map[string]string{"other.exe": sum([]byte("x"))} },
		"no build for me": func(rs *releaseServer) { rs.assets = map[string][]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			e, rs := newUpdateEnv(t, "1.0.0", "v1.1.0")
			tamper(rs)
			before := testutil.SnapshotDir(t, filepath.Dir(e.exe()))
			out, _, code := e.run("update", "--yes", "--json")
			if code != cli.ExitError {
				t.Fatalf("code = %d\n%s", code, out)
			}
			if d := decode[updateDoc](t, out); d.Updated || d.Error == "" {
				t.Errorf("doc = %+v", d)
			}
			after := testutil.SnapshotDir(t, filepath.Dir(e.exe()))
			if len(after) != len(before) || e.read(e.exe()) != "old build" {
				t.Errorf("install folder changed: %v -> %v", before, after)
			}
		})
	}
}

func TestUpdateDevBuildDoesNotUpdateItself(t *testing.T) {
	e, _ := newUpdateEnv(t, "0.1.0-dev", "v1.1.0")
	_, errOut, code := e.run("update", "--yes")
	if code != cli.ExitError || !strings.Contains(errOut, "development build") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	out, _, code := e.run("update", "--check", "--json")
	d := decode[updateDoc](t, out)
	if code != 0 || !d.DevBuild || d.LatestVersion != "1.1.0" || d.UpdateAvailable {
		t.Errorf("check: code = %d, doc = %+v", code, d)
	}
	if e.read(e.exe()) != "old build" {
		t.Error("dev build replaced itself")
	}
}

func TestUpdatePointsToThePackageManager(t *testing.T) {
	e, _ := newUpdateEnv(t, "1.0.0", "v1.1.0")
	exe := filepath.Join(e.root, `C\Users\sandbox\scoop\apps\oow\current\oow.exe`)
	if err := sandbox.WriteFile(exe, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cli.SetSelfExe(exe))
	out, _, code := e.run("update", "--yes", "--json")
	d := decode[updateDoc](t, out)
	if code != cli.ExitError || d.ManagedBy == nil || d.ManagedBy.Manager != "scoop" ||
		!strings.Contains(d.Error, "scoop update oow") {
		t.Fatalf("code = %d, doc = %+v", code, d)
	}
	out, _, code = e.run("update", "--check", "--json")
	if d := decode[updateDoc](t, out); code != 0 || !strings.Contains(d.Message, "scoop update oow") {
		t.Errorf("check: code = %d, doc = %+v", code, d)
	}
}

func TestUpdateTextOutput(t *testing.T) {
	e, _ := newUpdateEnv(t, "1.0.0", "v1.1.0")
	out, _, code := e.run("update", "--dry-run")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"Update", "DRY RUN", "SANDBOX", "Installed", "1.0.0", "Latest", "1.1.0",
		"SHA-256", "Dry run: nothing was downloaded or changed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI escapes with NO_COLOR")
	}
}
