package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/optimize"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// state snapshots everything the startup, optimize and repair commands can
// change in the sandbox: the simulated drive and the simulated registry.
func (e *env) state() map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for _, dir := range []string{"C", "registry"} {
		for k, v := range testutil.SnapshotDir(e.t, filepath.Join(e.root, dir)) {
			out[dir+`\`+k] = v
		}
	}
	return out
}

func (e *env) assertUnchanged(before map[string]string, what string) {
	e.t.Helper()
	after := e.state()
	if len(after) != len(before) {
		e.t.Fatalf("%s changed the sandbox (%d -> %d items)", what, len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			e.t.Errorf("%s changed %s", what, k)
		}
	}
}

func (e *env) registry(name string, v any) {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.root, "registry", name))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		e.t.Fatal(err)
	}
}

type startupDoc struct {
	Schema  string `json:"schema"`
	Entries []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		State       string `json:"state"`
		TargetState string `json:"target_state"`
		Toggleable  bool   `json:"toggleable"`
		NeedsAdmin  bool   `json:"needs_admin"`
		Approval    *struct {
			Data string `json:"data"`
		} `json:"approval"`
		Task *struct {
			Path  string `json:"path"`
			RunAs string `json:"run_as"`
		} `json:"task"`
	} `json:"entries"`
	Summary struct {
		Total, Enabled, Disabled, Broken int
		RunsOnce                         int `json:"runs_once"`
	} `json:"summary"`
	Warnings []string `json:"warnings"`
}

type changeDoc struct {
	Schema  string `json:"schema"`
	Action  string `json:"action"`
	DryRun  bool   `json:"dry_run"`
	Results []struct {
		Status            string `json:"status"`
		Reason            string `json:"reason"`
		Before            string `json:"before"`
		After             string `json:"after"`
		TaskEnabledBefore *bool  `json:"task_enabled_before"`
		Entry             struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"entry"`
	} `json:"results"`
}

func (e *env) startupState(name string) string {
	e.t.Helper()
	out, _, code := e.run("startup", "--json")
	if code != 0 {
		e.t.Fatalf("startup --json: code %d", code)
	}
	for _, en := range decode[startupDoc](e.t, out).Entries {
		if en.Name == name {
			return en.State
		}
	}
	e.t.Fatalf("no startup entry %s", name)
	return ""
}

func TestStartupListJSON(t *testing.T) {
	e := newEnv(t)
	before := e.state()
	out, _, code := e.run("startup", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	d := decode[startupDoc](t, out)
	s := d.Summary
	if d.Schema != "oow.startup/v1" || s.Total != 14 || s.Enabled != 11 || s.Disabled != 2 || s.RunsOnce != 1 || s.Broken != 4 ||
		d.Warnings == nil {
		t.Errorf("doc = %+v", d)
	}
	states := map[string]string{}
	for _, en := range d.Entries {
		states[en.Name] = en.State + "/" + en.TargetState
	}
	for name, want := range map[string]string{
		"Contoso Agent":    "enabled/found",
		"Fabrikam Updater": "enabled/missing",
		"Old Sync Helper":  "disabled/missing",
		"Text Services":    "enabled/found", // bare name found in System32
		"Cloud Drive":      "enabled/unknown",
		"Setup Cleanup":    "runs-once/missing",
		"Wingtip Updater":  "enabled/found", // %ProgramFiles(x86)% expanded
		"Litware Tray":     "enabled/missing",
		"Old Notes.lnk":    "enabled/missing",
		"backup.cmd":       "disabled/found",
		// Scheduled tasks: the user's own sign-in task, and one running as SYSTEM.
		"Tailspin Sync":                    "enabled/missing",
		`Wingtip Toys\Wingtip Logon Check`: "enabled/found",
	} {
		if states[name] != want {
			t.Errorf("%s = %q, want %q", name, states[name], want)
		}
	}
	for _, en := range d.Entries {
		switch en.Name {
		case "Tailspin Sync":
			if en.ID != "task-logon:Tailspin Sync" || !en.Toggleable || en.NeedsAdmin || en.Approval != nil ||
				en.Task == nil || en.Task.Path != `\Tailspin Sync` {
				t.Errorf("user task = %+v", en)
			}
		case `Wingtip Toys\Wingtip Logon Check`:
			if !en.NeedsAdmin || en.Task == nil || en.Task.RunAs != "SYSTEM" {
				t.Errorf("SYSTEM task = %+v", en)
			}
		}
		if strings.Contains(en.Name, "FamilySafetyMonitor") {
			t.Error("a Windows task (\\Microsoft\\) is listed")
		}
	}
	text, _, code := e.run("startup")
	if code != 0 || !strings.Contains(text, "4 broken") || strings.Contains(text, "\x1b[") || strings.Contains(text, "desktop.ini") {
		t.Errorf("text (code %d):\n%s", code, text)
	}
	e.assertUnchanged(before, "listing")
}

func TestStartupDisableDryRunAndConfirmation(t *testing.T) {
	e := newEnv(t)
	before := e.state()
	out, _, code := e.run("startup", "disable", "Contoso Agent", "--dry-run", "--json")
	d := decode[changeDoc](t, out)
	if code != 0 || d.Schema != "oow.startup-change/v1" || !d.DryRun || len(d.Results) != 1 || d.Results[0].Status != "planned" {
		t.Fatalf("dry run (code %d): %s", code, out)
	}
	if _, _, code := e.run("startup", "disable", "Contoso Agent"); code != cli.ExitNeedsConfirm {
		t.Errorf("without --yes: code %d, want %d", code, cli.ExitNeedsConfirm)
	}
	out, _, code = e.run("startup", "disable", "Contoso Agent", "--json")
	if code != cli.ExitNeedsConfirm || !strings.Contains(out, `"exit_code": 4`) {
		t.Errorf("json without --yes: code %d out %s", code, out)
	}
	t.Setenv("OOW_DRY_RUN", "1")
	if _, _, code := e.run("startup", "disable", "Contoso Agent", "--yes"); code != 0 {
		t.Errorf("OOW_DRY_RUN: code %d", code)
	}
	e.assertUnchanged(before, "startup disable preview")
}

func TestStartupDisableEnableRoundTrip(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("startup", "disable", "hkcu-run:Contoso Agent", "--yes", "--json")
	d := decode[changeDoc](t, out)
	if code != 0 || len(d.Results) != 1 || d.Results[0].Status != "changed" || d.Results[0].Entry.State != "disabled" ||
		!strings.HasPrefix(d.Results[0].After, "03000000") || d.Results[0].Before != "020000000000000000000000" {
		t.Fatalf("disable (code %d): %s", code, out)
	}
	if got := e.startupState("Contoso Agent"); got != "disabled" {
		t.Errorf("state after disable = %s", got)
	}
	// The Run value itself is untouched.
	var st struct {
		Run []struct{ Name, Command string } `json:"run"`
	}
	e.registry("startup.json", &st)
	found := false
	for _, r := range st.Run {
		if r.Name == "Contoso Agent" && strings.Contains(r.Command, "agent.exe") {
			found = true
		}
	}
	if !found {
		t.Error("the Run entry was removed or edited")
	}

	out, _, code = e.run("startup", "enable", "Contoso Agent", "--yes", "--json")
	d = decode[changeDoc](t, out)
	if code != 0 || d.Results[0].Status != "changed" || d.Results[0].After != "020000000000000000000000" {
		t.Fatalf("enable (code %d): %s", code, out)
	}
	hist, _, _ := e.run("history", "--json")
	h := decode[struct {
		Records []struct {
			Command string `json:"command"`
			Changes []struct {
				ID, Action, Status string
			} `json:"changes"`
		} `json:"records"`
	}](t, hist)
	if len(h.Records) != 2 || h.Records[0].Command != "startup" || len(h.Records[0].Changes) != 1 ||
		h.Records[0].Changes[0].Action != "enabled" || h.Records[1].Changes[0].Action != "disabled" ||
		h.Records[1].Changes[0].Status != "changed" {
		t.Errorf("history = %s", hist)
	}
}

type simTask struct {
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	Actions []struct {
		Command string `json:"command"`
	} `json:"actions"`
}

func (e *env) simTasks() map[string]simTask {
	e.t.Helper()
	var list []simTask
	e.registry("tasks.json", &list)
	out := map[string]simTask{}
	for _, t := range list {
		out[t.Path] = t
	}
	return out
}

// A scheduled task is switched with its own Enabled flag: the task stays
// registered and unchanged otherwise, and history records the old flag.
func TestStartupTaskDisableEnableRoundTrip(t *testing.T) {
	e := newEnv(t)
	before := e.simTasks()
	out, _, code := e.run("startup", "disable", "Tailspin Sync", "--yes", "--json")
	d := decode[changeDoc](t, out)
	if code != 0 || len(d.Results) != 1 || d.Results[0].Status != "changed" || d.Results[0].Entry.State != "disabled" ||
		d.Results[0].Before != "" || d.Results[0].After != "" || d.Results[0].TaskEnabledBefore == nil || !*d.Results[0].TaskEnabledBefore {
		t.Fatalf("disable (code %d): %s", code, out)
	}
	after := e.simTasks()
	if len(after) != len(before) || after[`\Tailspin Sync`].Enabled ||
		after[`\Tailspin Sync`].Actions[0].Command != before[`\Tailspin Sync`].Actions[0].Command {
		t.Errorf("tasks after disable = %+v", after)
	}
	for path, task := range after {
		if path != `\Tailspin Sync` && task.Enabled != before[path].Enabled {
			t.Errorf("%s changed", path)
		}
	}
	var st struct {
		Approved map[string]map[string]string `json:"approved"`
	}
	e.registry("startup.json", &st)
	for key, values := range st.Approved {
		if _, ok := values["Tailspin Sync"]; ok {
			t.Errorf("a StartupApproved value was written for a task under %s", key)
		}
	}
	out, _, code = e.run("startup", "enable", "task-logon:Tailspin Sync", "--yes", "--json")
	if d := decode[changeDoc](t, out); code != 0 || d.Results[0].Status != "changed" || *d.Results[0].TaskEnabledBefore {
		t.Fatalf("enable (code %d): %s", code, out)
	}
	if !e.simTasks()[`\Tailspin Sync`].Enabled {
		t.Error("task not enabled again")
	}
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `scheduled task \\Tailspin Sync, Enabled before: true`) ||
		!strings.Contains(hist, `scheduled task \\Tailspin Sync, Enabled before: false`) {
		t.Errorf("history = %s", hist)
	}
}

func TestStartupChangeRefusals(t *testing.T) {
	e := newEnv(t)
	before := e.state()
	if _, errOut, code := e.run("startup", "disable", "contoso", "--yes"); code != cli.ExitUsage || !strings.Contains(errOut, "use the ID") {
		t.Errorf("ambiguous: code %d err %q", code, errOut)
	}
	if _, _, code := e.run("startup", "disable", "no such entry", "--yes"); code != cli.ExitUsage {
		t.Errorf("unknown: code %d", code)
	}
	out, _, code := e.run("startup", "disable", "Setup Cleanup", "--yes", "--json")
	d := decode[changeDoc](t, out)
	if code != cli.ExitError || d.Results[0].Status != "skipped" || !strings.Contains(d.Results[0].Reason, "runs once") {
		t.Errorf("RunOnce (code %d): %s", code, out)
	}
	out, _, code = e.run("startup", "disable", "Old Sync Helper", "--yes", "--json")
	if d := decode[changeDoc](t, out); code != 0 || d.Results[0].Status != "unchanged" {
		t.Errorf("already disabled (code %d): %s", code, out)
	}
	e.assertUnchanged(before, "refused changes")
}

type doctorDoc struct {
	Schema string `json:"schema"`
	Checks []struct {
		ID      string   `json:"id"`
		Status  string   `json:"status"`
		Summary string   `json:"summary"`
		Details []string `json:"details"`
		Next    string   `json:"next_step"`
	} `json:"checks"`
	Summary struct {
		Issues, Problems, Warnings, OK, Info, Unknown int
	} `json:"summary"`
}

func TestDoctorFindsSeededIssuesAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	before := e.state()
	out, _, code := e.run("doctor", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	d := decode[doctorDoc](t, out)
	got := map[string]string{}
	for _, c := range d.Checks {
		got[c.ID] = c.Status
		if c.Details == nil || c.Summary == "" {
			t.Errorf("check %s incomplete", c.ID)
		}
		if (c.Status == "warning" || c.Status == "problem") && c.Next == "" {
			t.Errorf("check %s has no next step", c.ID)
		}
	}
	for id, want := range map[string]string{
		"disk.free.c":         "ok",
		"disk.free.d":         "warning",
		"reboot.pending":      "warning",
		"windows-update":      "ok",
		"path.user":           "warning",
		"path.machine":        "warning",
		"startup.broken":      "warning",
		"cleanup.reclaimable": "ok",
		"network":             "ok",
		"permissions.temp":    "ok",
		"permissions.data":    "ok",
		"package-managers":    "ok",
	} {
		if got[id] != want {
			t.Errorf("%s = %q, want %q", id, got[id], want)
		}
	}
	if d.Schema != "oow.doctor/v1" || d.Summary.Issues != 5 || d.Summary.Warnings != 5 || d.Summary.Problems != 0 {
		t.Errorf("summary = %+v", d.Summary)
	}
	text, _, code := e.run("doctor")
	if code != 0 || !strings.Contains(text, "5 issues found") || !strings.Contains(text, "nothing was changed") ||
		strings.Contains(text, "\x1b[") {
		t.Errorf("text (code %d):\n%s", code, text)
	}
	for _, word := range []string{"✓ ok", "⚠ warning"} {
		if !strings.Contains(text, word) {
			t.Errorf("symbol without its word: %q missing", word)
		}
	}
	e.assertUnchanged(before, "doctor")
}

type optimizeDoc struct {
	Schema string `json:"schema"`
	DryRun bool   `json:"dry_run"`
	Tasks  []struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
		Status         string `json:"status"`
		Reason         string `json:"reason"`
		Selected       bool   `json:"selected"`
		BytesBefore    int64  `json:"bytes_before"`
		ComponentStore *struct {
			ActualBytes         int64 `json:"actual_bytes"`
			BackupsBytes        int64 `json:"backups_bytes"`
			ReclaimablePackages int   `json:"reclaimable_packages"`
			Recommended         bool  `json:"cleanup_recommended"`
		} `json:"component_store"`
	} `json:"tasks"`
	Notes []struct {
		ID string `json:"id"`
	} `json:"notes"`
	Results []struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		Message        string `json:"message"`
		Freed          int64  `json:"freed_bytes"`
		BytesAfter     int64  `json:"bytes_after"`
		ComponentStore *struct {
			Recommended bool `json:"cleanup_recommended"`
		} `json:"component_store"`
	} `json:"results"`
}

type simOptimizeState struct {
	DNSFlushes           int      `json:"dns_flushes"`
	Retrimmed            []string `json:"retrimmed"`
	ComponentCleanups    int      `json:"component_cleanups"`
	ComponentStoreReport string   `json:"component_store_report"`
}

// dismCalls returns the simulated DISM command lines run so far.
func (e *env) dismCalls() []string {
	e.t.Helper()
	data, err := os.ReadFile(sandbox.DISMLog(e.root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r", "")), "\n")
}

const (
	dismAnalyze = "Dism.exe /Online /English /Cleanup-Image /AnalyzeComponentStore"
	dismCleanup = "Dism.exe /Online /English /Quiet /NoRestart /Cleanup-Image /StartComponentCleanup"
)

func TestOptimizePreviewConfirmAndRun(t *testing.T) {
	e := newEnv(t)
	before := e.state()
	out, _, code := e.run("optimize", "--dry-run", "--json")
	d := decode[optimizeDoc](t, out)
	if code != 0 || d.Schema != "oow.optimize/v1" || !d.DryRun || len(d.Tasks) != 4 || d.Results != nil {
		t.Fatalf("dry run (code %d): %s", code, out)
	}
	for _, it := range d.Tasks[:3] {
		if it.Status != "ready" || !it.Selected {
			t.Errorf("task %+v", it)
		}
	}
	// Opt-in: listed as ready, never preselected, and DISM is not started.
	if it := d.Tasks[3]; it.Task.ID != "optimize.component-store" || it.Status != "ready" || it.Selected ||
		it.ComponentStore != nil || it.BytesBefore != -1 || !strings.HasPrefix(it.Reason, "opt-in: ") ||
		!strings.Contains(it.Reason, "`oow optimize --task component-store`") {
		t.Errorf("component store = %+v", it)
	}
	notes := map[string]bool{}
	for _, n := range d.Notes {
		notes[n.ID] = true
	}
	if !notes["reboot.pending"] || !notes["disk.free.d"] || notes["disk.free.c"] {
		t.Errorf("notes = %+v", d.Notes)
	}
	if _, _, code := e.run("optimize"); code != cli.ExitNeedsConfirm {
		t.Errorf("without --yes: code %d", code)
	}
	if _, _, code := e.run("optimize", "--task", "nope", "--yes"); code != cli.ExitUsage {
		t.Errorf("unknown task: code %d", code)
	}
	t.Setenv("OOW_DRY_RUN", "1")
	if _, _, code := e.run("optimize", "--yes"); code != 0 {
		t.Errorf("OOW_DRY_RUN: code %d", code)
	}
	// Named in a preview: DISM analyzes (read-only), the system stays as it was.
	out, _, _ = e.run("optimize", "--task", "component-store", "--dry-run", "--json")
	d = decode[optimizeDoc](t, out)
	if it := d.Tasks[0]; len(d.Tasks) != 1 || it.Status != "ready" || !it.Selected || it.Reason != "" ||
		it.ComponentStore == nil || !it.ComponentStore.Recommended || it.ComponentStore.ReclaimablePackages != 4 ||
		it.ComponentStore.ActualBytes != 6979321856 || it.BytesBefore != 6979321856 {
		t.Errorf("named preview = %s", out)
	}
	e.assertUnchanged(before, "optimize preview")
	if calls := e.dismCalls(); len(calls) != 1 || calls[0] != dismAnalyze {
		t.Errorf("DISM calls in previews = %q", calls)
	}

	// --yes alone: the three preselected tasks, and DISM is never started.
	t.Setenv("OOW_DRY_RUN", "")
	out, _, code = e.run("optimize", "--yes", "--json")
	d = decode[optimizeDoc](t, out)
	if code != 0 || len(d.Results) != 3 {
		t.Fatalf("run (code %d): %s", code, out)
	}
	for _, r := range d.Results {
		if r.Status != "done" || r.ID == "optimize.component-store" {
			t.Errorf("result %+v", r)
		}
	}
	var st simOptimizeState
	e.registry("optimize.json", &st)
	if st.DNSFlushes != 1 || len(st.Retrimmed) != 1 || st.Retrimmed[0] != `C:\` || st.ComponentCleanups != 0 {
		t.Errorf("simulated state = %+v", st)
	}
	if calls := e.dismCalls(); len(calls) != 1 {
		t.Errorf("--yes started DISM: %q", calls)
	}
	if entries, _ := os.ReadDir(sandbox.DOCacheDir(e.root)); len(entries) != 0 {
		t.Errorf("Delivery Optimization cache not emptied: %v", entries)
	}
	if !e.exists(`C\Windows\System32\kernel32.dll`) {
		t.Error("system file removed")
	}
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `"command": "optimize"`) || !strings.Contains(hist, `"reclaimed_bytes": 5000000`) {
		t.Errorf("history = %s", hist)
	}

	// Named with --yes: analysis in the plan, again right before acting, the
	// cleanup, and the measurement afterwards (6.50 GB before, 5.25 GB after).
	out, _, code = e.run("optimize", "--task", "component-store", "--yes", "--json")
	d = decode[optimizeDoc](t, out)
	if code != 0 || len(d.Results) != 1 {
		t.Fatalf("component store run (code %d): %s", code, out)
	}
	if r := d.Results[0]; r.Status != "done" || r.Freed != 1342177280 || r.BytesAfter != 5637144576 || r.ComponentStore == nil ||
		r.ComponentStore.Recommended || !strings.Contains(r.Message, "1.2 GB freed") {
		t.Errorf("component store result = %+v", r)
	}
	if calls := e.dismCalls(); strings.Join(calls[1:], " ") != strings.Join([]string{dismAnalyze, dismAnalyze, dismCleanup, dismAnalyze}, " ") {
		t.Errorf("DISM calls = %q", calls)
	}
	for _, c := range e.dismCalls() {
		if strings.Contains(strings.ToLower(c), "resetbase") || strings.Contains(strings.ToLower(c), "spsuperseded") {
			t.Errorf("forbidden DISM option: %s", c)
		}
	}
	e.registry("optimize.json", &st)
	if st.ComponentCleanups != 1 {
		t.Errorf("cleanups = %d", st.ComponentCleanups)
	}
	hist, _, _ = e.run("history", "--json")
	if !strings.Contains(hist, `"reclaimed_bytes": 1342177280`) {
		t.Errorf("history = %s", hist)
	}
	// DISM no longer recommends a cleanup: nothing to do.
	out, _, _ = e.run("optimize", "--task", "component-store", "--dry-run", "--json")
	if d := decode[optimizeDoc](t, out); len(d.Tasks) != 1 || d.Tasks[0].Status != "not-applicable" || d.Tasks[0].Selected ||
		!strings.Contains(d.Tasks[0].Reason, "DISM does not recommend a cleanup") {
		t.Errorf("after cleanup: %s", out)
	}
}

func TestOptimizeComponentStoreText(t *testing.T) {
	e := newEnv(t)
	text, _, code := e.run("optimize", "--dry-run")
	for _, want := range []string{"Clean up the component store (WinSxS)  opt-in", "opt-in: it can take over an hour",
		"choose it in the list or run `oow optimize --task", "Dry run: nothing was run"} {
		if code != 0 || !strings.Contains(text, want) {
			t.Errorf("default plan lacks %q (code %d):\n%s", want, code, text)
		}
	}
	text, _, code = e.run("optimize", "--task", "component-store", "--dry-run")
	for _, want := range []string{"Clean up the component store (WinSxS)  ready", "store 6.5 GB, overhead 2.4 GB, 4 reclaimable packages",
		"StartComponentCleanup", "/ResetBase", "Ctrl+C", "Dry run: nothing was run"} {
		if code != 0 || !strings.Contains(text, want) {
			t.Errorf("named plan lacks %q (code %d):\n%s", want, code, text)
		}
	}
	help, _, _ := e.run("optimize", "--help")
	for _, want := range []string{"component-store", "never preselected", "SoftwareDistribution\\Download) is not cleaned", "left"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
}

// setDISMReport replaces the simulated DISM report.
func (e *env) setDISMReport(edit func(string) string) {
	e.t.Helper()
	path := filepath.Join(e.root, "registry", "optimize.json")
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		e.t.Fatal(err)
	}
	raw["component_store_report"] = edit(raw["component_store_report"].(string))
	if data, err = json.Marshal(raw); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// A DISM report that cannot be read (here: localized) makes the named task
// unavailable, and the cleanup never runs.
func TestOptimizeUnreadableDISMReportNeverRuns(t *testing.T) {
	e := newEnv(t)
	e.setDISMReport(func(report string) string {
		return strings.ReplaceAll(strings.ReplaceAll(report,
			"Component Store Cleanup Recommended : Yes", "Bereinigung des Komponentenspeichers empfohlen : Ja"),
			"Actual Size of Component Store", "Tatsächliche Größe des Komponentenspeichers")
	})
	out, _, code := e.run("optimize", "--task", "component-store", "--yes", "--json")
	d := decode[optimizeDoc](t, out)
	if code != 0 || len(d.Tasks) != 1 || d.Results != nil {
		t.Fatalf("run (code %d): %s", code, out)
	}
	it := d.Tasks[0]
	if it.Status != "unavailable" || it.Selected || it.ComponentStore != nil || it.BytesBefore != -1 ||
		!strings.Contains(it.Reason, "could not be read") {
		t.Errorf("unreadable report = %+v", it)
	}
	var st simOptimizeState
	e.registry("optimize.json", &st)
	if st.ComponentCleanups != 0 {
		t.Error("the cleanup ran without a readable recommendation")
	}
	for _, c := range e.dismCalls() {
		if c != dismAnalyze {
			t.Errorf("DISM call %s", c)
		}
	}
}

// The sandbox runner end to end through optimize.Plan and optimize.Run: a
// task ticked in the list without a plan-time analysis is analyzed first, and
// a cleanup DISM no longer recommends at run time is skipped.
func TestOptimizeSandboxRunnerRechecksBeforeActing(t *testing.T) {
	e := newEnv(t)
	sys := sandbox.Optimizer{Root: e.root}
	ctx := context.Background()

	plan := optimize.Plan(ctx, sys, true, optimize.Tasks(), nil)
	if len(e.dismCalls()) != 0 {
		t.Fatal("the default plan started DISM")
	}
	for i := range plan {
		plan[i].Selected = plan[i].Task.ID == optimize.TaskComponentStore // ticked in the list
	}
	res := optimize.Run(ctx, sys, plan)
	if len(res) != 1 || res[0].Status != optimize.Done || res[0].Freed != 1342177280 {
		t.Fatalf("ticked = %+v", res)
	}
	if calls := e.dismCalls(); strings.Join(calls, " ") != strings.Join([]string{dismAnalyze, dismCleanup, dismAnalyze}, " ") {
		t.Errorf("DISM calls = %q", calls)
	}

	// Recommended when planned, not any more right before acting.
	e = newEnv(t)
	sys = sandbox.Optimizer{Root: e.root}
	plan = optimize.Plan(ctx, sys, true, optimize.Tasks(), []string{"component-store"})
	if it := plan[3]; it.Status != optimize.Ready || !it.Selected {
		t.Fatalf("plan = %+v", it)
	}
	e.setDISMReport(func(report string) string {
		return strings.Replace(report, "Component Store Cleanup Recommended : Yes", "Component Store Cleanup Recommended : No", 1)
	})
	res = optimize.Run(ctx, sys, plan)
	var st simOptimizeState
	e.registry("optimize.json", &st)
	if r := res[3]; r.Status != optimize.Skipped || st.ComponentCleanups != 0 || !strings.Contains(r.Message, "DISM does not recommend it now") {
		t.Errorf("not recommended at run time = %+v (cleanups %d)", r, st.ComponentCleanups)
	}
	if calls := e.dismCalls(); len(calls) != 2 || calls[1] != dismAnalyze {
		t.Errorf("DISM calls = %q", calls)
	}
}

type repairDoc struct {
	Schema string `json:"schema"`
	DryRun bool   `json:"dry_run"`
	Fixes  []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Selected bool   `json:"selected"`
		Review   string `json:"review"`
	} `json:"fixes"`
	NotFixed []string `json:"not_fixed"`
	Outcome  *struct {
		Backup  string `json:"backup"`
		Fixed   int    `json:"fixed"`
		Failed  int    `json:"failed"`
		Skipped int    `json:"skipped"`
	} `json:"outcome"`
}

func TestRepairPreviewConfirmAndApply(t *testing.T) {
	e := newEnv(t)
	var envBefore struct{ User, Machine envpath.Value }
	e.registry("environment.json", &envBefore)
	before := e.state()

	out, _, code := e.run("repair", "--dry-run", "--json")
	d := decode[repairDoc](t, out)
	if code != 0 || d.Schema != "oow.repair/v1" || !d.DryRun || d.Outcome != nil || len(d.NotFixed) != 1 {
		t.Fatalf("dry run (code %d): %s", code, out)
	}
	sel := 0
	for _, f := range d.Fixes {
		if f.Selected {
			sel++
		} else if f.Review == "" {
			t.Errorf("unselected fix without a reason: %+v", f)
		}
	}
	if len(d.Fixes) != 8 || sel != 7 {
		t.Errorf("fixes = %+v", d.Fixes)
	}
	if _, _, code := e.run("repair"); code != cli.ExitNeedsConfirm {
		t.Errorf("without --yes: code %d", code)
	}
	t.Setenv("OOW_DRY_RUN", "1")
	if _, _, code := e.run("repair", "--yes"); code != 0 {
		t.Errorf("OOW_DRY_RUN: code %d", code)
	}
	e.assertUnchanged(before, "repair preview")

	t.Setenv("OOW_DRY_RUN", "")
	out, _, code = e.run("repair", "--yes", "--json")
	d = decode[repairDoc](t, out)
	if code != 0 || d.Outcome == nil || d.Outcome.Fixed != 7 || d.Outcome.Failed != 0 || d.Outcome.Backup == "" {
		t.Fatalf("apply (code %d): %s", code, out)
	}
	testutil.AssertInSandbox(t, d.Outcome.Backup)
	data, err := os.ReadFile(d.Outcome.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := envpath.ParseRegFile(data); err != nil || v != envBefore.User {
		t.Errorf("backup = %+v, %v", v, err)
	}
	var envAfter struct {
		User, Machine envpath.Value
		Broadcasts    int
	}
	e.registry("environment.json", &envAfter)
	if envAfter.Machine != envBefore.Machine {
		t.Error("the machine PATH changed")
	}
	u := envAfter.User.Raw
	if strings.Contains(u, `OldEditor\bin`) || strings.Contains(u, ";;") || strings.Count(strings.ToLower(u), `microsoft\windowsapps`) != 1 ||
		!strings.Contains(u, `%USERPROFILE%\go\bin`) || !strings.Contains(u, `%TOOLS_HOME%\bin`) || envAfter.Broadcasts != 1 {
		t.Errorf("user PATH after repair = %q (broadcasts %d)", u, envAfter.Broadcasts)
	}
	for _, name := range []string{"Fabrikam Updater", "Old Notes.lnk", "Litware Tray", "Tailspin Sync"} {
		if got := e.startupState(name); got != "disabled" {
			t.Errorf("%s = %s after repair", name, got)
		}
	}
	if tk := e.simTasks(); len(tk) != 4 || tk[`\Tailspin Sync`].Enabled || !tk[`\Wingtip Toys\Wingtip Logon Check`].Enabled {
		t.Errorf("tasks after repair = %+v (the broken task is disabled, never removed; working tasks untouched)", tk)
	}
	if !e.exists(`C\Users\sandbox\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\Old Notes.lnk`) {
		t.Error("the shortcut itself was removed")
	}

	// Doctor afterwards: startup is clean; the profile entry left for review remains.
	out, _, _ = e.run("doctor", "--json")
	for _, c := range decode[doctorDoc](t, out).Checks {
		if c.ID == "startup.broken" && c.Status != "ok" {
			t.Errorf("startup after repair = %+v", c)
		}
	}
	// Running again finds only the fix that needs review.
	out, _, _ = e.run("repair", "--yes", "--json")
	if d := decode[repairDoc](t, out); len(d.Fixes) != 1 || d.Fixes[0].Selected || d.Outcome != nil {
		t.Errorf("second run = %s", out)
	}
}

func TestPhase6CommandsAreRegistered(t *testing.T) {
	e := newEnv(t)
	help, _, _ := e.run("--help")
	for _, c := range []string{"optimize", "startup", "doctor", "repair"} {
		line := ""
		for _, l := range strings.Split(help, "\n") {
			if f := strings.Fields(l); len(f) > 0 && f[0] == c {
				line = l
			}
		}
		if line == "" || strings.Contains(line, "coming in") {
			t.Errorf("%s: %q", c, line)
		}
	}
}
