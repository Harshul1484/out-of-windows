package sandbox

import (
	"time"

	"github.com/Harshul1484/out-of-windows/internal/monitor"
)

// Monitor is a deterministic, simulated metrics source for sandbox mode, so
// status and process commands can be exercised without reading the real
// system. Each Read advances simulated time by one second.
type Monitor struct {
	n int
}

// Read returns the next simulated reading.
func (m *Monitor) Read() (monitor.Raw, error) {
	m.n++
	n := uint64(m.n)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(m.n) * time.Second)
	sec := uint64(1e7) // one second in 100 ns units, per core
	return monitor.Raw{
		Time: base,
		// 4 cores; per second: core 0 busy 80%, others 20% (kernel includes idle).
		Total: monitor.CoreTimes{Idle: n * (sec*2/10 + 3*sec*8/10), Kernel: n * 4 * sec, User: 0},
		Cores: []monitor.CoreTimes{
			{Idle: n * sec * 2 / 10, Kernel: n * sec},
			{Idle: n * sec * 8 / 10, Kernel: n * sec},
			{Idle: n * sec * 8 / 10, Kernel: n * sec},
			{Idle: n * sec * 8 / 10, Kernel: n * sec},
		},
		Processes: []monitor.RawProcess{
			{PID: 4, Name: "System", CPUTime: n * sec / 10, Private: 100 << 10, Created: 1, Threads: 150, Handles: 4000},
			{PID: 1200, Name: "compiler.exe", CPUTime: n * sec * 8 / 10, Private: 900 << 20, IOBytes: n * (20 << 20), Created: 2, Threads: 12, Handles: 300},
			{PID: 2400, Name: "browser.exe", CPUTime: n * sec * 4 / 10, Private: 1500 << 20, IOBytes: n * (1 << 20), Created: 3, Threads: 40, Handles: 1200},
			{PID: 3600, Name: "editor.exe", CPUTime: n * sec / 10, Private: 400 << 20, Created: 4, Threads: 20, Handles: 600},
		},
		MemTotal: 16 << 30, MemAvail: 6 << 30,
		CommitUsed: 14 << 30, CommitMax: 24 << 30, Cached: 3 << 30,
		Volumes:  []monitor.Volume{{Root: `C:\`, Total: 1000 << 30, Free: 250 << 30}},
		DiskRead: 12 << 20, DiskWrite: 3 << 20, DiskActive: 28,
		Interfaces: []monitor.Interface{{Name: "Simulated Ethernet", RecvRate: 18 << 20, SentRate: 2 << 20}},
		GPUs:       []monitor.GPU{{Name: "Simulated GPU", Utilization: 39, MemoryUsed: 2 << 30, MemoryTotal: 12 << 30, Temperature: 61}},
		FreqMHz:    3400, MaxMHz: 3800,
		Uptime:  76 * time.Hour,
		Threads: 222, Handles: 6100,
	}, nil
}

// Close does nothing.
func (m *Monitor) Close() {}
