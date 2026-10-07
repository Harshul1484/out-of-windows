package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func TestParseAndCompareVersions(t *testing.T) {
	// Ascending precedence, from semver.org §11.
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "v1.0.1", "1.1.0", "2.0.0+build.7"}
	for i := range order {
		a, err := ParseVersion(order[i])
		if err != nil {
			t.Fatalf("%s: %v", order[i], err)
		}
		for j := range order {
			b, _ := ParseVersion(order[j])
			want := cmpInt(i, j)
			if got := a.Compare(b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "01.2.3", "1.2.x", "1.2.3-", "1.2.3-a..b", "1.2.-3"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
	if v, _ := ParseVersion("v2.3.4-rc.1+abc"); v.String() != "2.3.4-rc.1" {
		t.Errorf("String() = %s", v)
	}
}

func TestIsDevBuild(t *testing.T) {
	for v, dev := range map[string]bool{"0.1.0-dev": true, "dev": true, "": true, "1.2.3": false,
		"1.2.3-rc.1": false, "1.2.3-DEV.4": true} {
		if IsDevBuild(v) != dev {
			t.Errorf("IsDevBuild(%q) = %v", v, !dev)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	a, b := strings.Repeat("ab", 32), strings.Repeat("CD", 32)
	sums, err := ParseChecksums([]byte(a + "  oow-1.0.0-windows-amd64.exe\r\n" + b + " *oow-1.0.0-windows-arm64.zip\n\n# comment\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sums["oow-1.0.0-windows-amd64.exe"] != a || sums["oow-1.0.0-windows-arm64.zip"] != strings.ToLower(b) {
		t.Errorf("sums = %v", sums)
	}
	for _, bad := range []string{
		"abc  file.exe",                         // short hash
		strings.Repeat("zz", 32) + "  file.exe", // not hex
		a,                                       // no name
		a + "  f.exe\n" + strings.Repeat("00", 32) + "  f.exe", // conflicting duplicates
	} {
		if _, err := ParseChecksums([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// fakeGitHub serves one release with assets and records requests.
type fakeGitHub struct {
	t        *testing.T
	srv      *httptest.Server
	tag      string
	assets   map[string][]byte
	sums     string // SHA256SUMS content; "" means no checksum file
	noSums   bool
	status   int // non-zero: answer the release query with this status
	authSeen map[string]string
}

func newFakeGitHub(t *testing.T, tag string) *fakeGitHub {
	f := &fakeGitHub{t: t, tag: tag, assets: map[string][]byte{}, authSeen: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) add(name string, data []byte) {
	f.assets[name] = data
	sum := sha256.Sum256(data)
	f.sums += hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.authSeen[r.URL.Path] = r.Header.Get("Authorization")
	switch {
	case r.URL.Path == "/repos/o/r/releases/latest":
		if f.status != 0 {
			if f.status == http.StatusForbidden {
				w.Header().Set("X-RateLimit-Remaining", "0")
			}
			w.WriteHeader(f.status)
			return
		}
		var assets []map[string]any
		names := []string{}
		for n := range f.assets {
			names = append(names, n)
		}
		if !f.noSums {
			names = append(names, ChecksumsName)
		}
		for i, n := range names {
			size := len(f.assets[n])
			if n == ChecksumsName {
				size = len(f.sums)
			}
			assets = append(assets, map[string]any{
				"name": n, "size": size,
				"url":                  fmt.Sprintf("%s/repos/o/r/releases/assets/%d?name=%s", f.srv.URL, i, n),
				"browser_download_url": f.srv.URL + "/download/" + n,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "html_url": f.srv.URL + "/release",
			"published_at": time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), "assets": assets})
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/releases/assets/"):
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "want octet-stream", http.StatusBadRequest)
			return
		}
		f.write(w, r.URL.Query().Get("name"))
	case strings.HasPrefix(r.URL.Path, "/download/"):
		f.write(w, strings.TrimPrefix(r.URL.Path, "/download/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) write(w http.ResponseWriter, name string) {
	if name == ChecksumsName {
		_, _ = w.Write([]byte(f.sums))
		return
	}
	data, ok := f.assets[name]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	_, _ = w.Write(data)
}

func (f *fakeGitHub) source(token string) Source {
	return Source{APIBase: f.srv.URL, Repo: "o/r", Token: token}
}

func TestLatestAndPlanSelectsArchitecture(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	gh.add("oow-1.4.0-windows-amd64.exe", []byte("amd64 build"))
	gh.add("oow-1.4.0-windows-arm64.exe", []byte("arm64 build"))
	gh.add("oow-1.4.0-windows-amd64.zip", []byte("zip"))
	src := gh.source("")
	rel, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := rel.Version(); v.String() != "1.4.0" {
		t.Fatalf("version = %s", v)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		p, err := src.PlanFor(context.Background(), rel, "oow", arch)
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		sum := sha256.Sum256([]byte(arch + " build"))
		if p.Asset.Name != "oow-1.4.0-windows-"+arch+".exe" || p.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: plan = %+v", arch, p)
		}
	}
	if _, err := src.PlanFor(context.Background(), rel, "oow", "386"); !errors.Is(err, ErrNoAsset) {
		t.Errorf("386: err = %v, want ErrNoAsset", err)
	}
}

func TestPlanFailsClosedWithoutChecksums(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	gh.add("oow-1.4.0-windows-amd64.exe", []byte("amd64 build"))
	gh.noSums = true
	rel, err := gh.source("").Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gh.source("").PlanFor(context.Background(), rel, "oow", "amd64"); !errors.Is(err, ErrChecksumMissing) {
		t.Errorf("no SHA256SUMS: err = %v", err)
	}

	gh.noSums = false
	gh.sums = strings.Repeat("ab", 32) + "  some-other-file.exe\n"
	rel, _ = gh.source("").Latest(context.Background())
	if _, err := gh.source("").PlanFor(context.Background(), rel, "oow", "amd64"); !errors.Is(err, ErrChecksumMissing) {
		t.Errorf("no entry: err = %v", err)
	}
}

func TestReleaseQueryErrorsNameTheCause(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	for status, want := range map[int]string{
		http.StatusNotFound:     "GITHUB_TOKEN or GH_TOKEN",
		http.StatusForbidden:    "rate limit",
		http.StatusUnauthorized: "rejected the token",
	} {
		gh.status = status
		_, err := gh.source("").Latest(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: err = %v, want mention of %q", status, err, want)
		}
	}
	gh.status = 0
	gh.tag = "nightly"
	if _, err := gh.source("").Latest(context.Background()); err == nil {
		t.Error("accepted a release whose tag is not a version")
	}
}

func TestTokenSentOnlyToTheAPI(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	gh.add("oow-1.4.0-windows-amd64.exe", []byte("amd64 build"))
	src := gh.source("secret-token")
	rel, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, err := src.PlanFor(context.Background(), rel, "oow", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	var sink strings.Builder
	if err := src.Download(context.Background(), p, &sink); err != nil {
		t.Fatal(err)
	}
	if gh.authSeen["/repos/o/r/releases/latest"] != "Bearer secret-token" {
		t.Error("token not sent to the API")
	}
	// With a token, assets come from the API endpoint (private repositories).
	if _, used := gh.authSeen["/download/oow-1.4.0-windows-amd64.exe"]; used {
		t.Error("public download URL used although a token is set")
	}

	// Without a token, the public URL is used and no Authorization is sent.
	gh.authSeen = map[string]string{}
	src = gh.source("")
	if err := src.Download(context.Background(), p, &sink); err != nil {
		t.Fatal(err)
	}
	for path, auth := range gh.authSeen {
		if auth != "" {
			t.Errorf("%s received Authorization without a token", path)
		}
	}
}

func TestDownloadRejectsMismatchAndWrongSize(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	gh.add("oow-1.4.0-windows-amd64.exe", []byte("amd64 build"))
	src := gh.source("")
	rel, _ := src.Latest(context.Background())
	p, err := src.PlanFor(context.Background(), rel, "oow", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	gh.assets["oow-1.4.0-windows-amd64.exe"] = []byte("AMD64 BUILD") // same size, other bytes
	var sink strings.Builder
	if err := src.Download(context.Background(), p, &sink); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("tampered asset: err = %v", err)
	}
	gh.assets["oow-1.4.0-windows-amd64.exe"] = []byte("amd64 build plus more")
	if err := src.Download(context.Background(), p, &sink); err == nil {
		t.Error("oversized asset accepted")
	}
}

// fakeInstall creates a fake installed executable inside the test sandbox.
func fakeInstall(t *testing.T, content string) string {
	t.Helper()
	exe := filepath.Join(testutil.Dir(t), "Programs", "oow", "oow.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStageAndReplace(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	gh.add("oow-1.4.0-windows-amd64.exe", []byte("new version"))
	src := gh.source("")
	rel, _ := src.Latest(context.Background())
	p, err := src.PlanFor(context.Background(), rel, "oow", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	exe := fakeInstall(t, "old version")
	staged, err := Stage(context.Background(), src, p, exe)
	if err != nil {
		t.Fatal(err)
	}
	if staged != StagedPath(exe) || read(t, staged) != "new version" || read(t, exe) != "old version" {
		t.Fatal("staging changed the installed executable or wrote elsewhere")
	}
	if err := Replace(exe, staged); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "new version" || read(t, OldPath(exe)) != "old version" {
		t.Fatal("replace did not swap the files")
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("staged file left behind")
	}
	// The next start removes the previous version.
	if err := CleanupOld(exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(OldPath(exe)); !os.IsNotExist(err) {
		t.Error(".old not removed")
	}
	if err := CleanupOld(exe); err != nil {
		t.Errorf("second cleanup: %v", err)
	}
}

func TestStageRemovesARejectedDownload(t *testing.T) {
	gh := newFakeGitHub(t, "v1.4.0")
	gh.add("oow-1.4.0-windows-amd64.exe", []byte("new version"))
	src := gh.source("")
	rel, _ := src.Latest(context.Background())
	p, _ := src.PlanFor(context.Background(), rel, "oow", "amd64")
	gh.assets["oow-1.4.0-windows-amd64.exe"] = []byte("evil versio")
	exe := fakeInstall(t, "old version")
	before := testutil.SnapshotDir(t, filepath.Dir(exe))
	if _, err := Stage(context.Background(), src, p, exe); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	after := testutil.SnapshotDir(t, filepath.Dir(exe))
	if len(after) != len(before) || after["oow.exe"] != before["oow.exe"] {
		t.Errorf("rejected download changed the folder: %v -> %v", before, after)
	}
}

func TestReplaceRollsBackWhenTheNewFileCannotMoveIn(t *testing.T) {
	exe := fakeInstall(t, "old version")
	staged := StagedPath(exe)
	if err := os.WriteFile(staged, []byte("new version"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := rename
	t.Cleanup(func() { rename = real })
	rename = func(from, to string) error {
		if from == staged {
			return errors.New("simulated failure")
		}
		return real(from, to)
	}
	err := Replace(exe, staged)
	if err == nil || !strings.Contains(err.Error(), "previous version was restored") {
		t.Fatalf("err = %v", err)
	}
	if read(t, exe) != "old version" {
		t.Error("previous version not restored")
	}
	if _, err := os.Stat(OldPath(exe)); !os.IsNotExist(err) {
		t.Error(".old left after rollback")
	}

	// When even the rollback fails, the message says how to recover.
	rename = func(from, to string) error {
		if from == staged || from == OldPath(exe) {
			return errors.New("simulated failure")
		}
		return real(from, to)
	}
	err = Replace(exe, staged)
	if err == nil || !strings.Contains(err.Error(), "rename "+OldPath(exe)+" to "+exe) {
		t.Fatalf("err = %v", err)
	}
	if read(t, OldPath(exe)) != "old version" {
		t.Error("previous version lost")
	}
}

func TestReplaceNeverOverwrites(t *testing.T) {
	exe := fakeInstall(t, "old version")
	// A directory where the backup should go cannot be removed by the
	// cleanup (not a plain file), so nothing is moved.
	if err := os.Mkdir(OldPath(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StagedPath(exe), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, StagedPath(exe)); err == nil {
		t.Fatal("replace went ahead with a folder in the way")
	}
	if read(t, exe) != "old version" {
		t.Error("executable changed")
	}
}

// GitHub answers asset requests with a redirect to a storage host. The token
// must not follow the redirect there.
func TestTokenNotForwardedOnRedirect(t *testing.T) {
	var storageAuth []string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageAuth = append(storageAuth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("payload"))
	}))
	t.Cleanup(storage.Close)
	// Another host name for the same listener: localhost vs 127.0.0.1.
	storageURL := strings.Replace(storage.URL, "127.0.0.1", "localhost", 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storageURL+"/blob", http.StatusFound)
	}))
	t.Cleanup(api.Close)

	sum := sha256.Sum256([]byte("payload"))
	p := &Plan{Asset: Asset{Name: "x.exe", Size: 7, APIURL: api.URL + "/repos/o/r/releases/assets/1"},
		SHA256: hex.EncodeToString(sum[:])}
	src := Source{APIBase: api.URL, Repo: "o/r", Token: "secret-token"}
	var sink strings.Builder
	if err := src.Download(context.Background(), p, &sink); err != nil {
		t.Fatal(err)
	}
	if len(storageAuth) != 1 || storageAuth[0] != "" {
		t.Errorf("storage host saw Authorization %q", storageAuth)
	}
}
