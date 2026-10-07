package startup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/tasks"
)

var alice = tasks.Account{SID: "S-1-5-21-1-2-3-1001", Name: "alice", Domain: "PC"}

// fakeTasks is an in-memory tasks.Store.
type fakeTasks struct {
	list []tasks.Task
	err  error
}

func (f fakeTasks) List(context.Context) ([]tasks.Task, []string, error) {
	return f.list, []string{"1 scheduled task folder could not be read"}, f.err
}
func (f fakeTasks) SetEnabled(string, bool) error { return errors.New("not used") }
func (f fakeTasks) Account() tasks.Account        { return alice }

func logonTask(path, user, exe string) tasks.Task {
	return tasks.Task{Path: path, Enabled: true, Triggers: []tasks.Trigger{tasks.TriggerLogon}, UserID: user, LogonUser: user,
		Actions: []tasks.Action{{Command: exe, Arguments: "/background"}}}
}

func TestTaskRawsSelectSignInAndStartupTasks(t *testing.T) {
	boot := tasks.Task{Path: `\Wingtip\Boot Check`, Enabled: true, Triggers: []tasks.Trigger{tasks.TriggerBoot}, UserID: "S-1-5-18",
		Actions: []tasks.Action{{Command: `C:\App\app.exe`}}}
	both := logonTask(`\Both`, "alice", `C:\App\app.exe`)
	both.Triggers = append(both.Triggers, tasks.TriggerBoot)
	daily := tasks.Task{Path: `\Contoso\Daily`, Enabled: true, Triggers: []tasks.Trigger{tasks.TriggerCalendar}, Actions: []tasks.Action{{Command: `C:\App\app.exe`}}}
	windows := logonTask(`\Microsoft\Windows\Shell\FamilySafetyMonitor`, "", `C:\Windows\System32\wpcmon.exe`)
	raws, warnings := startup.TaskRaws(context.Background(), fakeTasks{list: []tasks.Task{
		logonTask(`\Contoso Updater`, "alice", `C:\App\app.exe`), boot, both, daily, windows}})
	got := map[string]startup.Raw{}
	for _, r := range raws {
		got[r.Name] = r
	}
	if len(raws) != 3 || got["Contoso Updater"].Source != startup.SourceTaskLogon || !got["Contoso Updater"].TaskOwned ||
		got[`Wingtip\Boot Check`].Source != startup.SourceTaskBoot || got[`Wingtip\Boot Check`].TaskOwned ||
		got["Both"].Source != startup.SourceTaskLogon || len(warnings) != 1 {
		t.Errorf("raws = %+v, warnings %v (time-triggered and Windows tasks must be left out)", raws, warnings)
	}
	if _, w := startup.TaskRaws(context.Background(), fakeTasks{err: errors.New("service stopped")}); len(w) != 1 ||
		!strings.Contains(w[0], "could not read scheduled tasks") {
		t.Errorf("unreadable library: %v", w)
	}
}

func TestBuildTaskEntries(t *testing.T) {
	r := startup.Resolver{
		Expand: func(s string) string { return strings.ReplaceAll(s, "%LOCALAPPDATA%", `C:\Users\alice\AppData\Local`) },
		Probe:  fakeProbe(`C:\App\app.exe`),
	}
	owned := logonTask(`\Gone Updater`, "alice", `%LOCALAPPDATA%\Gone\gone.exe`)
	sys := logonTask(`\Wingtip\Logon Check`, "S-1-5-18", `C:\App\app.exe`)
	sys.LogonUser = ""
	off := logonTask(`\Off`, "alice", `C:\App\app.exe`)
	off.Enabled = false
	com := logonTask(`\Com Only`, "alice", "")
	com.Actions, com.OtherActions = []tasks.Action{}, 1
	other := logonTask(`\Bob Sync`, "bob", `%LOCALAPPDATA%\Sync\sync.exe`)
	raws, _ := startup.TaskRaws(context.Background(), fakeTasks{list: []tasks.Task{owned, sys, off, com, other}})
	got := map[string]startup.Entry{}
	for _, e := range startup.Build(raws, r) {
		got[e.Name] = e
	}
	check := func(name string, state startup.State, target system.Presence, broken, admin bool, scope string) {
		t.Helper()
		e, ok := got[name]
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if e.State != state || e.TargetState != target || e.Broken() != broken || e.NeedsAdmin != admin || e.Scope != scope ||
			!e.Toggleable || e.Task == nil || e.Approval != nil || e.Location != e.Task.Path {
			t.Errorf("%s = %+v", name, e)
		}
	}
	check("Gone Updater", startup.Enabled, system.Absent, true, false, "user")
	check(`Wingtip\Logon Check`, startup.Enabled, system.Present, false, true, "machine")
	check("Off", startup.Disabled, system.Present, false, false, "user")
	check("Com Only", startup.Enabled, system.Unknown, false, false, "user")
	// Another account's %LOCALAPPDATA% is not ours: never called missing.
	check("Bob Sync", startup.Enabled, system.Unknown, false, true, "machine")
	if e := got["Gone Updater"]; e.ID != `task-logon:Gone Updater` || e.Target != `C:\Users\alice\AppData\Local\Gone\gone.exe` ||
		e.Command != `%LOCALAPPDATA%\Gone\gone.exe /background` || e.SourceLabel != "Scheduled task, at sign-in" {
		t.Errorf("owned task = %+v", e)
	}
	if e := got[`Wingtip\Logon Check`]; e.Task.RunAs != "SYSTEM" || len(e.Task.Triggers) != 1 {
		t.Errorf("system task info = %+v", e.Task)
	}
}

func TestSetEnabledTasks(t *testing.T) {
	owned := logonTask(`\Gone Updater`, "alice", `C:\Gone\gone.exe`)
	machine := logonTask(`\Wingtip\Logon Check`, "S-1-5-18", `C:\App\app.exe`)
	newStore := func() *memStore {
		o, m := owned, machine
		raws, _ := startup.TaskRaws(context.Background(), fakeTasks{list: []tasks.Task{o, m}})
		return &memStore{written: map[string][]byte{}, raws: raws}
	}
	s := newStore()
	entries, _, _ := s.List(context.Background())
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	res := startup.SetEnabled(context.Background(), s, entries, false, false, now)
	byName := map[string]startup.Result{}
	for _, r := range res {
		byName[r.Entry.Name] = r
	}
	if r := byName["Gone Updater"]; r.Status != startup.StatusChanged || r.Entry.State != startup.Disabled ||
		r.Before != "" || r.After != "" || r.TaskEnabledBefore == nil || !*r.TaskEnabledBefore {
		t.Errorf("owned task = %+v", r)
	}
	if r := byName[`Wingtip\Logon Check`]; r.Status != startup.StatusSkipped || r.Reason != startup.ReasonTaskNeedsAdmin ||
		!startup.IsAdminReason(r.Reason) || !strings.Contains(r.Reason, "elevated terminal") {
		t.Errorf("machine task = %+v", r)
	}
	if s.taskWrites != 1 || len(s.written) != 0 {
		t.Errorf("writes: %d task, %d approval (a task must never get a StartupApproved value)", s.taskWrites, len(s.written))
	}
	// Enabling restores it; elevated, the machine task changes too.
	entries, _, _ = s.List(context.Background())
	res = startup.SetEnabled(context.Background(), s, entries, true, true, now)
	for _, r := range res {
		if r.Entry.Name == "Gone Updater" && (r.Status != startup.StatusChanged || r.Entry.State != startup.Enabled || *r.TaskEnabledBefore) {
			t.Errorf("enable = %+v", r)
		}
		if r.Entry.Name == `Wingtip\Logon Check` && r.Status != startup.StatusUnchanged {
			t.Errorf("already enabled machine task = %+v", r)
		}
	}

	// A store that does not really write is caught by the verification.
	s = newStore()
	s.lie = true
	entries, _, _ = s.List(context.Background())
	res = startup.SetEnabled(context.Background(), s, entries[:1], false, true, now)
	if res[0].Status != startup.StatusFailed || !strings.Contains(res[0].Reason, "still reports it enabled") {
		t.Errorf("unverified task write = %+v", res[0])
	}
	// A task deleted after listing is reported, not recreated.
	s = newStore()
	entries, _, _ = s.List(context.Background())
	s.raws = s.raws[1:]
	res = startup.SetEnabled(context.Background(), s, entries[:1], false, true, now)
	if res[0].Status != startup.StatusFailed || !strings.Contains(res[0].Reason, "no longer exists") {
		t.Errorf("gone task = %+v", res[0])
	}
}

func TestTaskWriteError(t *testing.T) {
	if !errors.Is(startup.TaskWriteError(tasks.ErrNotFound), startup.ErrEntryGone) {
		t.Error("ErrNotFound is not ErrEntryGone")
	}
	if err := startup.TaskWriteError(tasks.ErrAccessDenied); !errors.Is(err, tasks.ErrAccessDenied) ||
		!strings.Contains(err.Error(), "elevated terminal") {
		t.Errorf("access denied = %v", err)
	}
	if startup.TaskWriteError(nil) != nil {
		t.Error("nil error mapped")
	}
}
