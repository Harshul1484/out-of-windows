package startup_test

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseApproval(t *testing.T) {
	disabledAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		data    []byte
		enabled bool
		known   bool
		at      time.Time
	}{
		{"task manager enabled", mustHex(t, "020000000000000000000000"), true, true, time.Time{}},
		{"enabled (06)", mustHex(t, "060000000000000000000000"), true, true, time.Time{}},
		{"task manager disabled", startup.ApprovalData(false, disabledAt), false, true, disabledAt},
		{"disabled (07), no time", mustHex(t, "070000000000000000000000"), false, true, time.Time{}},
		{"disabled (01)", mustHex(t, "01000000"), false, true, time.Time{}},
		{"unknown odd value is disabled", mustHex(t, "0b0000000000000000000000"), false, false, time.Time{}},
		{"unknown even value is enabled", mustHex(t, "0400"), true, false, time.Time{}},
		{"empty value is enabled", []byte{}, true, false, time.Time{}},
	} {
		enabled, at, known := startup.ParseApproval(c.data)
		if enabled != c.enabled || known != c.known || !at.Equal(c.at) {
			t.Errorf("%s: got enabled=%v known=%v at=%v", c.name, enabled, known, at)
		}
	}
}

func TestApprovalDataMatchesTaskManager(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)
	if got := hex.EncodeToString(startup.ApprovalData(true, now)); got != "020000000000000000000000" {
		t.Errorf("enable = %s", got)
	}
	d := startup.ApprovalData(false, now)
	if len(d) != 12 || d[0] != 3 || d[1] != 0 || d[2] != 0 || d[3] != 0 {
		t.Fatalf("disable = %x", d)
	}
	// 2026-10-05T12:30:00Z as a FILETIME ([DateTime]::ToFileTimeUtc agrees).
	if got := startup.TimeToFiletime(now); got != 134356770000000000 {
		t.Errorf("FILETIME = %d", got)
	}
	if back := startup.FiletimeToTime(startup.TimeToFiletime(now)); !back.Equal(now) {
		t.Errorf("round trip = %v", back)
	}
}

func TestApprovalKeys(t *testing.T) {
	for src, want := range map[startup.Source]string{
		startup.SourceHKCURun:       `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`,
		startup.SourceHKLMRun:       `HKLM\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`,
		startup.SourceHKLMRun32:     `HKLM\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run32`,
		startup.SourceUserFolder:    `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder`,
		startup.SourceCommonFolder:  `HKLM\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder`,
		startup.SourceHKCURunOnce:   "",
		startup.SourceHKLMRunOnce:   "",
		startup.SourceHKLMRunOnce32: "",
	} {
		if got := src.ApprovalKey(); !strings.EqualFold(got, want) {
			t.Errorf("%s: approval key %q, want %q", src, got, want)
		}
	}
}

// fakeProbe reports paths in the set as present and every other absolute
// path as absent.
func fakeProbe(present ...string) func(string) (system.Presence, string) {
	set := map[string]bool{}
	for _, p := range present {
		set[strings.ToLower(p)] = true
	}
	return func(p string) (system.Presence, string) {
		switch {
		case strings.HasPrefix(p, `\\`):
			return system.Unknown, "network path (not checked)"
		case len(p) < 3 || p[1] != ':':
			return system.Unknown, "not absolute"
		case set[strings.ToLower(p)]:
			return system.Present, ""
		}
		return system.Absent, ""
	}
}

func TestCommandTarget(t *testing.T) {
	r := startup.Resolver{
		Expand: func(s string) string { return strings.ReplaceAll(s, "%ProgramFiles%", `C:\Program Files`) },
		Probe: fakeProbe(`C:\Program Files\App\app.exe`, `C:\Windows\System32\ctfmon.exe`,
			`C:\Windows\System32\rundll32.exe`, `C:\Tools\present.dll`, `C:\Program Files\Spaced App\run.bat`),
		Search: []string{`C:\Windows\System32`, `C:\Windows`},
	}
	for _, c := range []struct {
		cmd, target string
		state       system.Presence
	}{
		{`"C:\Program Files\App\app.exe" --tray`, `C:\Program Files\App\app.exe`, system.Present},
		{`%ProgramFiles%\App\app.exe /min`, `C:\Program Files\App\app.exe`, system.Present},
		{`"C:\Program Files\Gone\gone.exe" /x`, `C:\Program Files\Gone\gone.exe`, system.Absent},
		// Unquoted path with spaces whose program is gone: the whole path is named.
		{`C:\Program Files\Old App\old.exe -background`, `C:\Program Files\Old App\old.exe`, system.Absent},
		{`C:\Program Files\Spaced App\run.bat now`, `C:\Program Files\Spaced App\run.bat`, system.Present},
		{`ctfmon.exe`, `C:\Windows\System32\ctfmon.exe`, system.Present},
		{`ctfmon`, `C:\Windows\System32\ctfmon.exe`, system.Present},
		// Bare names not in System32 are found through PATH at run time: never "missing".
		{`sometool.exe --start`, `sometool.exe`, system.Unknown},
		{`\\server\share\tool.exe`, `\\server\share\tool.exe`, system.Unknown},
		{`..\tool.exe`, `..\tool.exe`, system.Unknown},
		{`\??\C:\Program Files\App\app.exe`, `C:\Program Files\App\app.exe`, system.Present},
		// rundll32 is looked through to the DLL it loads.
		{`rundll32.exe C:\Tools\gone.dll,Start`, `C:\Tools\gone.dll`, system.Absent},
		{`C:\Windows\System32\rundll32.exe "C:\Tools\present.dll",Start`, `C:\Tools\present.dll`, system.Present},
		{"", "", system.Unknown},
	} {
		target, state, note := r.CommandTarget(c.cmd)
		if target != c.target || state != c.state {
			t.Errorf("CommandTarget(%q) = %q %s (%s), want %q %s", c.cmd, target, state, note, c.target, c.state)
		}
	}
}

func TestBuildStatesAndBroken(t *testing.T) {
	r := startup.Resolver{Probe: fakeProbe(`C:\App\app.exe`)}
	raws := []startup.Raw{
		{Source: startup.SourceHKCURun, Name: "No Value", Command: `C:\App\app.exe`},
		{Source: startup.SourceHKCURun, Name: "Enabled", Command: `C:\App\app.exe`, Approval: startup.ApprovalData(true, time.Now())},
		{Source: startup.SourceHKCURun, Name: "Disabled Broken", Command: `C:\Gone\gone.exe`,
			Approval: startup.ApprovalData(false, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))},
		{Source: startup.SourceHKLMRun32, Name: "Machine Broken", Command: `C:\Gone\gone.exe`},
		{Source: startup.SourceHKCURunOnce, Name: "Once", Command: `C:\Gone\setup.exe`},
		{Source: startup.SourceUserFolder, Name: "notes.lnk", File: `C:\Startup\notes.lnk`, Link: &startup.Link{LocalPath: `C:\Gone\notes.exe`}},
		{Source: startup.SourceUserFolder, Name: "shell.lnk", File: `C:\Startup\shell.lnk`, Link: &startup.Link{}},
		{Source: startup.SourceUserFolder, Name: "bad.lnk", File: `C:\Startup\bad.lnk`, LinkErr: errors.New("truncated shortcut")},
		{Source: startup.SourceCommonFolder, Name: "run.cmd", File: `C:\Common\run.cmd`},
	}
	got := map[string]startup.Entry{}
	for _, e := range startup.Build(raws, r) {
		got[e.Name] = e
	}
	check := func(name string, state startup.State, broken, toggle, admin bool) {
		t.Helper()
		e, ok := got[name]
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if e.State != state || e.Broken() != broken || e.Toggleable != toggle || e.NeedsAdmin != admin {
			t.Errorf("%s: state=%s broken=%v toggle=%v admin=%v", name, e.State, e.Broken(), e.Toggleable, e.NeedsAdmin)
		}
	}
	check("No Value", startup.Enabled, false, true, false)
	check("Enabled", startup.Enabled, false, true, false)
	check("Disabled Broken", startup.Disabled, true, true, false)
	check("Machine Broken", startup.Enabled, true, true, true)
	check("Once", startup.RunsOnce, false, false, false)
	check("notes.lnk", startup.Enabled, true, true, false)
	check("shell.lnk", startup.Enabled, false, true, false)
	check("bad.lnk", startup.Enabled, false, true, false)
	check("run.cmd", startup.Enabled, false, true, true)
	if e := got["Disabled Broken"]; e.DisabledAt == nil || e.DisabledAt.Year() != 2026 {
		t.Errorf("disabled time = %v", e.DisabledAt)
	}
	if e := got["Machine Broken"]; e.ID != "hklm-run32:Machine Broken" || e.Scope != "machine" ||
		e.Approval == nil || !strings.HasSuffix(e.Approval.Key, `StartupApproved\Run32`) || e.Approval.Data != "" {
		t.Errorf("machine entry = %+v", e)
	}
	if e := got["shell.lnk"]; e.TargetState != system.Unknown || e.TargetNote == "" {
		t.Errorf("shell item shortcut = %+v", e)
	}
	if e := got["notes.lnk"]; e.Location != `C:\Startup\notes.lnk` || e.Target != `C:\Gone\notes.exe` {
		t.Errorf("folder entry = %+v", e)
	}
}

func TestFind(t *testing.T) {
	entries := []startup.Entry{
		{ID: "hkcu-run:Contoso Agent", Name: "Contoso Agent"},
		{ID: "common-startup-folder:Contoso Studio.lnk", Name: "Contoso Studio.lnk"},
		{ID: "hkcu-run:Agent", Name: "Agent"},
	}
	names := func(es []startup.Entry) string {
		var s []string
		for _, e := range es {
			s = append(s, e.Name)
		}
		return strings.Join(s, ",")
	}
	for q, want := range map[string]string{
		"hkcu-run:contoso agent": "Contoso Agent",
		"agent":                  "Agent", // exact name wins over partial matches
		"contoso":                "Contoso Agent,Contoso Studio.lnk",
		"studio":                 "Contoso Studio.lnk",
		"nothing":                "",
		"  ":                     "",
	} {
		if got := names(startup.Find(entries, q)); got != want {
			t.Errorf("Find(%q) = %q, want %q", q, got, want)
		}
	}
}

// memStore is an in-memory startup.Store.
type memStore struct {
	raws       []startup.Raw
	written    map[string][]byte
	taskWrites int
	failSet    error
	lie        bool // pretend to write but keep the old value (verification must catch it)
}

func (m *memStore) List(ctx context.Context) ([]startup.Entry, []string, error) {
	var raws []startup.Raw
	for _, r := range m.raws {
		if b, ok := m.written[startup.ID(r.Source, r.Name)]; ok {
			r.Approval = b
		}
		raws = append(raws, r)
	}
	return startup.Build(raws, startup.Resolver{Probe: fakeProbe(`C:\App\app.exe`)}), nil, nil
}

func (m *memStore) SetApproval(e startup.Entry, data []byte) error {
	if m.failSet != nil {
		return m.failSet
	}
	if e.Task != nil {
		return errors.New("SetApproval called for a scheduled task")
	}
	if !m.lie {
		m.written[e.ID] = data
	}
	return nil
}

func (m *memStore) SetTaskEnabled(e startup.Entry, enabled bool) error {
	if m.failSet != nil {
		return m.failSet
	}
	for _, r := range m.raws {
		if r.Task != nil && r.Task.Path == e.Location {
			m.taskWrites++
			if !m.lie {
				r.Task.Enabled = enabled
			}
			return nil
		}
	}
	return startup.ErrEntryGone
}

func TestSetEnabled(t *testing.T) {
	s := &memStore{written: map[string][]byte{}, raws: []startup.Raw{
		{Source: startup.SourceHKCURun, Name: "User App", Command: `C:\App\app.exe`},
		{Source: startup.SourceHKLMRun, Name: "Machine App", Command: `C:\App\app.exe`},
		{Source: startup.SourceHKCURunOnce, Name: "Once", Command: `C:\App\app.exe`},
		{Source: startup.SourceHKCURun, Name: "Already Off", Command: `C:\App\app.exe`, Approval: startup.ApprovalData(false, time.Now())},
	}}
	entries, _, _ := s.List(context.Background())
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	// Not elevated: the machine entry is refused with a cause and next step.
	res := startup.SetEnabled(context.Background(), s, entries, false, false, now)
	status := map[string]startup.Result{}
	for _, r := range res {
		status[r.Entry.Name] = r
	}
	if r := status["User App"]; r.Status != startup.StatusChanged || r.Entry.State != startup.Disabled || r.Before != "" ||
		r.After != hex.EncodeToString(startup.ApprovalData(false, now)) {
		t.Errorf("user app = %+v", r)
	}
	if r := status["Machine App"]; r.Status != startup.StatusSkipped || !strings.Contains(r.Reason, "administrator") ||
		!strings.Contains(r.Reason, "elevated terminal") {
		t.Errorf("machine app = %+v", r)
	}
	if r := status["Once"]; r.Status != startup.StatusSkipped || !strings.Contains(r.Reason, "runs once") {
		t.Errorf("runonce = %+v", r)
	}
	if r := status["Already Off"]; r.Status != startup.StatusUnchanged {
		t.Errorf("already disabled = %+v", r)
	}
	if _, wrote := s.written["hklm-run:Machine App"]; wrote {
		t.Error("machine entry written without elevation")
	}
	if _, wrote := s.written["hkcu-runonce:Once"]; wrote {
		t.Error("RunOnce entry written")
	}

	// Elevated: the machine entry changes too; enabling restores 02.
	res = startup.SetEnabled(context.Background(), s, []startup.Entry{status["Machine App"].Entry}, false, true, now)
	if res[0].Status != startup.StatusChanged {
		t.Errorf("elevated machine = %+v", res[0])
	}
	entries, _, _ = s.List(context.Background())
	res = startup.SetEnabled(context.Background(), s, entries[:1], true, false, now)
	if res[0].Status != startup.StatusChanged || hex.EncodeToString(s.written[entries[0].ID]) != "020000000000000000000000" {
		t.Errorf("enable = %+v, data %x", res[0], s.written[entries[0].ID])
	}
}

func TestSetEnabledVerifiesAndReportsFailures(t *testing.T) {
	raws := []startup.Raw{{Source: startup.SourceHKCURun, Name: "App", Command: `C:\App\app.exe`}}
	s := &memStore{written: map[string][]byte{}, raws: raws, lie: true}
	entries, _, _ := s.List(context.Background())
	res := startup.SetEnabled(context.Background(), s, entries, false, true, time.Now())
	if res[0].Status != startup.StatusFailed || !strings.Contains(res[0].Reason, "still reports it enabled") {
		t.Errorf("unverified write = %+v", res[0])
	}
	s = &memStore{written: map[string][]byte{}, raws: raws, failSet: startup.ErrEntryGone}
	res = startup.SetEnabled(context.Background(), s, entries, false, true, time.Now())
	if res[0].Status != startup.StatusFailed || !strings.Contains(res[0].Reason, "no longer exists") {
		t.Errorf("gone entry = %+v", res[0])
	}
	// Cancelled before writing: nothing is written.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = &memStore{written: map[string][]byte{}, raws: raws}
	res = startup.SetEnabled(ctx, s, entries, false, true, time.Now())
	if res[0].Status != startup.StatusSkipped || len(s.written) != 0 {
		t.Errorf("cancelled = %+v, written %v", res[0], s.written)
	}
}

func TestPlanChangesNothing(t *testing.T) {
	s := &memStore{written: map[string][]byte{}, raws: []startup.Raw{{Source: startup.SourceHKCURun, Name: "App", Command: `C:\App\app.exe`}}}
	entries, _, _ := s.List(context.Background())
	res := startup.Plan(entries, false, true)
	if len(res) != 1 || res[0].Status != startup.StatusPlanned || len(s.written) != 0 {
		t.Errorf("plan = %+v, written = %v", res, s.written)
	}
}

func TestReadFolder(t *testing.T) {
	f := testutil.NewFixture(t)
	dir := f.Dir("Startup", time.Hour)
	target := f.File(`Programs\App\app.exe`, 100, time.Hour)
	if err := os.WriteFile(f.Path(`Startup\App.lnk`), startup.BuildLink(target, "--min"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path(`Startup\Broken.lnk`), []byte("not a link"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.File(`Startup\desktop.ini`, 10, time.Hour)
	f.File(`Startup\script.cmd`, 10, time.Hour)
	f.Dir(`Startup\subfolder`, time.Hour)

	before := f.Snapshot()
	raws, err := startup.ReadFolder(startup.SourceUserFolder, dir, nil, func(name string) []byte {
		if name == "script.cmd" {
			return startup.ApprovalData(false, time.Now())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 3 {
		t.Fatalf("raws = %+v", raws)
	}
	entries := startup.Build(raws, startup.Resolver{Probe: system.ProbePath})
	byName := map[string]startup.Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e := byName["App.lnk"]; e.Target != target || e.TargetState != system.Present || !strings.HasSuffix(e.Command, "--min") {
		t.Errorf("shortcut = %+v", e)
	}
	if e := byName["Broken.lnk"]; e.TargetState != system.Unknown || !strings.Contains(e.TargetNote, "could not be read") {
		t.Errorf("broken shortcut = %+v", e)
	}
	if e := byName["script.cmd"]; e.State != startup.Disabled || e.TargetState != system.Present {
		t.Errorf("script = %+v", e)
	}
	if missing, err := startup.ReadFolder(startup.SourceUserFolder, f.Path("nope"), nil, nil); err != nil || missing != nil {
		t.Errorf("missing folder = %v, %v", missing, err)
	}
	if after := f.Snapshot(); len(after) != len(before) {
		t.Error("reading the folder changed it")
	}
}
