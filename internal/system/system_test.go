package system_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// Read-only queries of the real system; run on CI or with
// OOW_TEST_REAL_SYSTEM=1.

func TestOSInfo(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	o := system.OS()
	if o.Build < 10240 || !strings.HasPrefix(o.Name, "Windows") {
		t.Fatalf("OS = %+v", o)
	}
	if o.Build >= 22000 && strings.Contains(o.Name, "Windows 10") {
		t.Errorf("Windows 11 build reported as %q", o.Name)
	}
	t.Logf("%s", o)
}

func TestMemoryAndDisk(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	m, err := system.MemoryStatus()
	if err != nil || m.Total == 0 || m.Used > m.Total || m.UsedPercent <= 0 || m.UsedPercent > 100 {
		t.Fatalf("memory = %+v, %v", m, err)
	}
	d, err := system.DiskUsage(system.SystemDrive())
	if err != nil || d.Total == 0 || d.Free > d.Total {
		t.Fatalf("disk = %+v, %v", d, err)
	}
}

func TestCPUSampler(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	var s system.CPUSampler
	if _, ok := s.Sample(); ok {
		t.Fatal("first sample should only prime")
	}
	time.Sleep(200 * time.Millisecond)
	p, ok := s.Sample()
	if !ok || p < 0 || p > 100 {
		t.Fatalf("cpu = %v, %v", p, ok)
	}
}

func TestProcesses(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	names := system.RunningNames()
	if !names["system"] && !names["svchost.exe"] {
		t.Fatalf("expected core processes, got %d names", len(names))
	}
}
