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
