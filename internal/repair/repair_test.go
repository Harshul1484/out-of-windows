package repair_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/repair"
	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// paths is an in-memory envpath.Store.
type paths struct {
	user, machine envpath.Value
	writes        int
	broadcasts    int
	writeErr      error
	changeBefore  bool // another program edits the PATH right before oow writes
}

func (p *paths) Read(s envpath.Scope) (envpath.Value, error) {
	if s == envpath.Machine {
		return p.machine, nil
	}
	return p.user, nil
}

func (p *paths) WriteUser(expected, updated envpath.Value) error {
	if p.writeErr != nil {
		return p.writeErr
	}
	if p.changeBefore {
		p.user.Raw += `;C:\NewTool`
	}
	if p.user != expected {
		return envpath.ErrChanged
	}
	p.user = updated
	p.writes++
	return nil
}

func (p *paths) Broadcast() error               { p.broadcasts++; return nil }
func (p *paths) Expand(s string) (string, bool) { return s, !strings.Contains(s, "%") }

// entries is an in-memory startup.Store.
type entries struct {
	raws    []startup.Raw
	written map[string][]byte
}

func (e *entries) List(context.Context) ([]startup.Entry, []string, error) {
	var raws []startup.Raw
	for _, r := range e.raws {
		if b, ok := e.written[startup.ID(r.Source, r.Name)]; ok {
			r.Approval = b
		}
		raws = append(raws, r)
	}
	return startup.Build(raws, startup.Resolver{Probe: system.ProbePath}), nil, nil
}

func (e *entries) SetApproval(en startup.Entry, data []byte) error {
	e.written[en.ID] = data
	return nil
}

type world struct {
	f       *testutil.Fixture
	paths   *paths
	entries *entries
	user    envpath.Report
	machine envpath.Report
	list    []startup.Entry
}

func newWorld(t *testing.T) *world {
	f := testutil.NewFixture(t)
	f.Dir(`Tools\bin`, time.Hour)
	f.Dir(`Users\me`, time.Hour)
	f.File(`Apps\present.exe`, 10, time.Hour)
	userRaw := strings.Join([]string{
		f.Path(`Tools\bin`),       // 0 ok
		f.Path(`Gone\bin`),        // 1 missing: preselected
		f.Path(`Users\me\go\bin`), // 2 missing in profile: review
		"",                        // 3 empty
		f.Path(`Tools\bin`) + `\`, // 4 duplicate
		`\\server\share\bin`,      // 5 unknown: never offered
	}, ";")
	w := &world{f: f,
		paths: &paths{
			user:    envpath.Value{Raw: userRaw, Expand: true, Exists: true},
			machine: envpath.Value{Raw: f.Path(`Tools\bin`) + ";" + f.Path(`Old\bin`), Expand: true, Exists: true},
		},
		entries: &entries{written: map[string][]byte{}, raws: []startup.Raw{
			{Source: startup.SourceHKCURun, Name: "Present", Command: `"` + f.Path(`Apps\present.exe`) + `"`},
			{Source: startup.SourceHKCURun, Name: "Gone", Command: `"` + f.Path(`Apps\gone.exe`) + `" /bg`},
			{Source: startup.SourceHKLMRun, Name: "Machine Gone", Command: f.Path(`Apps\machine.exe`)},
			{Source: startup.SourceHKCURun, Name: "Already Off", Command: f.Path(`Apps\off.exe`),
				Approval: startup.ApprovalData(false, time.Now())},
			{Source: startup.SourceHKCURunOnce, Name: "Once", Command: f.Path(`Apps\once.exe`)},
		}},
	}
	opts := envpath.Options{Expand: w.paths.Expand, Probe: system.ProbePath, Profile: f.Path(`Users\me`)}
	w.user = envpath.Analyze(envpath.User, w.paths.user, opts)
	w.machine = envpath.Analyze(envpath.Machine, w.paths.machine, opts)
	w.list, _, _ = w.entries.List(context.Background())
	return w
}

func (w *world) env(elevated bool) repair.Env {
	return repair.Env{Paths: w.paths, Startup: w.entries, BackupDir: w.f.Path("backups"), Elevated: elevated,
		Now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
}

func fixByID(p repair.Plan) map[string]repair.Fix {
	m := map[string]repair.Fix{}
	for _, f := range p.Fixes {
		m[f.ID] = f
	}
	return m
}

func TestPlanSelection(t *testing.T) {
	w := newWorld(t)
	p := repair.NewPlan(&w.user, &w.machine, w.list, false)
	fx := fixByID(p)
	for id, selected := range map[string]bool{
		"path.user:1":                   true,
		"path.user:2":                   false, // inside the profile
		"path.user:3":                   true,
		"path.user:4":                   true,
		"startup:hkcu-run:Gone":         true,
		"startup:hklm-run:Machine Gone": false, // needs administrator
	} {
		f, ok := fx[id]
		if !ok {
			t.Errorf("fix %s missing (have %v)", id, p.Fixes)
			continue
		}
		if f.Selected != selected {
			t.Errorf("%s selected = %v, want %v (%s)", id, f.Selected, selected, f.Review)
		}
		if !f.Selected && f.Review == "" {
			t.Errorf("%s is not preselected without saying why", id)
		}
	}
	for _, id := range []string{"path.user:0", "path.user:5", "startup:hkcu-run:Present", "startup:hkcu-run:Already Off", "startup:hkcu-runonce:Once"} {
		if _, ok := fx[id]; ok {
			t.Errorf("%s must not be offered", id)
		}
	}
	if len(p.NotFixed) != 1 || !strings.Contains(p.NotFixed[0], "system PATH") {
		t.Errorf("not fixed = %v", p.NotFixed)
	}
	if fx["startup:hklm-run:Machine Gone"].Review != startup.ReasonNeedsAdmin {
		t.Error("admin review reason")
	}
	// Elevated: the machine startup entry is preselected.
	if !fixByID(repair.NewPlan(&w.user, &w.machine, w.list, true))["startup:hklm-run:Machine Gone"].Selected {
		t.Error("machine entry not preselected when elevated")
	}
}

func selected(p repair.Plan) []repair.Fix {
	var out []repair.Fix
	for _, f := range p.Fixes {
		if f.Selected {
			out = append(out, f)
		}
	}
	return out
}

func TestApplyBacksUpThenRewritesOnlyChosenEntries(t *testing.T) {
	w := newWorld(t)
	before := w.paths.user
	machineBefore := w.paths.machine
	p := repair.NewPlan(&w.user, &w.machine, w.list, false)
	out := repair.Apply(context.Background(), w.env(false), p, selected(p))
	if out.Failed != 0 || out.Fixed != 4 || out.Skipped != 0 {
		t.Fatalf("outcome = %+v", out)
	}
	want := envpath.Remove(before, []int{1, 3, 4})
	if w.paths.user != want || w.paths.writes != 1 || w.paths.broadcasts != 1 {
		t.Errorf("user PATH = %q (writes %d, broadcasts %d), want %q", w.paths.user.Raw, w.paths.writes, w.paths.broadcasts, want.Raw)
	}
	if !strings.Contains(w.paths.user.Raw, `go\bin`) || !strings.Contains(w.paths.user.Raw, `\\server\share\bin`) {
		t.Error("review and unknown entries were removed")
	}
	if w.paths.machine != machineBefore {
		t.Error("the machine PATH changed")
	}
	data, err := os.ReadFile(out.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := envpath.ParseRegFile(data); err != nil || got != before {
		t.Errorf("backup = %+v, %v; want the value before the change", got, err)
	}
	if _, ok := w.entries.written["hkcu-run:Gone"]; !ok {
		t.Error("broken startup entry not disabled")
	}
	if _, ok := w.entries.written["hklm-run:Machine Gone"]; ok {
		t.Error("machine entry changed without elevation")
	}
}

func TestApplyRefusesWhenPathChanged(t *testing.T) {
	w := newWorld(t)
	p := repair.NewPlan(&w.user, &w.machine, w.list, false)
	w.paths.user.Raw += `;C:\Installed\Later`
	out := repair.Apply(context.Background(), w.env(false), p, selected(p))
	for _, r := range out.Results {
		if r.Fix.Kind == repair.KindPath && (r.Status != repair.Skipped || !strings.Contains(r.Reason, "changed")) {
			t.Errorf("path fix = %+v", r)
		}
	}
	if w.paths.writes != 0 || out.Backup != "" {
		t.Error("PATH written although it changed after analysis")
	}

	// Changed between the backup and the write (compare-and-swap).
	w = newWorld(t)
	p = repair.NewPlan(&w.user, &w.machine, w.list, false)
	w.paths.changeBefore = true
	out = repair.Apply(context.Background(), w.env(false), p, selected(p))
	if w.paths.writes != 0 || out.Skipped < 3 {
		t.Errorf("concurrent change: writes=%d outcome=%+v", w.paths.writes, out)
	}
}

func TestApplyWriteFailure(t *testing.T) {
	w := newWorld(t)
	p := repair.NewPlan(&w.user, &w.machine, w.list, false)
	w.paths.writeErr = errors.New("access denied")
	out := repair.Apply(context.Background(), w.env(false), p, selected(p))
	if out.Failed != 3 || out.Backup == "" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestApplyCancelled(t *testing.T) {
	w := newWorld(t)
	p := repair.NewPlan(&w.user, &w.machine, w.list, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := repair.Apply(ctx, w.env(true), p, selected(p))
	if w.paths.writes != 0 || len(w.entries.written) != 0 || out.Fixed != 0 {
		t.Errorf("changed after cancel: %+v", out)
	}
}

func TestQuotedPathIsNotRewritten(t *testing.T) {
	w := newWorld(t)
	w.paths.user.Raw = `"C:\Quoted;Dir";` + w.f.Path(`Gone\bin`)
	opts := envpath.Options{Expand: w.paths.Expand, Probe: system.ProbePath}
	u := envpath.Analyze(envpath.User, w.paths.user, opts)
	p := repair.NewPlan(&u, nil, nil, false)
	if len(p.Fixes) != 0 || len(p.NotFixed) != 1 || !strings.Contains(p.NotFixed[0], "quoted") {
		t.Errorf("plan = %+v", p)
	}
}
