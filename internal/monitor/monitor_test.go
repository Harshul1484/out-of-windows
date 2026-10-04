package monitor

import (
	"math"
	"os"
	"testing"
	"time"
	"unsafe"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestComputeCPUAndProcesses(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := Raw{
		Time:  t0,
		Total: CoreTimes{Idle: 1000, Kernel: 2000, User: 1000},
		Cores: []CoreTimes{{Idle: 500, Kernel: 1000, User: 500}, {Idle: 500, Kernel: 1000, User: 500}},
		Processes: []RawProcess{
			{PID: 10, Name: "busy.exe", CPUTime: 0, Created: 1, IOBytes: 100},
			{PID: 20, Name: "reused.exe", CPUTime: 0, Created: 1},
		},
		MemTotal: 16 << 30, MemAvail: 8 << 30,
	}
	cur := Raw{
		Time: t0.Add(time.Second),
		// 4000 units elapsed in total, 1000 of them idle -> 75% busy.
		Total: CoreTimes{Idle: 2000, Kernel: 4000, User: 3000},
		// Core 0 fully idle, core 1 fully busy.
		Cores: []CoreTimes{{Idle: 1500, Kernel: 2000, User: 500}, {Idle: 500, Kernel: 1500, User: 1500}},
		Processes: []RawProcess{
			// One full core for one second = 1e7 units of 100 ns; two cores -> 50%.
			{PID: 10, Name: "busy.exe", CPUTime: 1e7, Created: 1, IOBytes: 2100, Private: 300 << 20},
			// Same PID, different creation time: a new process, no rate yet.
			{PID: 20, Name: "reused.exe", CPUTime: 5e6, Created: 2},
			{PID: 30, Name: "new.exe", CPUTime: 3e6, Created: 1},
		},
		MemTotal: 16 << 30, MemAvail: 4 << 30,
		Interfaces: []Interface{{Name: "Ethernet", RecvRate: 1000, SentRate: 10}, {Name: "Wi-Fi", RecvRate: 500, SentRate: 5}},
	}
	s := Compute(prev, cur)
	if !near(s.CPU, 75) {
		t.Errorf("CPU = %.2f, want 75", s.CPU)
	}
	if len(s.Cores) != 2 || !near(s.Cores[0], 0) || !near(s.Cores[1], 100) {
		t.Errorf("cores = %v", s.Cores)
	}
	if s.MemUsed != 12<<30 || !near(s.MemPercent(), 75) {
		t.Errorf("memory used = %d (%.1f%%)", s.MemUsed, s.MemPercent())
	}
	if !near(s.NetRecv, 1500) || !near(s.NetSent, 15) {
		t.Errorf("net = %.0f / %.0f", s.NetRecv, s.NetSent)
	}
	if s.Processes[0].Name != "busy.exe" || !near(s.Processes[0].CPU, 50) || !near(s.Processes[0].IORate, 2000) {
		t.Errorf("top process = %+v", s.Processes[0])
	}
	for _, p := range s.Processes[1:] {
		if p.CPU != 0 {
			t.Errorf("%s has a CPU rate without a previous sample: %.1f", p.Name, p.CPU)
		}
	}
}

func TestComputeHandlesCounterResets(t *testing.T) {
	prev := Raw{Time: time.Unix(0, 0), Total: CoreTimes{Idle: 10, Kernel: 20, User: 20}}
	cur := Raw{Time: time.Unix(1, 0), Total: CoreTimes{Idle: 5, Kernel: 10, User: 10}}
	if s := Compute(prev, cur); s.CPU != 0 {
		t.Errorf("CPU after reset = %.1f", s.CPU)
	}
	if s := Compute(cur, cur); s.CPU != 0 || s.Processes != nil && len(s.Processes) != 0 {
		t.Errorf("identical readings: %+v", s)
	}
}

func TestSortProcesses(t *testing.T) {
	ps := []Process{{Name: "b", CPU: 1, Memory: 10, IORate: 5}, {Name: "A", CPU: 5, Memory: 1, IORate: 1}, {Name: "c", CPU: 1, Memory: 20}}
	SortProcesses(ps, "memory")
	if ps[0].Name != "c" {
		t.Errorf("by memory: %v", ps)
	}
	SortProcesses(ps, "name")
	if ps[0].Name != "A" {
		t.Errorf("by name: %v", ps)
	}
	SortProcesses(ps, "cpu")
	if ps[0].Name != "A" || ps[1].Name != "c" {
		t.Errorf("by cpu (ties by memory): %v", ps)
	}
}

func TestGPUUtilization(t *testing.T) {
	engines := map[string]float64{
		"pid_1_luid_0x00000000_0x0000D1B5_phys_0_eng_0_engtype_3D":          30,
		"pid_2_luid_0x00000000_0x0000D1B5_phys_0_eng_0_engtype_3D":          25,
		"pid_2_luid_0x00000000_0x0000D1B5_phys_0_eng_3_engtype_VideoDecode": 40,
		"pid_3_luid_0x00000000_0x0000AAAA_phys_0_eng_0_engtype_3D":          10,
	}
	if got := gpuUtilization(engines); !near(got, 55) {
		t.Errorf("GPU utilization = %.1f, want 55 (busiest engine type: 3D)", got)
	}
	if got := gpuUtilization(nil); got != -1 {
		t.Errorf("no counters: %.1f, want -1", got)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	gpus := parseNvidiaSMI("NVIDIA GeForce RTX 3060, 12, 1024, 12288, 45\nNVIDIA T4, [N/A], 0, 15360, [N/A]\n")
	if len(gpus) != 2 {
		t.Fatalf("gpus = %+v", gpus)
	}
	g := gpus[0]
	if g.Name != "NVIDIA GeForce RTX 3060" || g.Utilization != 12 || g.MemoryUsed != 1024<<20 || g.MemoryTotal != 12288<<20 || g.Temperature != 45 {
		t.Errorf("gpu = %+v", g)
	}
	if gpus[1].Utilization != -1 || gpus[1].Temperature != -1 {
		t.Errorf("unavailable values not -1: %+v", gpus[1])
	}
}

func TestStructLayouts(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layout only")
	}
	if s := unsafe.Sizeof(processorPerformance{}); s != 48 {
		t.Errorf("SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION = %d bytes, want 48", s)
	}
	if s := unsafe.Sizeof(performanceInformation{}); s != 104 {
		t.Errorf("PERFORMANCE_INFORMATION = %d bytes, want 104", s)
	}
	if s := unsafe.Sizeof(pdhCounterValue{}); s != 16 {
		t.Errorf("PDH_FMT_COUNTERVALUE = %d bytes, want 16", s)
	}
	if s := unsafe.Sizeof(pdhCounterItem{}); s != 24 {
		t.Errorf("PDH_FMT_COUNTERVALUE_ITEM = %d bytes, want 24", s)
	}
}

// Real readings (read-only) run on CI or with OOW_TEST_REAL_SYSTEM=1.
func TestRealSystem(t *testing.T) {
	if os.Getenv("OOW_TEST_REAL_SYSTEM") != "1" {
		t.Skip("reads the real system; set OOW_TEST_REAL_SYSTEM=1 to run (CI does)")
	}
	src := NewSystem()
	defer src.Close()
	s := &Sampler{Source: src}
	if _, err := s.Sample(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	snap, err := s.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if snap.MemTotal == 0 || len(snap.Cores) == 0 || snap.ProcCount < 10 || len(snap.Volumes) == 0 {
		t.Fatalf("snapshot incomplete: mem=%d cores=%d procs=%d volumes=%d", snap.MemTotal, len(snap.Cores), snap.ProcCount, len(snap.Volumes))
	}
	if snap.CPU < 0 || snap.CPU > 100 {
		t.Errorf("CPU = %.1f", snap.CPU)
	}
	t.Logf("CPU %.1f%% (%d cores, %.0f MHz), memory %.1f%%, disk active %.1f%%, %d interfaces, GPUs %+v, %d processes",
		snap.CPU, len(snap.Cores), snap.FreqMHz, snap.MemPercent(), snap.DiskActive, len(snap.Interfaces), snap.GPUs, snap.ProcCount)
}
