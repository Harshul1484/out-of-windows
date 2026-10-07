package system_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func TestProbePath(t *testing.T) {
	f := testutil.NewFixture(t)
	file := f.File(`app\app.exe`, 10, time.Hour)
	for _, c := range []struct {
		path string
		want system.Presence
	}{
		{file, system.Present},
		{filepath.Dir(file), system.Present},
		{f.Path(`app\gone.exe`), system.Absent},
		{f.Path(`gone\deeper\x.exe`), system.Absent},
		{`\\server\share\tool.exe`, system.Unknown},
		{`//server/share/tool.exe`, system.Unknown},
		{`relative\tool.exe`, system.Unknown},
		{`C:tool.exe`, system.Unknown},
		{"", system.Unknown},
	} {
		got, note := system.ProbePath(c.path)
		if got != c.want {
			t.Errorf("ProbePath(%q) = %s (%s), want %s", c.path, got, note, c.want)
		}
		if got == system.Unknown && note == "" {
			t.Errorf("ProbePath(%q): unknown without a reason", c.path)
		}
	}
}

func TestCanCreateInDoesNotWrite(t *testing.T) {
	f := testutil.NewFixture(t)
	dir := f.Dir("writable", time.Hour)
	before := f.Snapshot()
	if err := system.CanCreateIn(dir); err != nil {
		t.Fatalf("CanCreateIn(%s) = %v", dir, err)
	}
	if err := system.CanCreateIn(f.Path("missing")); err == nil {
		t.Error("CanCreateIn on a missing folder succeeded")
	}
	after := f.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("CanCreateIn changed the folder: %v -> %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed", k)
		}
	}
}

func TestFixedDrivesRealSystem(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	drives := system.FixedDrives()
	found := false
	for _, d := range drives {
		if d == system.SystemDrive() {
			found = true
		}
	}
	if !found {
		t.Errorf("fixed drives %v do not include the system drive %s", drives, system.SystemDrive())
	}
}
