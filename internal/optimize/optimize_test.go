package optimize_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/optimize"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// fake is an in-memory optimize.Runner that records what ran.
type fake struct {
	doAvailable bool
	doBytes     int64 // -1 unknown
	ssds        []optimize.Volume
	ssdErr      error
	flushErr    error
	clearErr    error
	trimErr     map[string]error
	cancelAfter int // cancel the context after this many retrims (0 = never)
	cancel      context.CancelFunc

	// Component store: the report before and after a cleanup (nil: csErr).
	cs, csAfter      *optimize.ComponentStore
	csErr, afterErr  error // afterErr: the analysis after a cleanup fails
	cleanupErr       error
	cleanupRestart   bool
	cancelInCleanup  bool // the user presses Ctrl+C while DISM cleans up
	cancelInAnalysis bool // ... or while DISM analyzes

	flushed, cleared    int
	trimmed             []string
	analyses, cleanups  int
	cleanupSawCancelled bool
}

func (f *fake) AnalyzeComponentStore(ctx context.Context) (optimize.ComponentStore, error) {
	f.analyses++
	if f.cancelInAnalysis {
		f.cancel()
	}
	if ctx.Err() != nil {
		return optimize.ComponentStore{}, ctx.Err()
	}
	if f.cleanups > 0 && f.afterErr != nil {
		return optimize.ComponentStore{}, f.afterErr
	}
	if f.cleanups > 0 && f.csAfter != nil {
		return *f.csAfter, nil
	}
	if f.cs == nil {
		if f.csErr == nil {
			return optimize.ComponentStore{}, optimize.ErrNoDISM
		}
		return optimize.ComponentStore{}, f.csErr
	}
	return *f.cs, nil
}

func (f *fake) CleanupComponentStore(ctx context.Context) (bool, error) {
	f.cleanups++
	if f.cancelInCleanup {
		f.cancel()
		// The real runner keeps waiting for DISM after a cancel.
		f.cleanupSawCancelled = ctx.Err() != nil
	}
	return f.cleanupRestart, f.cleanupErr
}

func (f *fake) FlushDNS(context.Context) error { f.flushed++; return f.flushErr }
func (f *fake) DeliveryOptimization(_ context.Context, elevated bool) optimize.DOCache {
	c := optimize.DOCache{Available: f.doAvailable, Bytes: -1}
	if elevated {
		c.Bytes = f.doBytes
	}
	return c
}
func (f *fake) ClearDeliveryOptimization(context.Context) error {
	f.cleared++
	if f.clearErr == nil {
		f.doBytes = 0
	}
	return f.clearErr
}
func (f *fake) SSDVolumes(context.Context) ([]optimize.Volume, error) { return f.ssds, f.ssdErr }
func (f *fake) ReTrim(_ context.Context, v optimize.Volume) error {
	f.trimmed = append(f.trimmed, v.Root)
	if f.cancelAfter > 0 && len(f.trimmed) == f.cancelAfter {
		f.cancel()
	}
	return f.trimErr[v.Root]
}

func ready() *fake {
	return &fake{doAvailable: true, doBytes: 300 << 20, ssds: []optimize.Volume{{Root: `C:\`, FileSystem: "NTFS"}},
		cs: &optimize.ComponentStore{ActualBytes: 10 << 30, ExplorerBytes: -1, SharedBytes: -1, BackupsBytes: 4 << 30,
			CacheBytes: 1 << 20, ReclaimablePackages: 3, Recommended: true},
		csAfter: &optimize.ComponentStore{ActualBytes: 7 << 30, ExplorerBytes: -1, SharedBytes: -1, BackupsBytes: 1 << 30},
	}
}

func statuses(items []optimize.Item) map[string]optimize.Item {
	m := map[string]optimize.Item{}
	for _, it := range items {
		m[it.Task.ID] = it
	}
	return m
}

func TestTasksAreExplainedWithoutSpeedClaims(t *testing.T) {
	for _, task := range optimize.Tasks() {
		if task.ID == "" || task.Name == "" || task.What == "" || task.Why == "" || task.Effect == "" {
			t.Errorf("task %+v is missing an explanation", task)
		}
		if !strings.HasPrefix(task.ID, "optimize.") {
			t.Errorf("task ID %q", task.ID)
		}
		text := strings.ToLower(task.What + " " + task.Why + " " + task.Effect)
		for _, claim := range []string{"faster pc", "speeds up", "boost", "performance score"} {
			if strings.Contains(text, claim) {
				t.Errorf("%s claims %q", task.ID, claim)
			}
		}
	}
}

// named is --task component-store: the only way the opt-in task is analyzed
// in the plan and preselected.
var named = []string{"component-store"}

func TestPlanNotElevated(t *testing.T) {
	for _, filters := range [][]string{nil, named} {
		f := ready()
		plan := statuses(optimize.Plan(context.Background(), f, false, optimize.Tasks(), filters))
		if it := plan[optimize.TaskDNS]; it.Status != optimize.Ready || !it.Selected {
			t.Errorf("dns = %+v", it)
		}
		for _, id := range []string{optimize.TaskDO, optimize.TaskReTrim, optimize.TaskComponentStore} {
			it := plan[id]
			if it.Status != optimize.NeedsAdmin || it.Selected || !strings.Contains(it.Reason, "elevated terminal") {
				t.Errorf("%s = %+v", id, it)
			}
		}
		if plan[optimize.TaskDO].BytesBefore != -1 || plan[optimize.TaskComponentStore].BytesBefore != -1 {
			t.Error("sizes must be unknown without administrator rights")
		}
		if plan[optimize.TaskComponentStore].ComponentStore != nil || f.analyses != 0 {
			t.Error("DISM's analysis needs administrator rights and must not be attempted")
		}
		if f.flushed+f.cleared+len(f.trimmed)+f.cleanups != 0 {
			t.Fatal("Plan ran something")
		}
	}
}

// Not named: listed as ready but unselected, and DISM is not started at all,
// so neither the default plan nor --yes ever runs it.
func TestComponentStoreIsOptIn(t *testing.T) {
	// No --task, or a prefix that covers every task, does not name it.
	for _, filters := range [][]string{nil, {"optimize"}} {
		f := ready()
		tasks, err := optimize.Select(filters)
		if err != nil {
			t.Fatal(err)
		}
		plan := optimize.Plan(context.Background(), f, true, tasks, filters)
		it := statuses(plan)[optimize.TaskComponentStore]
		if it.Status != optimize.Ready || it.Selected || it.ComponentStore != nil || it.BytesBefore != -1 ||
			!strings.HasPrefix(it.Reason, "opt-in: it can take over an hour, and Windows also runs this cleanup on its own schedule") ||
			!strings.Contains(it.Reason, "`"+buildinfo.Name+" optimize --task component-store`") {
			t.Errorf("%v: %+v", filters, it)
		}
		if f.analyses != 0 {
			t.Errorf("%v: the plan started DISM", filters)
		}
		res := optimize.Run(context.Background(), f, plan)
		if f.analyses+f.cleanups != 0 || len(res) != 3 {
			t.Errorf("%v: --yes ran DISM: %+v", filters, res)
		}
	}
	for _, filters := range [][]string{named, {"optimize.component-store"}, {" Component-Store "}, {"dns-flush", "component-store"}} {
		if !optimize.Named(filters, optimize.TaskComponentStore) {
			t.Errorf("%q does not name the task", filters)
		}
	}
	for _, filters := range [][]string{nil, {"optimize"}, {"component"}, {"dns-flush"}} {
		if optimize.Named(filters, optimize.TaskComponentStore) {
			t.Errorf("%q names the task", filters)
		}
	}
}

func TestPlanComponentStore(t *testing.T) {
	f := ready()
	it := statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks(), named))[optimize.TaskComponentStore]
	if it.Status != optimize.Ready || !it.Selected || it.ComponentStore == nil || it.BytesBefore != 10<<30 || f.analyses != 1 ||
		it.Reason != "" {
		t.Errorf("recommended = %+v (analyses %d)", it, f.analyses)
	}

	f.cs.Recommended, f.cs.ReclaimablePackages = false, 0
	it = statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks(), named))[optimize.TaskComponentStore]
	if it.Status != optimize.NotApplicable || it.Selected || it.ComponentStore == nil ||
		!strings.Contains(it.Reason, "DISM does not recommend a cleanup (0 reclaimable packages)") {
		t.Errorf("not recommended = %+v", it)
	}

	// An unreadable report, a DISM failure or a missing DISM: never ready.
	for _, err := range []error{
		fmt.Errorf("%w: no %q line", optimize.ErrUnreadableReport, "Component Store Cleanup Recommended"),
		errors.New("DISM error 0x800f0806: pending"),
		optimize.ErrNoDISM,
	} {
		f = ready()
		f.cs, f.csErr = nil, err
		it = statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks(), named))[optimize.TaskComponentStore]
		if it.Status != optimize.Unavailable || it.Selected || it.ComponentStore != nil || it.BytesBefore != -1 ||
			!strings.Contains(it.Reason, err.Error()) || !strings.Contains(it.Reason, "only when DISM's analysis recommends it") {
			t.Errorf("%v: %+v", err, it)
		}
		// Even if a caller marks it selected, it never runs.
		plan := optimize.Plan(context.Background(), f, true, optimize.Tasks(), named)
		for i := range plan {
			plan[i].Selected = true
		}
		optimize.Run(context.Background(), f, plan)
		if f.cleanups != 0 {
			t.Errorf("%v: cleanup ran without a recommendation", err)
		}
	}
}

func componentResult(t *testing.T, f *fake, ctx context.Context) optimize.Result {
	t.Helper()
	plan := optimize.Plan(context.Background(), f, true, []*optimize.Task{optimize.Tasks()[3]}, named)
	res := optimize.Run(ctx, f, plan)
	if len(res) != 1 || res[0].ID != optimize.TaskComponentStore {
		t.Fatalf("results = %+v", res)
	}
	return res[0]
}

// Analyses: one in the plan, one right before the cleanup, one afterwards.
func TestRunComponentStore(t *testing.T) {
	f := ready()
	r := componentResult(t, f, context.Background())
	if r.Status != optimize.Done || f.cleanups != 1 || f.analyses != 3 || r.BytesBefore != 10<<30 || r.BytesAfter != 7<<30 ||
		r.Freed != 3<<30 || r.ComponentStore == nil || r.RestartRequired ||
		!strings.Contains(r.Message, "3.0 GB freed; the store is now 7.0 GB, 0 reclaimable packages left") {
		t.Errorf("done = %+v (analyses %d)", r, f.analyses)
	}

	f = ready()
	f.cleanupErr = errors.New("DISM error 0x800f0806: pending")
	if r := componentResult(t, f, context.Background()); r.Status != optimize.Failed || r.Freed != 0 || f.analyses != 2 ||
		!strings.Contains(r.Message, "0x800f0806") {
		t.Errorf("failed = %+v (analyses %d)", r, f.analyses)
	}

	f = ready()
	f.cleanupRestart = true
	if r := componentResult(t, f, context.Background()); r.Status != optimize.Done || !r.RestartRequired ||
		!strings.Contains(r.Message, "Windows needs a restart to finish it") {
		t.Errorf("restart = %+v", r)
	}

	// The store cannot be measured afterwards: done, nothing claimed as freed.
	f = ready()
	f.afterErr = fmt.Errorf("%w: no line", optimize.ErrUnreadableReport)
	if r := componentResult(t, f, context.Background()); r.Status != optimize.Done || r.Freed != 0 || r.BytesAfter != -1 ||
		r.ComponentStore != nil || !strings.Contains(r.Message, "could not be measured") {
		t.Errorf("unmeasured = %+v", r)
	}
}

// Ticked in the list without a plan-time analysis: Run analyzes first.
func TestRunComponentStoreTickedWithoutAnalysis(t *testing.T) {
	f := ready()
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks(), nil)
	for i := range plan {
		plan[i].Selected = plan[i].Task.ID == optimize.TaskComponentStore
	}
	res := optimize.Run(context.Background(), f, plan)
	if len(res) != 1 || f.analyses != 2 || f.cleanups != 1 || f.flushed+f.cleared+len(f.trimmed) != 0 {
		t.Fatalf("results = %+v (analyses %d, cleanups %d)", res, f.analyses, f.cleanups)
	}
	if r := res[0]; r.Status != optimize.Done || r.BytesBefore != 10<<30 || r.Freed != 3<<30 {
		t.Errorf("ticked = %+v", r)
	}

	// Ticked, but DISM's fresh analysis does not recommend a cleanup.
	f = ready()
	f.cs.Recommended = false
	res = optimize.Run(context.Background(), f, plan)
	if r := res[0]; r.Status != optimize.Skipped || f.cleanups != 0 || r.Freed != 0 || r.ComponentStore == nil ||
		r.BytesBefore != 10<<30 || r.BytesAfter != 10<<30 || !strings.Contains(r.Message, "DISM does not recommend it now (3 reclaimable packages)") {
		t.Errorf("not recommended = %+v (cleanups %d)", r, f.cleanups)
	}
}

// A plan-time recommendation is never trusted on its own.
func TestRunComponentStoreRechecksBeforeActing(t *testing.T) {
	f := ready()
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks(), named)
	if it := statuses(plan)[optimize.TaskComponentStore]; !it.Selected || !it.ComponentStore.Recommended {
		t.Fatalf("plan = %+v", it)
	}
	f.cs.Recommended = false // Windows' own scheduled cleanup ran in between
	res := optimize.Run(context.Background(), f, plan)
	if r := res[3]; r.Status != optimize.Skipped || f.cleanups != 0 || !strings.Contains(r.Message, "the cleanup was not run") {
		t.Errorf("not recommended any more = %+v", r)
	}

	f = ready()
	plan = optimize.Plan(context.Background(), f, true, optimize.Tasks(), named)
	f.cs, f.csErr = nil, fmt.Errorf("%w: no line", optimize.ErrUnreadableReport)
	res = optimize.Run(context.Background(), f, plan)
	if r := res[3]; r.Status != optimize.Failed || f.cleanups != 0 || r.BytesBefore != -1 ||
		!strings.Contains(r.Message, "the cleanup was not run: DISM's analysis right before it failed") {
		t.Errorf("analysis failed = %+v", r)
	}

	// Ctrl+C during that analysis: nothing is changed and nothing else starts.
	ctx, cancel := context.WithCancel(context.Background())
	f = ready()
	plan = optimize.Plan(context.Background(), f, true, optimize.Tasks(), named)
	plan = []optimize.Item{plan[3], plan[0]}
	f.cancelInAnalysis, f.cancel = true, cancel
	res = optimize.Run(ctx, f, plan)
	if res[0].Status != optimize.Cancelled || res[1].Status != optimize.Cancelled || f.cleanups+f.flushed != 0 {
		t.Errorf("cancelled analysis = %+v", res)
	}
}

// Ctrl+C while DISM cleans up: the cleanup is left to finish and reported as
// done, nothing is measured or started afterwards.
func TestRunComponentStoreCancelLeavesDISMToFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := ready()
	f.cancelInCleanup, f.cancel = true, cancel
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks(), named)
	// The component store first, then the others.
	plan = []optimize.Item{plan[3], plan[0], plan[1], plan[2]}
	res := optimize.Run(ctx, f, plan)
	if f.cleanups != 1 || !f.cleanupSawCancelled || f.analyses != 2 || f.flushed+f.cleared+len(f.trimmed) != 0 {
		t.Fatalf("cleanups=%d analyses=%d flushed=%d cleared=%d trimmed=%v", f.cleanups, f.analyses, f.flushed, f.cleared, f.trimmed)
	}
	r := res[0]
	if r.Status != optimize.Done || r.Freed != 0 || r.BytesAfter != -1 ||
		!strings.Contains(r.Message, "left to finish after the cancel (stopping it midway is not safe)") {
		t.Errorf("component store = %+v", r)
	}
	for _, r := range res[1:] {
		if r.Status != optimize.Cancelled {
			t.Errorf("after cancel: %+v", r)
		}
	}
}

func TestPlanElevatedStatuses(t *testing.T) {
	f := ready()
	plan := statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks(), nil))
	for _, it := range plan {
		// Every ready task is preselected except the opt-in one.
		if it.Status != optimize.Ready || it.Selected == it.Task.OptIn {
			t.Errorf("%s = %+v", it.Task.ID, it)
		}
	}
	if plan[optimize.TaskDO].BytesBefore != 300<<20 || len(plan[optimize.TaskReTrim].Volumes) != 1 {
		t.Errorf("plan details = %+v", plan)
	}

	f = &fake{doAvailable: true, doBytes: 1000}
	plan = statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks(), nil))
	if it := plan[optimize.TaskDO]; it.Status != optimize.NotApplicable || it.Selected {
		t.Errorf("empty cache = %+v", it)
	}
	if it := plan[optimize.TaskReTrim]; it.Status != optimize.NotApplicable {
		t.Errorf("no SSDs = %+v", it)
	}
	f = &fake{doAvailable: false, ssdErr: errors.New("boom")}
	plan = statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks(), nil))
	if plan[optimize.TaskDO].Status != optimize.Unavailable || plan[optimize.TaskReTrim].Status != optimize.Unavailable {
		t.Errorf("unavailable = %+v", plan)
	}
}

func TestRunOnlySelectedReadyTasks(t *testing.T) {
	f := ready()
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks(), nil)
	for i := range plan {
		if plan[i].Task.ID == optimize.TaskReTrim || plan[i].Task.ID == optimize.TaskComponentStore {
			plan[i].Selected = false
		}
	}
	res := optimize.Run(context.Background(), f, plan)
	if len(res) != 2 || f.flushed != 1 || f.cleared != 1 || len(f.trimmed) != 0 || f.cleanups != 0 {
		t.Fatalf("results = %+v, flushed=%d cleared=%d trimmed=%v", res, f.flushed, f.cleared, f.trimmed)
	}
	do := res[1]
	if do.Status != optimize.Done || do.BytesBefore != 300<<20 || do.BytesAfter != 0 || do.Freed != 300<<20 {
		t.Errorf("delivery optimization = %+v", do)
	}

	// Not-ready items never run, even if marked selected.
	f = ready()
	plan = optimize.Plan(context.Background(), f, false, optimize.Tasks(), nil)
	for i := range plan {
		plan[i].Selected = true
	}
	res = optimize.Run(context.Background(), f, plan)
	if len(res) != 1 || f.cleared != 0 || len(f.trimmed) != 0 || f.cleanups != 0 || f.analyses != 0 {
		t.Errorf("admin tasks ran without elevation: %+v", res)
	}
}

func TestRunFailuresAndPartialRetrim(t *testing.T) {
	f := ready()
	f.flushErr = errors.New("DNS Client stopped")
	f.clearErr = errors.New("cmdlet failed")
	f.ssds = []optimize.Volume{{Root: `C:\`}, {Root: `D:\`}}
	f.trimErr = map[string]error{`D:\`: errors.New("not supported")}
	f.cleanupErr = errors.New("DISM error 0x800f0806")
	res := optimize.Run(context.Background(), f, optimize.Plan(context.Background(), f, true, optimize.Tasks(), optimize.ShortIDs()))
	got := map[string]optimize.Result{}
	for _, r := range res {
		got[r.ID] = r
	}
	if r := got[optimize.TaskDNS]; r.Status != optimize.Failed || !strings.Contains(r.Message, "DNS Client") {
		t.Errorf("dns = %+v", r)
	}
	if r := got[optimize.TaskDO]; r.Status != optimize.Failed || r.Freed != 0 {
		t.Errorf("do = %+v", r)
	}
	if r := got[optimize.TaskReTrim]; r.Status != optimize.Partial || !strings.Contains(r.Message, `retrimmed C:\`) ||
		!strings.Contains(r.Message, `D:\ failed`) {
		t.Errorf("retrim = %+v", r)
	}
	if r := got[optimize.TaskComponentStore]; r.Status != optimize.Failed || r.Message != "DISM error 0x800f0806" {
		t.Errorf("component store = %+v", r)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := ready()
	f.ssds = []optimize.Volume{{Root: `C:\`}, {Root: `D:\`}, {Root: `E:\`}}
	f.cancelAfter, f.cancel = 1, cancel
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks(), nil)
	// Retrim first, then the others.
	plan = []optimize.Item{plan[2], plan[0], plan[1]}
	res := optimize.Run(ctx, f, plan)
	if len(f.trimmed) != 1 || f.flushed != 0 || f.cleared != 0 || f.cleanups != 0 {
		t.Fatalf("ran after cancel: trimmed=%v flushed=%d cleared=%d cleanups=%d", f.trimmed, f.flushed, f.cleared, f.cleanups)
	}
	if res[0].Status != optimize.Partial || res[1].Status != optimize.Cancelled || res[2].Status != optimize.Cancelled {
		t.Errorf("results = %+v", res)
	}
}

func TestSelect(t *testing.T) {
	for _, c := range []struct {
		filters []string
		want    int
		err     bool
	}{
		{nil, 4, false},
		{[]string{"dns-flush"}, 1, false},
		{[]string{"optimize.ssd-retrim", "delivery-optimization"}, 2, false},
		{[]string{"component-store"}, 1, false},
		{[]string{"optimize"}, 4, false},
		{[]string{"nope"}, 0, true},
	} {
		got, err := optimize.Select(c.filters)
		if (err != nil) != c.err || len(got) != c.want {
			t.Errorf("Select(%v) = %d tasks, %v", c.filters, len(got), err)
		}
	}
}

// Read-only: lists SSD volumes and the cache state; never runs a task.
func TestRealSystemReadOnly(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	s := optimize.System{}
	vols, err := s.SSDVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := s.DeliveryOptimization(context.Background(), false)
	t.Logf("SSD volumes: %+v; Delivery Optimization: %+v", vols, c)
	if c.Bytes != -1 {
		t.Error("the cache must not be measured without elevation")
	}
}

// Read-only: DISM's analysis on the real system. Without elevation DISM
// refuses at once (error 740, no prompt); elevated (CI) its real English
// report must parse. The cleanup is never run here.
func TestRealSystemComponentStoreAnalysis(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	cs, err := optimize.System{}.AnalyzeComponentStore(ctx)
	switch {
	case !system.IsElevated():
		if err == nil || !strings.Contains(err.Error(), "DISM error 740") {
			t.Errorf("not elevated: %+v, %v", cs, err)
		}
	case ctx.Err() != nil:
		t.Skip("the analysis did not finish within 7 minutes (DISM was left to finish)")
	case err != nil:
		t.Fatalf("DISM's real report did not parse: %v", err)
	default:
		t.Logf("component store: %+v (overhead %d bytes)", cs, cs.OverheadBytes())
		if cs.ActualBytes <= 0 || cs.ExplorerBytes < cs.ActualBytes/2 {
			t.Errorf("implausible report: %+v", cs)
		}
	}
}
