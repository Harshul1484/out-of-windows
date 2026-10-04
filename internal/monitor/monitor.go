// Package monitor collects live system metrics: CPU (total and per core),
// memory, disks, network, GPUs and processes. It is strictly read-only.
//
// A Source returns raw counters; Compute turns two consecutive readings into
// rates and percentages, so the arithmetic is testable without a real system.
// Metrics that are unavailable on a machine are reported as unavailable,
// never guessed.
package monitor

import (
	"sort"
	"strings"
	"time"
)

// CoreTimes are cumulative times of one logical processor, in 100 ns units.
type CoreTimes struct {
	Idle, Kernel, User uint64 // Kernel includes Idle
}

// RawProcess is a process as read from the system.
type RawProcess struct {
	PID     uint32
	PPID    uint32
	Name    string
	CPUTime uint64 // user + kernel, 100 ns units
	Private uint64 // private working set, bytes
	IOBytes uint64 // cumulative read + write + other transfer
	Threads uint32
	Handles uint32
	Created int64
	Session uint32
}

// Interface is a network interface's current rates.
type Interface struct {
	Name     string  `json:"name"`
	RecvRate float64 `json:"recv_bytes_per_sec"`
	SentRate float64 `json:"sent_bytes_per_sec"`
}

// GPU is one graphics adapter.
type GPU struct {
	Name        string  `json:"name"`
	Utilization float64 `json:"utilization_percent"` // -1 when unknown
	MemoryUsed  uint64  `json:"memory_used_bytes"`
	MemoryTotal uint64  `json:"memory_total_bytes"`
	Temperature float64 `json:"temperature_c"` // -1 when unknown
}

// Volume is one fixed drive.
type Volume struct {
	Root  string `json:"root"`
	Total uint64 `json:"total_bytes"`
	Free  uint64 `json:"free_bytes"`
}

// Raw is one reading of every counter.
type Raw struct {
	Time       time.Time
	Total      CoreTimes
	Cores      []CoreTimes
	Processes  []RawProcess
	MemTotal   uint64
	MemAvail   uint64
	CommitUsed uint64
	CommitMax  uint64
	Cached     uint64
	Volumes    []Volume
	DiskRead   float64 // bytes/s (already a rate)
	DiskWrite  float64
	DiskActive float64 // percent, -1 unknown
	Interfaces []Interface
	GPUs       []GPU
	FreqMHz    float64 // current, 0 unknown
	MaxMHz     float64
	Uptime     time.Duration
	Handles    uint32
	Threads    uint32
}

// Source reads raw counters.
type Source interface {
	Read() (Raw, error)
	Close()
}

// Process is a process with computed rates.
type Process struct {
	PID     uint32  `json:"pid"`
	PPID    uint32  `json:"ppid"`
	Name    string  `json:"name"`
	CPU     float64 `json:"cpu_percent"`
	Memory  uint64  `json:"memory_bytes"`
	IORate  float64 `json:"io_bytes_per_sec"`
	Threads uint32  `json:"threads"`
	Handles uint32  `json:"handles"`
}

// Snapshot is a computed view of the system.
type Snapshot struct {
	Time       time.Time   `json:"time"`
	CPU        float64     `json:"cpu_percent"`
	Cores      []float64   `json:"cores_percent"`
	FreqMHz    float64     `json:"frequency_mhz,omitempty"`
	MaxMHz     float64     `json:"max_frequency_mhz,omitempty"`
	MemTotal   uint64      `json:"memory_total_bytes"`
	MemUsed    uint64      `json:"memory_used_bytes"`
	MemAvail   uint64      `json:"memory_available_bytes"`
	CommitUsed uint64      `json:"commit_used_bytes"`
	CommitMax  uint64      `json:"commit_limit_bytes"`
	Cached     uint64      `json:"cached_bytes"`
	Volumes    []Volume    `json:"volumes"`
	DiskRead   float64     `json:"disk_read_bytes_per_sec"`
	DiskWrite  float64     `json:"disk_write_bytes_per_sec"`
	DiskActive float64     `json:"disk_active_percent"` // -1 when unknown
	NetRecv    float64     `json:"net_recv_bytes_per_sec"`
	NetSent    float64     `json:"net_sent_bytes_per_sec"`
	Interfaces []Interface `json:"interfaces"`
	GPUs       []GPU       `json:"gpus"`
	Processes  []Process   `json:"processes"`
	Uptime     float64     `json:"uptime_seconds"`
	ProcCount  int         `json:"process_count"`
	Threads    uint32      `json:"thread_count"`
	Handles    uint32      `json:"handle_count"`
}

// MemPercent is the share of physical memory in use.
func (s Snapshot) MemPercent() float64 { return pct(float64(s.MemUsed), float64(s.MemTotal)) }

func pct(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	v := part / whole * 100
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

func busy(a, b CoreTimes) float64 {
	total := float64((b.Kernel - a.Kernel) + (b.User - a.User))
	if total <= 0 || b.Kernel < a.Kernel || b.User < a.User || b.Idle < a.Idle {
		return 0
	}
	return pct(total-float64(b.Idle-a.Idle), total)
}

// Compute derives rates and percentages from two readings of the same source.
func Compute(prev, cur Raw) Snapshot {
	s := Snapshot{
		Time: cur.Time, MemTotal: cur.MemTotal, MemAvail: cur.MemAvail,
		CommitUsed: cur.CommitUsed, CommitMax: cur.CommitMax, Cached: cur.Cached,
		Volumes: cur.Volumes, DiskRead: cur.DiskRead, DiskWrite: cur.DiskWrite, DiskActive: cur.DiskActive,
		Interfaces: cur.Interfaces, GPUs: cur.GPUs, FreqMHz: cur.FreqMHz, MaxMHz: cur.MaxMHz,
		Uptime: cur.Uptime.Seconds(), ProcCount: len(cur.Processes), Threads: cur.Threads, Handles: cur.Handles,
	}
	if cur.MemTotal >= cur.MemAvail {
		s.MemUsed = cur.MemTotal - cur.MemAvail
	}
	s.CPU = busy(prev.Total, cur.Total)
	for i := range cur.Cores {
		if i < len(prev.Cores) {
			s.Cores = append(s.Cores, busy(prev.Cores[i], cur.Cores[i]))
		} else {
			s.Cores = append(s.Cores, 0)
		}
	}
	for _, n := range cur.Interfaces {
		s.NetRecv += n.RecvRate
		s.NetSent += n.SentRate
	}

	elapsed := cur.Time.Sub(prev.Time).Seconds()
	// CPU time is measured across all cores; a process using one full core
	// of eight shows 12.5%, as in Task Manager.
	capacity := float64(max(1, len(cur.Cores))) * elapsed * 1e7
	old := make(map[uint32]RawProcess, len(prev.Processes))
	for _, p := range prev.Processes {
		old[p.PID] = p
	}
	for _, p := range cur.Processes {
		out := Process{PID: p.PID, PPID: p.PPID, Name: p.Name, Memory: p.Private, Threads: p.Threads, Handles: p.Handles}
		if o, ok := old[p.PID]; ok && o.Created == p.Created && elapsed > 0 {
			if p.CPUTime >= o.CPUTime {
				out.CPU = pct(float64(p.CPUTime-o.CPUTime), capacity)
			}
			if p.IOBytes >= o.IOBytes {
				out.IORate = float64(p.IOBytes-o.IOBytes) / elapsed
			}
		}
		s.Processes = append(s.Processes, out)
	}
	SortProcesses(s.Processes, "cpu")
	if s.Volumes == nil {
		s.Volumes = []Volume{}
	}
	if s.Interfaces == nil {
		s.Interfaces = []Interface{}
	}
	if s.GPUs == nil {
		s.GPUs = []GPU{}
	}
	if s.Cores == nil {
		s.Cores = []float64{}
	}
	return s
}

// SortProcesses orders processes by "cpu", "memory", "io" or "name".
func SortProcesses(ps []Process, by string) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		switch by {
		case "memory":
			return a.Memory > b.Memory
		case "io":
			return a.IORate > b.IORate
		case "name":
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if a.CPU != b.CPU {
			return a.CPU > b.CPU
		}
		return a.Memory > b.Memory
	})
}

// Sampler keeps the previous reading so each call returns current rates.
type Sampler struct {
	Source Source
	prev   *Raw
}

// Sample reads the source and computes a snapshot against the previous
// reading. The first call primes the sampler and reports zero rates.
func (s *Sampler) Sample() (Snapshot, error) {
	cur, err := s.Source.Read()
	if err != nil {
		return Snapshot{}, err
	}
	prev := cur
	if s.prev != nil {
		prev = *s.prev
	}
	s.prev = &cur
	return Compute(prev, cur), nil
}
