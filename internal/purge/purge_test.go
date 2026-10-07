package purge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type world struct {
	t    *testing.T
	root string
	locs safety.Locations
	env  *Env
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := testutil.Dir(t)
	if err := sandbox.Seed(root); err != nil {
		t.Fatal(err)
	}
	l := sandbox.Locations(root)
	return &world{t: t, root: root, locs: l, env: &Env{Guard: safety.NewGuard(l, nil), Locations: l, Ceiling: root}}
}

func (w *world) repos(rel string) string { return filepath.Join(sandbox.ProjectsDir(w.root), rel) }

// bin is the sandbox's simulated Recycle Bin.
func (w *world) bin() filesystem.Recycler { return sandbox.Recycler{Root: w.root} }

func (w *world) binEntries() int {
	entries, _ := os.ReadDir(filepath.Join(sandbox.RecycleBinDir(w.root), "S-1-5-21-sandbox"))
	return len(entries)
}

type noRecycleBin struct{}

func (noRecycleBin) Recycle(string) error { return filesystem.ErrNoRecycleBin }

func TestReviewArtifactsGoToTheRecycleBin(t *testing.T) {
	w := newWorld(t)
	res := w.find()
	got := w.byPath(res)
	review, ready := got[`handmade\dist`], got[`rustapp\target`]
	if review == nil || review.Status != StatusReview || ready == nil || ready.Status != StatusReady {
		t.Fatalf("fixtures: review %+v, ready %+v", review, ready)
	}
	binBefore := w.binEntries()
	out := Remove(context.Background(), w.env, res, []*Artifact{review, ready}, w.bin(), nil)
	if out.Errors != 0 || out.Removed != 1 || out.Recycled != 1 || out.RecycledBytes != review.Bytes {
		t.Fatalf("outcome: removed %d, recycled %d (%d bytes), errors %d", out.Removed, out.Recycled, out.RecycledBytes, out.Errors)
	}
	if review.Result.Method != MethodRecycled || review.Result.RemovedFiles != 0 || review.Result.RecycledFiles != 1 {
		t.Errorf("review result %+v", review.Result)
	}
	if ready.Result.Method != MethodDeleted || !ready.Result.Complete || ready.Result.RecycledFiles != 0 {
		t.Errorf("ready result %+v", ready.Result)
	}
	for _, p := range []string{review.Path, ready.Path} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s still in place", p)
		}
	}
	// The review folder is in the (simulated) Recycle Bin as one entry, with
	// its file; the ready one was deleted, not recycled.
	if w.binEntries() != binBefore+1 {
		t.Fatalf("Recycle Bin entries %d, want %d", w.binEntries(), binBefore+1)
	}
	found := false
	_ = filepath.WalkDir(sandbox.RecycleBinDir(w.root), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "index.html" {
			found = true
		}
		if err == nil && d.Name() == "rustapp.exe" {
			t.Errorf("deleted artifact found in the Recycle Bin: %s", p)
		}
		return nil
	})
	if !found {
		t.Error("recycled dist content not found in the Recycle Bin")
	}
}

func TestReviewArtifactWithoutRecycleBinIsKept(t *testing.T) {
	w := newWorld(t)
	res := w.find()
	review := w.byPath(res)[`handmade\dist`]
	if review == nil {
		t.Fatal("fixture missing")
	}
	for _, r := range []filesystem.Recycler{noRecycleBin{}, nil} {
		out := Remove(context.Background(), w.env, res, []*Artifact{review}, r, nil)
		if out.Recycled != 0 || out.Removed != 0 || review.Result.Kept == "" || review.Result.Method != "" {
			t.Errorf("recycler %T: outcome %+v result %+v", r, out, review.Result)
		}
		if _, err := os.Lstat(filepath.Join(review.Path, "index.html")); err != nil {
			t.Fatalf("recycler %T: files removed without a Recycle Bin", r)
		}
	}
	Remove(context.Background(), w.env, res, []*Artifact{review}, noRecycleBin{}, nil)
	if review.Result.Kept != "the drive has no Recycle Bin" {
		t.Errorf("kept reason %q", review.Result.Kept)
	}
}

func (w *world) find() *Result {
	w.t.Helper()
	res := Find(context.Background(), w.env, Roots(w.env, nil, nil), nil)
	if res.Cancelled {
		w.t.Fatal("scan cancelled")
	}
	return res
}

// byPath indexes artifacts by their path relative to ~\source\repos (or
// Documents\GitHub for paths starting with "GitHub\").
func (w *world) byPath(res *Result) map[string]*Artifact {
	out := map[string]*Artifact{}
	for _, a := range res.Artifacts() {
		testutil.AssertInSandbox(w.t, a.Path)
		rel, err := filepath.Rel(sandbox.ProjectsDir(w.root), a.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel, _ = filepath.Rel(sandbox.DocumentsDir(w.root), a.Path)
		}
		out[rel] = a
	}
	return out
}

func TestDefaultRoots(t *testing.T) {
	w := newWorld(t)
	roots := Roots(w.env, nil, nil)
	var got []string
	for _, r := range roots {
		if r.Status != RootOK || r.Source != SourceDefault {
			t.Errorf("root %+v", r)
		}
		got = append(got, r.Path)
	}
	if len(got) != 2 || !strings.HasSuffix(got[0], `\source\repos`) || !strings.HasSuffix(got[1], `\Documents\GitHub`) {
		t.Errorf("default roots = %v", got)
	}
}

func TestFindInSandbox(t *testing.T) {
	needGit(t)
	w := newWorld(t)
	before := testutil.SnapshotDir(t, w.root)
	res := w.find()
	if after := testutil.SnapshotDir(t, w.root); len(after) != len(before) {
		t.Fatal("scanning changed the filesystem")
	}
	got := w.byPath(res)
	want := map[string]Status{
		`webapp\node_modules`:               StatusReady,
		`webapp\.next`:                      StatusReady,
		`webapp\dist`:                       StatusKept,   // committed to Git
		`fresh-app\node_modules`:            StatusReview, // installed yesterday
		`rustapp\target`:                    StatusReady,
		`Api\bin`:                           StatusReady,
		`Api\obj`:                           StatusReady,
		`pyproj\.venv`:                      StatusReady,
		`pyproj\app\__pycache__`:            StatusReady,
		`pyproj\.pytest_cache`:              StatusReady,
		`deploy-site\build`:                 StatusKept,   // holds a deployment key
		`handmade\dist`:                     StatusReview, // not in Git: may be hand-made
		`monorepo\node_modules`:             StatusKept,   // workspace junction
		`monorepo\packages\ui\node_modules`: StatusReady,
		`nested-repo\node_modules`:          StatusKept, // nested repository
		`cpp-engine\build`:                  StatusReady,
		`android-app\.gradle`:               StatusReady,
		`android-app\app\build`:             StatusReady,
		`GitHub\game\.dart_tool`:            StatusReady,
	}
	for rel, status := range want {
		a := got[rel]
		if a == nil {
			t.Errorf("%s not found", rel)
			continue
		}
		if a.Status != status || a.Selected != (status == StatusReady) {
			t.Errorf("%s: status %s selected %v, want %s (reasons %v)", rel, a.Status, a.Selected, status, a.Reasons)
		}
		if a.Status != StatusReady && len(a.Reasons) == 0 {
			t.Errorf("%s: no reason given", rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("unexpected artifact %s (%s)", rel, got[rel].Status)
		}
	}
	reason := func(rel, sub string) {
		if a := got[rel]; a != nil && !strings.Contains(strings.Join(a.Reasons, "; "), sub) {
			t.Errorf("%s reasons %v, want %q", rel, a.Reasons, sub)
		}
	}
	reason(`webapp\dist`, "tracked by Git")
	reason(`deploy-site\build`, "deploy_key.pem")
	reason(`monorepo\node_modules`, "links or junctions")
	reason(`nested-repo\node_modules`, "Git repository")
	reason(`fresh-app\node_modules`, "recent activity")
	reason(`handmade\dist`, "not in a Git repository")
	if a := got[`webapp\node_modules`]; a != nil && (a.Files != 4 || a.Bytes != 900+300000+20000+1700 || a.Kind != "node_modules") {
		t.Errorf("webapp node_modules = %d files, %d bytes, kind %s", a.Files, a.Bytes, a.Kind)
	}
	for _, p := range res.Projects {
		if strings.Contains(p.Path, `\gosvc`) || strings.Contains(p.Path, `.vscode`) || strings.Contains(p.Path, `AppData`) {
			t.Errorf("project %s should not have artifacts", p.Path)
		}
	}
}

func TestRemoveDeletesOnlySelected(t *testing.T) {
	needGit(t)
	w := newWorld(t)
	res := w.find()
	var chosen []*Artifact
	for _, a := range res.Artifacts() {
		if a.Selected {
			chosen = append(chosen, a)
		}
	}
	out := Remove(context.Background(), w.env, res, chosen, w.bin(), nil)
	if out.Errors != 0 || out.Cancelled || out.Removed != len(chosen) {
		t.Fatalf("outcome: removed %d of %d, errors %d", out.Removed, len(chosen), out.Errors)
	}
	for _, a := range chosen {
		if _, err := os.Lstat(a.Path); err == nil {
			t.Errorf("%s still exists", a.Path)
		}
		if a.Result == nil || !a.Result.Complete || a.Result.Reclaimed != a.Bytes {
			t.Errorf("%s result %+v (scanned %d bytes)", a.Path, a.Result, a.Bytes)
		}
	}
	for _, keep := range []string{
		`webapp\src\index.js`, `webapp\package.json`, `webapp\dist\bundle.js`, `webapp\.git\HEAD`,
		`fresh-app\node_modules\lodash\lodash.js`, `deploy-site\build\deploy_key.pem`, `handmade\dist\index.html`,
		`monorepo\node_modules\typescript\lib\tsc.js`, `monorepo\packages\ui\src\button.js`,
		`nested-repo\node_modules\private-dep\.git\HEAD`, `gosvc\vendor\example.com\widget\node_modules\x\x.js`,
		`cpp-engine\assets\build\logo.png`, `Api\tools\bin\deploy.ps1`, `pyproj\tools\venv\notes.txt`,
		`pyproj\app\main.py`, `tools\out\cli.js`, `rustapp\Cargo.toml`,
	} {
		if _, err := os.Lstat(w.repos(keep)); err != nil {
			t.Errorf("%s was removed", keep)
		}
	}
	if _, err := os.Lstat(filepath.Join(w.locs.UserProfile, ".vscode", "extensions", "ms-python.python-2025.1.0", "node_modules", "x", "x.js")); err != nil {
		t.Error("VS Code extension files were removed")
	}
	// The workspace package the junction pointed at is intact.
	if _, err := os.Lstat(w.repos(`monorepo\node_modules\@acme\ui`)); err != nil {
		t.Error("workspace junction removed")
	}
}

func TestGitMissingKeepsRepositoryArtifacts(t *testing.T) {
	w := newWorld(t)
	w.env.Git = ExecGit{Path: filepath.Join(w.root, "no-such-git.exe")}
	got := w.byPath(w.find())
	for _, rel := range []string{`webapp\node_modules`, `webapp\.next`, `monorepo\packages\ui\node_modules`} {
		a := got[rel]
		if a == nil || a.Status != StatusKept || !strings.Contains(strings.Join(a.Reasons, " "), "Git is not installed") {
			t.Errorf("%s = %+v", rel, a)
		}
	}
	if a := got[`rustapp\target`]; a == nil || a.Status != StatusReady {
		t.Errorf("artifacts outside repositories are unaffected: %+v", a)
	}
}

type failingGit struct{}

func (failingGit) Tracked(context.Context, string, []string) (map[string]bool, error) {
	return nil, errors.New("fatal: detected dubious ownership")
}
func (failingGit) Ignored(context.Context, string, []string) (map[string]bool, error) {
	return nil, errors.New("fatal")
}

func TestGitFailureKeepsArtifacts(t *testing.T) {
	w := newWorld(t)
	w.env.Git = failingGit{}
	res := w.find()
	a := w.byPath(res)[`webapp\node_modules`]
	if a == nil || a.Status != StatusKept || !strings.Contains(a.Reasons[0], "dubious ownership") {
		t.Fatalf("artifact = %+v", a)
	}
	// Even when chosen directly, deletion re-checks Git and keeps it.
	a.Status = StatusReady
	out := Remove(context.Background(), w.env, res, []*Artifact{a}, w.bin(), nil)
	if out.Removed != 0 || a.Result.Kept == "" {
		t.Fatalf("removed despite failing Git: %+v", a.Result)
	}
	if _, err := os.Lstat(filepath.Join(a.Path, "react", "index.js")); err != nil {
		t.Error("files removed")
	}
}

func TestChangesAfterScanAreKept(t *testing.T) {
	needGit(t)
	w := newWorld(t)
	res := w.find()
	got := w.byPath(res)
	target, venv, nm := got[`rustapp\target`], got[`pyproj\.venv`], got[`webapp\node_modules`]
	if target == nil || venv == nil || nm == nil {
		t.Fatal("fixtures missing")
	}
	// A file appears inside target (a build is running).
	if err := os.WriteFile(filepath.Join(target.Path, "debug", "new.o"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A folder inside .venv is swapped for a junction to Documents.
	site := filepath.Join(venv.Path, "Lib", "site-packages", "requests")
	if err := os.Rename(site, site+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.MakeJunction(site, sandbox.DocumentsDir(w.root)); err != nil {
		t.Fatal(err)
	}
	// A file inside node_modules is committed to Git after the scan.
	git := exec.Command("git", "-C", w.repos("webapp"), "-c", "user.name=t", "-c", "user.email=t@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=NUL", "add", "-f", "node_modules/react/index.js")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	docs := testutil.SnapshotDir(t, sandbox.DocumentsDir(w.root))
	out := Remove(context.Background(), w.env, res, []*Artifact{target, venv, nm}, w.bin(), nil)
	if out.Removed != 0 || out.RemovedFiles != 0 {
		t.Fatalf("removed %d artifacts, %d files", out.Removed, out.RemovedFiles)
	}
	for a, want := range map[*Artifact]string{target: "changed since it was scanned", venv: "links or junctions", nm: "tracked by Git"} {
		if a.Result == nil || !strings.Contains(a.Result.Kept, want) {
			t.Errorf("%s: result %+v, want kept for %q", a.Path, a.Result, want)
		}
	}
	if after := testutil.SnapshotDir(t, sandbox.DocumentsDir(w.root)); len(after) != len(docs) {
		t.Error("Documents changed")
	}
	// The artifact folder itself replaced by a junction is refused too.
	cmake := got[`cpp-engine\build`]
	if err := os.Rename(cmake.Path, cmake.Path+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.MakeJunction(cmake.Path, sandbox.DocumentsDir(w.root)); err != nil {
		t.Fatal(err)
	}
	Remove(context.Background(), w.env, res, []*Artifact{cmake}, w.bin(), nil)
	if cmake.Result == nil || cmake.Result.Kept == "" || cmake.Result.RemovedFiles != 0 {
		t.Errorf("junction artifact result %+v", cmake.Result)
	}
}

// The sink itself refuses a path that leaves the artifact through a
// junction, whatever the walk saw.
func TestSinkRefusesPathsOutsideTheArtifact(t *testing.T) {
	w := newWorld(t)
	art := w.repos(`rustapp\target`)
	link := filepath.Join(art, "escape")
	if err := sandbox.MakeJunction(link, sandbox.DocumentsDir(w.root)); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(link, "thesis.docx")
	e, err := filesystem.Lstat(victim)
	if err != nil {
		t.Fatal(err)
	}
	err = filesystem.RemoveVerified(victim, e.Fingerprint, purgeCheck(w.env.Guard, art, false))
	if !errors.Is(err, filesystem.ErrPolicy) {
		t.Fatalf("RemoveVerified through a junction = %v, want a policy refusal", err)
	}
	if _, err := os.Lstat(filepath.Join(sandbox.DocumentsDir(w.root), "thesis.docx")); err != nil {
		t.Fatal("document removed")
	}
	// A sensitive file directly inside the artifact is refused at the sink.
	key := filepath.Join(art, "deploy.pem")
	if err := sandbox.WriteFile(key, 100, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, _ = filesystem.Lstat(key)
	if err := filesystem.RemoveVerified(key, e.Fingerprint, purgeCheck(w.env.Guard, art, false)); !errors.Is(err, filesystem.ErrPolicy) {
		t.Fatalf("sensitive file: %v", err)
	}
}

func TestRootValidation(t *testing.T) {
	w := newWorld(t)
	l := w.locs
	link := filepath.Join(w.root, "projects-link")
	if err := sandbox.MakeJunction(link, sandbox.ProjectsDir(w.root)); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(sandbox.DocumentsDir(w.root), "old-notes.txt")
	cases := map[string]string{
		l.Windows:                               RootRefused,
		l.ProgramFiles:                          RootRefused,
		filepath.Join(l.LocalAppData, "Google"): RootRefused,
		l.RoamingAppData:                        RootRefused,
		l.UsersRoot:                             RootRefused,
		filepath.Join(l.UserProfile, ".ssh"):    RootRefused,
		link:                                    RootRefused,
		notes:                                   RootRefused,
		`relative\projects`:                     RootRefused,
		filepath.Join(w.root, "missing"):        RootMissing,
		l.UserProfile:                           RootOK,
		sandbox.ProjectsDir(w.root):             RootOK,
	}
	for p, want := range cases {
		r := Roots(w.env, []string{p}, nil)
		if len(r) != 1 || r[0].Status != want {
			t.Errorf("Roots(%s) = %+v, want %s", p, r, want)
		}
	}
	// Nested roots are scanned once.
	r := Roots(w.env, []string{l.UserProfile, sandbox.ProjectsDir(w.root)}, nil)
	if len(r) != 2 || r[0].Status != RootOK || r[1].Status != RootRefused {
		t.Errorf("nested roots = %+v", r)
	}
	// Configured paths win over defaults, arguments over both.
	if r := Roots(w.env, nil, []string{sandbox.DocumentsDir(w.root)}); len(r) != 1 || r[0].Source != SourceConfig {
		t.Errorf("configured roots = %+v", r)
	}
}

func TestProfileRootSkipsToolState(t *testing.T) {
	needGit(t)
	w := newWorld(t)
	res := Find(context.Background(), w.env, Roots(w.env, []string{w.locs.UserProfile}, nil), nil)
	for _, a := range res.Artifacts() {
		if strings.Contains(a.Path, `\.vscode\`) || strings.Contains(a.Path, `\AppData\`) {
			t.Errorf("tool state offered: %s", a.Path)
		}
	}
	if len(w.byPath(res)) != 19 {
		t.Errorf("found %d artifacts from the profile, want the 19 project artifacts", len(w.byPath(res)))
	}
}

func TestCancelledScanIsMarked(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Find(ctx, w.env, Roots(w.env, nil, nil), nil)
	if !res.Cancelled {
		t.Fatal("cancelled scan not marked")
	}
	for _, a := range res.Artifacts() {
		if a.Selected {
			t.Errorf("%s selected in a cancelled scan", a.Path)
		}
	}
}

func TestEveryKindIsAcceptedByTheGuard(t *testing.T) {
	for _, k := range Kinds {
		for _, n := range k.Names {
			if !safety.IsPurgeArtifactName(strings.ReplaceAll(n, "*", "x")) {
				t.Errorf("kind %s name %s is not a guard artifact name", k.ID, n)
			}
		}
		if k.Label == "" || k.Rebuild == "" || k.Ecosystem == "" || len(k.Markers) == 0 {
			t.Errorf("kind %s is incomplete", k.ID)
		}
		for _, m := range append(append([]string{}, k.Markers...), k.only...) {
			if m != strings.ToLower(m) {
				t.Errorf("kind %s pattern %s must be lower case", k.ID, m)
			}
		}
	}
}

func TestGitPathspecsAreLiteral(t *testing.T) {
	needGit(t)
	dir := testutil.Dir(t)
	for _, f := range []string{`we[ird]\a.txt`, `weird\b.txt`, `Dist\c.js`} {
		if err := sandbox.WriteFile(filepath.Join(dir, f), 10, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=NUL"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "--", "we[ird]/a.txt", "Dist/c.js")
	tracked, err := ExecGit{}.Tracked(context.Background(), dir, []string{"weird", "dist", "we[ird]", "other"})
	if err != nil {
		t.Fatal(err)
	}
	if tracked["weird"] || !tracked["dist"] || !tracked["we[ird]"] || tracked["other"] {
		t.Errorf("tracked = %v (pathspecs must be literal and case-insensitive)", tracked)
	}
}
