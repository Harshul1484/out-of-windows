package doctor_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/doctor"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

const gb = 1 << 30

func healthy() doctor.Facts {
	return doctor.Facts{
		Drives:      []doctor.Drive{{Root: `C:\`, Total: 500 * gb, Free: 200 * gb}},
		Reboot:      &doctor.Reboot{},
		Update:      &doctor.Update{ServiceStart: 3},
		UserPath:    &envpath.Report{Scope: envpath.User, Value: envpath.Value{Exists: true, Raw: `C:\a`}, Entries: []envpath.Entry{{Raw: `C:\a`}}},
		MachinePath: &envpath.Report{Scope: envpath.Machine, Value: envpath.Value{Exists: true, Raw: `C:\w`}, Entries: []envpath.Entry{{Raw: `C:\w`}}},
		Startup: []startup.Entry{{Name: "App", State: startup.Enabled, TargetState: system.Present, Toggleable: true},
			{Name: "Old", State: startup.Disabled, TargetState: system.Absent, Toggleable: true}},
		PackageManagers: map[string]string{"winget": `C:\winget.exe`},
		Reclaimable:     &doctor.Reclaimable{Bytes: 100 << 20},
		Adapters: []doctor.Adapter{{Name: "Ethernet", Up: true, Addresses: []string{"10.0.0.2"},
			Gateways: []string{"10.0.0.1"}, DNS: []string{"10.0.0.1"}}},
		Dirs: []doctor.Dir{{ID: "temp", Label: "Temp folder", Path: `C:\Temp`}},
	}
}

func byID(cs []doctor.Check) map[string]doctor.Check {
	m := map[string]doctor.Check{}
	for _, c := range cs {
		m[c.ID] = c
	}
	return m
}

func TestHealthySystemHasNoIssues(t *testing.T) {
	cs := doctor.Evaluate(healthy())
	s := doctor.Summarize(cs)
	if s.Issues != 0 || s.Unknown != 0 {
		for _, c := range cs {
			t.Logf("%s %s %s", c.ID, c.Status, c.Summary)
		}
		t.Fatalf("summary = %+v", s)
	}
	for _, c := range cs {
		if c.Summary == "" || c.Title == "" || c.Category == "" {
			t.Errorf("incomplete check %+v", c)
		}
	}
	// Checks come grouped by category in display order.
	last := -1
	order := map[string]int{}
	for i, c := range doctor.Categories {
		order[c] = i
	}
	for _, c := range cs {
		if order[c.Category] < last {
			t.Errorf("%s out of order", c.ID)
		}
		last = order[c.Category]
	}
}

func TestDiskThresholds(t *testing.T) {
	for _, c := range []struct {
		free, total uint64
		want        doctor.Status
	}{
		{200 * gb, 500 * gb, doctor.OK},
		{40 * gb, 500 * gb, doctor.Warning}, // 8% free
		{9 * gb, 40 * gb, doctor.Warning},   // under 10 GB
		{1 * gb, 500 * gb, doctor.Problem},  // under 2 GB
		{0, 0, doctor.Problem},
	} {
		f := healthy()
		f.Drives = []doctor.Drive{{Root: `D:\`, Total: c.total, Free: c.free}}
		got := byID(doctor.Evaluate(f))["disk.free.d"]
		if got.Status != c.want {
			t.Errorf("free %d of %d: %s, want %s", c.free, c.total, got.Status, c.want)
		}
		if got.Status != doctor.OK && !strings.Contains(got.Next, "oow clean") {
			t.Errorf("next step = %q", got.Next)
		}
	}
	f := healthy()
	f.Drives, f.DrivesErr = nil, errors.New("access denied")
	if c := byID(doctor.Evaluate(f))["disk.free"]; c.Status != doctor.Unknown {
		t.Errorf("unreadable drives = %+v", c)
	}
}

func TestRebootAndUpdate(t *testing.T) {
	f := healthy()
	f.Reboot = &doctor.Reboot{WindowsUpdate: true}
	f.Update = &doctor.Update{ServiceStart: 4}
	cs := byID(doctor.Evaluate(f))
	if c := cs["reboot.pending"]; c.Status != doctor.Warning || !strings.Contains(c.Next, "Restart") {
		t.Errorf("reboot = %+v", c)
	}
	if c := cs["windows-update"]; c.Status != doctor.Problem || c.Next == "" {
		t.Errorf("disabled service = %+v", c)
	}

	f.Reboot = &doctor.Reboot{FileRenames: true}
	f.Update = &doctor.Update{ServiceStart: 3, PausedUntil: time.Now().Add(72 * time.Hour), Managed: true}
	cs = byID(doctor.Evaluate(f))
	if c := cs["reboot.pending"]; c.Status != doctor.Info {
		t.Errorf("file renames only = %+v", c)
	}
	if c := cs["windows-update"]; c.Status != doctor.Info || !strings.Contains(c.Summary, "paused") {
		t.Errorf("paused = %+v", c)
	}
	f.Update = &doctor.Update{ServiceStart: 2, NoAutoUpdate: true}
	if c := byID(doctor.Evaluate(f))["windows-update"]; c.Status != doctor.Warning {
		t.Errorf("policy = %+v", c)
	}
	f.Reboot, f.RebootErr = nil, errors.New("denied")
	f.Update, f.UpdateErr = nil, errors.New("denied")
	cs = byID(doctor.Evaluate(f))
	if cs["reboot.pending"].Status != doctor.Unknown || cs["windows-update"].Status != doctor.Unknown {
		t.Error("unreadable facts must be unknown, never ok")
	}
}

func TestPathChecks(t *testing.T) {
	f := healthy()
	f.UserPath = &envpath.Report{Scope: envpath.User, Value: envpath.Value{Exists: true}, Missing: 1, Duplicates: 1,
		Entries: []envpath.Entry{{Raw: `C:\Gone`, Problem: envpath.ProblemMissing}, {Raw: `C:\a`}, {Raw: `C:\a`, Problem: envpath.ProblemDuplicate}}}
	f.MachinePath = &envpath.Report{Scope: envpath.Machine, Value: envpath.Value{Exists: true}, Missing: 1,
		Entries: []envpath.Entry{{Raw: `C:\Old`, Problem: envpath.ProblemMissing}}}
	cs := byID(doctor.Evaluate(f))
	u, m := cs["path.user"], cs["path.machine"]
	if u.Status != doctor.Warning || !strings.Contains(u.Next, "oow repair") || len(u.Details) < 2 {
		t.Errorf("user = %+v", u)
	}
	if m.Status != doctor.Warning || !strings.Contains(m.Next, "never changes it") {
		t.Errorf("machine = %+v", m)
	}

	f = healthy()
	f.UserPath.ExpandedLength, f.MachinePath.ExpandedLength = 5000, 4000
	if c := byID(doctor.Evaluate(f))["path.user"]; c.Status != doctor.Warning || !strings.Contains(strings.Join(c.Details, " "), "8191") {
		t.Errorf("too long = %+v", c)
	}
	f = healthy()
	f.UserPath = &envpath.Report{Scope: envpath.User, Error: "access denied"}
	if c := byID(doctor.Evaluate(f))["path.user"]; c.Status != doctor.Unknown {
		t.Errorf("unreadable = %+v", c)
	}
}

func TestStartupCheckCountsOnlyEnabledBroken(t *testing.T) {
	f := healthy()
	f.Startup = append(f.Startup,
		startup.Entry{Name: "Gone", State: startup.Enabled, TargetState: system.Absent, Target: `C:\Gone\x.exe`, Toggleable: true},
		startup.Entry{Name: "Once", State: startup.RunsOnce, TargetState: system.Absent},
		startup.Entry{Name: "Net", State: startup.Enabled, TargetState: system.Unknown})
	c := byID(doctor.Evaluate(f))["startup.broken"]
	if c.Status != doctor.Warning || c.Data["broken"] != 1 || !strings.Contains(c.Details[0], `C:\Gone\x.exe`) {
		t.Errorf("startup = %+v", c)
	}
	f.Startup, f.StartupErr = nil, errors.New("denied")
	if c := byID(doctor.Evaluate(f))["startup.broken"]; c.Status != doctor.Unknown {
		t.Errorf("unreadable = %+v", c)
	}
}

func TestNetworkCheck(t *testing.T) {
	cases := []struct {
		name     string
		adapters []doctor.Adapter
		want     doctor.Status
	}{
		{"none", nil, doctor.Problem},
		{"down", []doctor.Adapter{{Name: "Wi-Fi", Up: false, Addresses: []string{"10.0.0.2"}}}, doctor.Problem},
		{"no gateway", []doctor.Adapter{{Name: "vEthernet", Up: true, Addresses: []string{"172.20.0.1"}}}, doctor.Warning},
		{"placeholder DNS only", []doctor.Adapter{{Name: "Ethernet", Up: true, Addresses: []string{"10.0.0.2"},
			Gateways: []string{"10.0.0.1"}, DNS: []string{"fec0:0:0:ffff::1", "fec0:0:0:ffff::2"}}}, doctor.Warning},
		{"one good of several", []doctor.Adapter{
			{Name: "vEthernet", Up: true, Addresses: []string{"172.20.0.1"}},
			{Name: "Ethernet", Up: true, Addresses: []string{"10.0.0.2"}, Gateways: []string{"10.0.0.1"}, DNS: []string{"1.1.1.1"}}}, doctor.OK},
	}
	for _, c := range cases {
		f := healthy()
		f.Adapters = c.adapters
		got := byID(doctor.Evaluate(f))["network"]
		if got.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.Status, got.Summary, c.want)
		}
	}
}

func TestPermissionsAndTools(t *testing.T) {
	f := healthy()
	f.Dirs = []doctor.Dir{{ID: "temp", Label: "Temp folder", Path: `C:\Temp`, Err: "Access is denied."}}
	f.PackageManagers = map[string]string{}
	cs := byID(doctor.Evaluate(f))
	if c := cs["permissions.temp"]; c.Status != doctor.Problem || !strings.Contains(c.Next, "icacls") {
		t.Errorf("permissions = %+v", c)
	}
	if c := cs["package-managers"]; c.Status != doctor.Info {
		t.Errorf("no package managers = %+v", c)
	}
	s := doctor.Summarize(doctor.Evaluate(f))
	if s.Issues != 1 || s.Problems != 1 || s.Info != 1 {
		t.Errorf("summary = %+v", s)
	}
}

func TestReclaimableIsInformationNotAnIssue(t *testing.T) {
	f := healthy()
	f.Reclaimable = &doctor.Reclaimable{Bytes: 12 * gb, Items: 4000}
	c := byID(doctor.Evaluate(f))["cleanup.reclaimable"]
	if c.Status != doctor.Info || !strings.Contains(c.Next, "clean") {
		t.Errorf("reclaimable = %+v", c)
	}
}

type fakeProbe struct{ writableErr error }

func (fakeProbe) Drives() ([]doctor.Drive, error) {
	return []doctor.Drive{{Root: `C:\`, Total: 10, Free: 5}}, nil
}
func (fakeProbe) Reboot() (doctor.Reboot, error)      { return doctor.Reboot{}, errors.New("denied") }
func (fakeProbe) Update() (doctor.Update, error)      { return doctor.Update{ServiceStart: 3}, nil }
func (fakeProbe) Adapters() ([]doctor.Adapter, error) { return nil, nil }
func (fakeProbe) PackageManagers() map[string]string  { return nil }
func (p fakeProbe) Writable(string) error             { return p.writableErr }

func TestCollect(t *testing.T) {
	f := doctor.Collect(fakeProbe{writableErr: errors.New("denied")}, []doctor.Dir{{ID: "data", Path: `C:\d`}})
	if f.Reboot != nil || f.RebootErr == nil || f.Update == nil || len(f.Drives) != 1 || f.Dirs[0].Err != "denied" {
		t.Errorf("facts = %+v", f)
	}
}

func TestRealProbe(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	f := doctor.Collect(doctor.System{}, nil)
	if f.DrivesErr != nil || len(f.Drives) == 0 {
		t.Fatalf("drives = %v, %v", f.Drives, f.DrivesErr)
	}
	for _, c := range doctor.Evaluate(f) {
		t.Logf("%-20s %-8s %s", c.ID, c.Status, c.Summary)
	}
}
