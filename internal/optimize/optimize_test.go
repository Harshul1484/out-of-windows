package optimize_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/optimize"
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

	flushed, cleared int
	trimmed          []string
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
	return &fake{doAvailable: true, doBytes: 300 << 20, ssds: []optimize.Volume{{Root: `C:\`, FileSystem: "NTFS"}}}
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

func TestPlanNotElevated(t *testing.T) {
	f := ready()
	plan := statuses(optimize.Plan(context.Background(), f, false, optimize.Tasks()))
	if it := plan[optimize.TaskDNS]; it.Status != optimize.Ready || !it.Selected {
		t.Errorf("dns = %+v", it)
	}
	for _, id := range []string{optimize.TaskDO, optimize.TaskReTrim} {
		it := plan[id]
		if it.Status != optimize.NeedsAdmin || it.Selected || !strings.Contains(it.Reason, "elevated terminal") {
			t.Errorf("%s = %+v", id, it)
		}
	}
	if plan[optimize.TaskDO].BytesBefore != -1 {
		t.Error("cache size must be unknown without administrator rights")
	}
	if f.flushed+f.cleared+len(f.trimmed) != 0 {
		t.Fatal("Plan ran something")
	}
}

func TestPlanElevatedStatuses(t *testing.T) {
	f := ready()
	plan := statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks()))
	for _, it := range plan {
		if it.Status != optimize.Ready || !it.Selected {
			t.Errorf("%s = %+v", it.Task.ID, it)
		}
	}
	if plan[optimize.TaskDO].BytesBefore != 300<<20 || len(plan[optimize.TaskReTrim].Volumes) != 1 {
		t.Errorf("plan details = %+v", plan)
	}

	f = &fake{doAvailable: true, doBytes: 1000}
	plan = statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks()))
	if it := plan[optimize.TaskDO]; it.Status != optimize.NotApplicable || it.Selected {
		t.Errorf("empty cache = %+v", it)
	}
	if it := plan[optimize.TaskReTrim]; it.Status != optimize.NotApplicable {
		t.Errorf("no SSDs = %+v", it)
	}
	f = &fake{doAvailable: false, ssdErr: errors.New("boom")}
	plan = statuses(optimize.Plan(context.Background(), f, true, optimize.Tasks()))
	if plan[optimize.TaskDO].Status != optimize.Unavailable || plan[optimize.TaskReTrim].Status != optimize.Unavailable {
		t.Errorf("unavailable = %+v", plan)
	}
}

func TestRunOnlySelectedReadyTasks(t *testing.T) {
	f := ready()
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks())
	for i := range plan {
		if plan[i].Task.ID == optimize.TaskReTrim {
			plan[i].Selected = false
		}
	}
	res := optimize.Run(context.Background(), f, plan)
	if len(res) != 2 || f.flushed != 1 || f.cleared != 1 || len(f.trimmed) != 0 {
		t.Fatalf("results = %+v, flushed=%d cleared=%d trimmed=%v", res, f.flushed, f.cleared, f.trimmed)
	}
	do := res[1]
	if do.Status != optimize.Done || do.BytesBefore != 300<<20 || do.BytesAfter != 0 || do.Freed != 300<<20 {
		t.Errorf("delivery optimization = %+v", do)
	}

	// Not-ready items never run, even if marked selected.
	f = ready()
	plan = optimize.Plan(context.Background(), f, false, optimize.Tasks())
	for i := range plan {
		plan[i].Selected = true
	}
	res = optimize.Run(context.Background(), f, plan)
	if len(res) != 1 || f.cleared != 0 || len(f.trimmed) != 0 {
		t.Errorf("admin tasks ran without elevation: %+v", res)
	}
}

func TestRunFailuresAndPartialRetrim(t *testing.T) {
	f := ready()
	f.flushErr = errors.New("DNS Client stopped")
	f.clearErr = errors.New("cmdlet failed")
	f.ssds = []optimize.Volume{{Root: `C:\`}, {Root: `D:\`}}
	f.trimErr = map[string]error{`D:\`: errors.New("not supported")}
	res := optimize.Run(context.Background(), f, optimize.Plan(context.Background(), f, true, optimize.Tasks()))
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
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := ready()
	f.ssds = []optimize.Volume{{Root: `C:\`}, {Root: `D:\`}, {Root: `E:\`}}
	f.cancelAfter, f.cancel = 1, cancel
	plan := optimize.Plan(context.Background(), f, true, optimize.Tasks())
	// Retrim first, then the others.
	plan = []optimize.Item{plan[2], plan[0], plan[1]}
	res := optimize.Run(ctx, f, plan)
	if len(f.trimmed) != 1 || f.flushed != 0 || f.cleared != 0 {
		t.Fatalf("ran after cancel: trimmed=%v flushed=%d cleared=%d", f.trimmed, f.flushed, f.cleared)
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
		{nil, 3, false},
		{[]string{"dns-flush"}, 1, false},
		{[]string{"optimize.ssd-retrim", "delivery-optimization"}, 2, false},
		{[]string{"optimize"}, 3, false},
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
