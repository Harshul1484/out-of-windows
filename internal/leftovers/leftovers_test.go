package leftovers_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/leftovers"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/tasks"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
	"github.com/Harshul1484/out-of-windows/internal/uninstall"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type world struct {
	t     *testing.T
	root  string
	locs  safety.Locations
	guard *safety.Guard
	sim   sandbox.Apps
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := testutil.Dir(t)
	if err := sandbox.Seed(root); err != nil {
		t.Fatal(err)
	}
	locs := sandbox.Locations(root)
	return &world{t: t, root: root, locs: locs, guard: safety.NewGuard(locs, nil), sim: sandbox.Apps{Root: root}}
}

func (w *world) inventory() *apps.Inventory {
	inv, err := w.sim.List(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return inv
}

func (w *world) app(name string) apps.App {
	for _, a := range w.inventory().Apps {
		if a.Name == name {
			return a
		}
	}
	w.t.Fatalf("app %s not found", name)
	return apps.App{}
}

func (w *world) env(elevated bool) *leftovers.Env {
	inv := w.inventory()
	return &leftovers.Env{
		Guard:          w.guard,
		Claims:         leftovers.NewClaims(inv, func(a apps.App) bool { return a.IsBroken() }, nil, w.guard),
		Elevated:       elevated,
		RecentActivity: 7 * 24 * time.Hour,
	}
}

func (w *world) rel(p string) string {
	r, err := filepath.Rel(w.root, p)
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *world) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(w.root, rel))
	return err == nil
}

// uninstallApp runs the simulated uninstaller through the real orchestration.
func (w *world) uninstallApp(name string) apps.App {
	w.t.Helper()
	a := w.app(name)
	p, err := uninstall.NewPlan(a, false)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := uninstall.Execute(context.Background(), p, w.sim, w.sim, uninstall.Options{WaitTimeout: time.Second, Poll: 10 * time.Millisecond})
	if err != nil || !out.Removed {
		w.t.Fatalf("uninstall %s: %+v, %v", name, out, err)
	}
	return a
}

func byPath(res *leftovers.Result, w *world) map[string]leftovers.Candidate {
	m := map[string]leftovers.Candidate{}
	for _, c := range res.Candidates {
		m[w.rel(c.Path)] = c
	}
	return m
}

const (
	pfContoso = `C\Program Files\Contoso`
	roam      = `C\Users\sandbox\AppData\Roaming`
	local     = `C\Users\sandbox\AppData\Local`
)

func TestUninstallThenLeftovers(t *testing.T) {
	w := newWorld(t)
	studio := w.uninstallApp("Contoso Studio")
	res := leftovers.Find(context.Background(), w.env(true), []leftovers.Evidence{leftovers.FromApp(studio, leftovers.SourceUninstalled)})
	got := byPath(res, w)
	want := map[string]leftovers.Confidence{
		pfContoso + `\Studio`:          leftovers.High, // recorded install folder
		roam + `\Contoso\Studio`:       leftovers.High, // publisher folder + product name
		`C\ProgramData\Contoso\Studio`: leftovers.High,
		local + `\Contoso Studio`:      leftovers.Medium, // name only
	}
	if len(got) != len(want) {
		t.Errorf("candidates = %v", keys(got))
	}
	for p, conf := range want {
		c, ok := got[p]
		if !ok {
			t.Errorf("missing candidate %s", p)
			continue
		}
		if c.Confidence != conf || len(c.Reasons) == 0 {
			t.Errorf("%s: confidence %s, reasons %v; want %s", p, c.Confidence, c.Reasons, conf)
		}
	}
	if c := got[pfContoso+`\Studio`]; c.Bytes != 5000000 || c.Files != 1 {
		t.Errorf("install folder size = %d bytes, %d files", c.Bytes, c.Files)
	}

	out := leftovers.Recycle(context.Background(), w.env(true), res.Candidates, sandbox.Recycler{Root: w.root})
	if len(out.Recycled) != 4 || out.Errors != 0 {
		t.Fatalf("recycle outcome = %+v", out)
	}
	for p := range want {
		if w.exists(p) {
			t.Errorf("%s not recycled", p)
		}
	}
	// Siblings of other Contoso products and vendor files are kept; the
	// emptied Roaming\Contoso publisher folder is removed.
	for _, kept := range []string{pfContoso + `\Agent\agent.exe`, pfContoso + `\contoso.exe`,
		`C\ProgramData\Contoso\license.dat`, local + `\Contoso\settings.json`} {
		if !w.exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
	if w.exists(roam + `\Contoso`) {
		t.Error("empty publisher folder kept")
	}
	// Everything went to the (simulated) Recycle Bin, not away.
	bin, _ := os.ReadDir(filepath.Join(sandbox.RecycleBinDir(w.root), "S-1-5-21-sandbox"))
	if len(bin) < 4+3 { // 4 recycled folders + 3 seeded bin items
		t.Errorf("Recycle Bin has %d entries", len(bin))
	}
}

func TestInstalledAppsAreClaimed(t *testing.T) {
	w := newWorld(t)
	// Pretend Wingtip Toys was uninstalled while it is still installed.
	ev := leftovers.FromApp(w.app("Wingtip Toys"), leftovers.SourceHistory)
	res := leftovers.Find(context.Background(), w.env(true), []leftovers.Evidence{ev})
	if len(res.Candidates) != 0 {
		t.Fatalf("installed app's folders offered: %v", res.Candidates)
	}
	if len(res.Kept) == 0 || !strings.Contains(res.Kept[0].Reason, "still used by Wingtip Toys") {
		t.Errorf("kept = %+v", res.Kept)
	}
}

func TestUsageTraces(t *testing.T) {
	w := newWorld(t)
	evs := leftovers.EvidenceFromTraces(sandbox.Traces(w.root), w.guard, apps.FileExists)
	var names []string
	for _, e := range evs {
		names = append(names, e.Name)
	}
	if len(evs) != 2 || names[0] != "Old Editor" || names[1] != "Recent Tool" {
		t.Fatalf("trace evidence = %v (Temp, Downloads and still-installed programs must be ignored)", names)
	}
	res := leftovers.Find(context.Background(), w.env(true), evs)
	got := byPath(res, w)
	for _, p := range []string{`C\Program Files\OldEditor`, roam + `\Old Editor`} {
		if c, ok := got[p]; !ok || c.Confidence != leftovers.Medium {
			t.Errorf("%s: %+v (usage traces are at most medium confidence)", p, c)
		}
	}
	recentKept := false
	for _, k := range res.Kept {
		if strings.Contains(k.Path, "Recent Tool") && strings.Contains(k.Reason, "changed recently") {
			recentKept = true
		}
	}
	if !recentKept || len(got) != 2 {
		t.Errorf("candidates %v, kept %+v", keys(got), res.Kept)
	}
}

// A scheduled task that loads a DLL from a leftover folder keeps that folder,
// even while the task is disabled; the app's other folder is still offered.
func TestScheduledTasksClaimFolders(t *testing.T) {
	w := newWorld(t)
	evs := leftovers.EvidenceFromTraces(sandbox.Traces(w.root), w.guard, apps.FileExists)
	list, _, err := sandbox.Tasks{Root: w.root}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expand := func(s string) string { x, _ := sandbox.Expand(w.root, s); return x }
	find := func(ts []tasks.Task) *leftovers.Result {
		env := w.env(true)
		env.Claims = leftovers.NewClaims(w.inventory(), func(a apps.App) bool { return a.IsBroken() }, leftovers.TaskClaims(ts, expand), w.guard)
		return leftovers.Find(context.Background(), env, evs)
	}
	res := find(list)
	got := byPath(res, w)
	if _, ok := got[`C\Program Files\OldEditor`]; ok {
		t.Fatal("a folder a scheduled task uses was offered")
	}
	if _, ok := got[roam+`\Old Editor`]; !ok {
		t.Errorf("the folder the task does not use is missing: %v", keys(got))
	}
	kept := false
	for _, k := range res.Kept {
		kept = kept || w.rel(k.Path) == `C\Program Files\OldEditor` && k.Reason == `still used by scheduled task \Proseware\Old Editor Dictionary`
	}
	if !kept {
		t.Errorf("kept = %+v", res.Kept)
	}
	// Without the task the folder is offered again.
	var others []tasks.Task
	for _, task := range list {
		if task.Path != `\Proseware\Old Editor Dictionary` {
			others = append(others, task)
		}
	}
	if _, ok := byPath(find(others), w)[`C\Program Files\OldEditor`]; !ok {
		t.Error("folder kept although no task uses it")
	}
}

func TestTaskClaimPaths(t *testing.T) {
	claims := leftovers.TaskClaims([]tasks.Task{{Path: `\Vendor\Job`, Actions: []tasks.Action{{
		Command:          `"C:\Program Files\Vendor\Job\job.exe"`,
		Arguments:        `--data "D:\Vendor Data\cache" --log=E:\logs\job.log`,
		WorkingDirectory: `C:\ProgramData\Vendor`,
	}}}}, nil)
	var got []string
	for _, c := range claims {
		got = append(got, c.Path)
		if c.Owner != `scheduled task \Vendor\Job` {
			t.Errorf("owner = %q", c.Owner)
		}
	}
	want := []string{`C:\Program Files\Vendor\Job`, `C:\ProgramData\Vendor`, `D:\Vendor Data\cache`, `D:\Vendor Data`,
		`E:\logs\job.log`, `E:\logs`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("claims = %q, want %q", got, want)
	}
}

const startMenu = `C\Users\sandbox\AppData\Roaming\Microsoft\Windows\Start Menu\Programs`

func (w *world) shortcutEnv() *leftovers.Env {
	env := w.env(true)
	env.Links = sandbox.Links(w.root)
	env.Shortcuts = leftovers.FindBrokenShortcuts(context.Background(), w.guard, sandbox.ShortcutFolders(w.root), env.Links)
	return env
}

// Broken shortcuts are exact evidence for the folder their program lived in:
// only programs verifiably missing count, Startup folders are not read, the
// shortcut's name is never matched, and the result is medium confidence.
func TestShortcutEvidence(t *testing.T) {
	w := newWorld(t)
	env := w.shortcutEnv()
	got := map[string]leftovers.Shortcut{}
	for _, sc := range env.Shortcuts {
		got[w.rel(sc.Path)] = sc
	}
	want := map[string]bool{ // broken shortcut -> may be recycled
		startMenu + `\Adatum\Adatum Photo.lnk`:                                 true,
		`C\Users\sandbox\Desktop\Adatum Photo.lnk`:                             true,
		`C\ProgramData\Microsoft\Windows\Start Menu\Programs\Adatum Photo.lnk`: false, // all users
		startMenu + `\Gone Game.lnk`:                                           true,
		startMenu + `\Toolbox.lnk`:                                             true,
	}
	if len(got) != len(want) {
		t.Errorf("broken shortcuts = %v", got)
	}
	for p, removable := range want {
		sc, ok := got[p]
		if !ok {
			t.Errorf("%s not reported", p)
			continue
		}
		if sc.Removable != removable || removable == (sc.Note != "") {
			t.Errorf("%s = %+v", p, sc)
		}
	}
	// A working shortcut, a network shortcut (unknown, not missing) and
	// startup entries are not broken shortcuts here.
	for _, p := range []string{startMenu + `\Wingtip Toys.lnk`, `C\Users\sandbox\Desktop\Team Tool.lnk`,
		startMenu + `\Contoso\Contoso Studio.lnk`, startMenu + `\Startup\Old Notes.lnk`} {
		if _, ok := got[p]; ok {
			t.Errorf("%s reported as broken", p)
		}
	}

	// Gone Game's folder is gone and Toolbox points into a shared "Tools"
	// folder: only Adatum Photo is evidence.
	evs := leftovers.EvidenceFromShortcuts(env.Shortcuts, w.guard)
	if len(evs) != 1 || evs[0].Name != "Adatum Photo" || evs[0].Source != leftovers.SourceShortcut || len(evs[0].Shortcuts) != 3 ||
		w.rel(evs[0].InstallLocation) != `C\Users\sandbox\AppData\Local\Programs\Adatum Photo` || evs[0].Exes[0] != "adatum.exe" {
		t.Fatalf("evidence = %+v", evs)
	}
	res := leftovers.Find(context.Background(), env, evs)
	cands := byPath(res, w)
	c, ok := cands[`C\Users\sandbox\AppData\Local\Programs\Adatum Photo`]
	// Roaming\Adatum Photo matches only by name: never offered.
	if len(cands) != 1 || !ok || c.Confidence != leftovers.Medium || len(c.Shortcuts) != 3 || c.NeedsAdmin {
		t.Fatalf("candidates = %+v", res.Candidates)
	}

	// Recent activity keeps a folder found only through shortcuts.
	if err := sandbox.WriteFile(filepath.Join(w.root, `C\Users\sandbox\AppData\Local\Programs\Adatum Photo\new.tmp`), 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	res = leftovers.Find(context.Background(), env, evs)
	if len(res.Candidates) != 0 || len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Reason, "changed recently") {
		t.Errorf("recently changed: %+v", res)
	}
}

// Moving the folder takes the user's own shortcuts with it; the all-users
// shortcut and unrelated shortcuts and folders stay.
func TestShortcutsGoWithTheirFolder(t *testing.T) {
	w := newWorld(t)
	env := w.shortcutEnv()
	res := leftovers.Find(context.Background(), env, leftovers.EvidenceFromShortcuts(env.Shortcuts, w.guard))
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %+v", res.Candidates)
	}
	out := leftovers.Recycle(context.Background(), env, res.Candidates, sandbox.Recycler{Root: w.root})
	if len(out.Recycled) != 1 || len(out.RecycledShortcuts) != 2 || out.Errors != 0 || len(out.Skipped) != 0 {
		t.Fatalf("outcome = %+v", out)
	}
	for _, gone := range []string{`C\Users\sandbox\AppData\Local\Programs\Adatum Photo`, startMenu + `\Adatum\Adatum Photo.lnk`,
		`C\Users\sandbox\Desktop\Adatum Photo.lnk`} {
		if w.exists(gone) {
			t.Errorf("%s not moved", gone)
		}
	}
	for _, kept := range []string{`C\ProgramData\Microsoft\Windows\Start Menu\Programs\Adatum Photo.lnk`,
		startMenu + `\Wingtip Toys.lnk`, startMenu + `\Gone Game.lnk`, startMenu + `\Toolbox.lnk`,
		`C\Users\sandbox\Desktop\Team Tool.lnk`, `C\Users\sandbox\AppData\Roaming\Adatum Photo\gallery.db`,
		`C\Users\sandbox\AppData\Local\Tools\other-tool.exe`} {
		if !w.exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
}

// After an uninstall the app's own Start Menu shortcut points into its
// leftover folder and goes with it; a shortcut changed after the scan stays.
func TestUninstalledAppShortcuts(t *testing.T) {
	w := newWorld(t)
	studio := w.uninstallApp("Contoso Studio")
	env := w.shortcutEnv()
	res := leftovers.Find(context.Background(), env, []leftovers.Evidence{leftovers.FromApp(studio, leftovers.SourceUninstalled)})
	c := byPath(res, w)[pfContoso+`\Studio`]
	if c.Confidence != leftovers.High || len(c.Shortcuts) != 1 || !c.Shortcuts[0].Removable ||
		w.rel(c.Shortcuts[0].Path) != startMenu+`\Contoso\Contoso Studio.lnk` {
		t.Fatalf("install folder = %+v", c)
	}
	// The shortcut now starts a program that exists: it is not moved.
	lnk := filepath.Join(w.root, startMenu, "Contoso", "Contoso Studio.lnk")
	if err := os.WriteFile(lnk, startup.BuildLink(filepath.Join(w.root, `C\Program Files\Contoso\Agent\agent.exe`), ""), 0o644); err != nil {
		t.Fatal(err)
	}
	out := leftovers.Recycle(context.Background(), env, []leftovers.Candidate{c}, sandbox.Recycler{Root: w.root})
	if len(out.Recycled) != 1 || len(out.RecycledShortcuts) != 0 || len(out.Skipped) != 1 ||
		out.Skipped[0].Reason != "changed since it was scanned" || !w.exists(startMenu+`\Contoso\Contoso Studio.lnk`) {
		t.Errorf("outcome = %+v", out)
	}
}

func TestBrokenEntryEvidence(t *testing.T) {
	w := newWorld(t)
	lit := w.app("Litware Tool")
	if !lit.IsBroken() {
		t.Fatalf("Litware Tool not detected as broken: %+v", lit)
	}
	if ok, _ := lit.Removable(); ok {
		t.Error("broken entry offered for normal uninstall")
	}
	res := leftovers.Find(context.Background(), w.env(true), []leftovers.Evidence{leftovers.FromApp(lit, leftovers.SourceBroken)})
	got := byPath(res, w)
	if c, ok := got[`C\Program Files\Litware Tool`]; !ok || c.Confidence != leftovers.High || c.Bytes != 3002000 {
		t.Fatalf("Litware leftovers = %+v", got)
	}
}

func TestSensitiveContentKeepsFolder(t *testing.T) {
	w := newWorld(t)
	studio := w.uninstallApp("Contoso Studio")
	vault := filepath.Join(w.root, local, "Contoso Studio", "backup", "vault.kdbx")
	if err := sandbox.WriteFile(vault, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	res := leftovers.Find(context.Background(), w.env(true), []leftovers.Evidence{leftovers.FromApp(studio, leftovers.SourceUninstalled)})
	if _, ok := byPath(res, w)[local+`\Contoso Studio`]; ok {
		t.Fatal("folder containing a password database offered")
	}
	found := false
	for _, k := range res.Kept {
		found = found || strings.Contains(k.Reason, "vault.kdbx")
	}
	if !found {
		t.Errorf("kept = %+v", res.Kept)
	}
}

func TestAdminLocationsNeedElevation(t *testing.T) {
	w := newWorld(t)
	studio := w.uninstallApp("Contoso Studio")
	env := w.env(false)
	res := leftovers.Find(context.Background(), env, []leftovers.Evidence{leftovers.FromApp(studio, leftovers.SourceUninstalled)})
	for _, c := range res.Candidates {
		want := strings.Contains(c.Path, `\Program Files\`) || strings.Contains(c.Path, `\ProgramData\`)
		if c.NeedsAdmin != want {
			t.Errorf("%s NeedsAdmin = %v", w.rel(c.Path), c.NeedsAdmin)
		}
	}
	out := leftovers.Recycle(context.Background(), env, res.Candidates, sandbox.Recycler{Root: w.root})
	if len(out.Recycled) != 2 || len(out.Skipped) != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if !w.exists(pfContoso + `\Studio`) {
		t.Error("admin-only leftover removed without elevation")
	}
}

func TestJunctionsAreNotLeftovers(t *testing.T) {
	w := newWorld(t)
	player := w.uninstallApp("Fabrikam Player")
	// Replace the leftover data folder with a junction to Documents.
	data := filepath.Join(w.root, roam, "Fabrikam Player")
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.MakeJunction(data, w.locs.UserContent[1]); err != nil {
		t.Fatal(err)
	}
	res := leftovers.Find(context.Background(), w.env(true), []leftovers.Evidence{leftovers.FromApp(player, leftovers.SourceUninstalled)})
	if len(res.Candidates) != 0 {
		t.Fatalf("junction offered: %+v", res.Candidates)
	}
}

func TestRecycleRefusesSwappedFolder(t *testing.T) {
	w := newWorld(t)
	player := w.uninstallApp("Fabrikam Player")
	res := leftovers.Find(context.Background(), w.env(true), []leftovers.Evidence{leftovers.FromApp(player, leftovers.SourceUninstalled)})
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %+v", res.Candidates)
	}
	// After review, the folder is replaced by a junction to Documents.
	data := res.Candidates[0].Path
	if err := os.Rename(data, data+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.MakeJunction(data, w.locs.UserContent[1]); err != nil {
		t.Fatal(err)
	}
	before := testutil.SnapshotDir(t, w.locs.UserContent[1])
	out := leftovers.Recycle(context.Background(), w.env(true), res.Candidates, sandbox.Recycler{Root: w.root})
	if len(out.Recycled) != 0 {
		t.Fatalf("swapped folder recycled: %+v", out)
	}
	if after := testutil.SnapshotDir(t, w.locs.UserContent[1]); len(after) != len(before) {
		t.Fatal("Documents changed")
	}
}

func TestFailedUninstallLeavesEverything(t *testing.T) {
	w := newWorld(t)
	a := w.app("Northwind Sync")
	p, err := uninstall.NewPlan(a, false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := uninstall.Execute(context.Background(), p, w.sim, w.sim, uninstall.Options{WaitTimeout: 100 * time.Millisecond, Poll: 10 * time.Millisecond})
	if err == nil || out.Removed || out.ExitCode != 1603 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if !w.exists(`C\Program Files\Northwind\Sync\sync.exe`) || !w.exists(roam+`\Northwind Sync\state.json`) {
		t.Fatal("files of a still-installed app were removed")
	}
}

func keys(m map[string]leftovers.Candidate) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
