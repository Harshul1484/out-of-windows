package monitor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/system"
)

const (
	cDiskRead                = `\PhysicalDisk(_Total)\Disk Read Bytes/sec`
	cDiskWrite               = `\PhysicalDisk(_Total)\Disk Write Bytes/sec`
	cDiskIdle                = `\PhysicalDisk(_Total)\% Idle Time`
	cNetRecv                 = `\Network Interface(*)\Bytes Received/sec`
	cNetSent                 = `\Network Interface(*)\Bytes Sent/sec`
	cGPUEngine               = `\GPU Engine(*)\Utilization Percentage`
	cGPUMem                  = `\GPU Adapter Memory(*)\Dedicated Usage`
	cProcPerf                = `\Processor Information(_Total)\% Processor Performance`
	cProcFreq                = `\Processor Information(_Total)\Processor Frequency`
	statusInfoLengthMismatch = 0xC0000004
)

// System reads the real system. Create it with NewSystem and Close it.
type System struct {
	q        *pdhQuery
	adapters []GPU // names and memory sizes from the registry
	smi      string
	smiAt    time.Time
	smiGPUs  []GPU
}

// NewSystem opens performance counters.
func NewSystem() *System {
	s := &System{q: openPDH([]string{cDiskRead, cDiskWrite, cDiskIdle, cNetRecv, cNetSent, cGPUEngine, cGPUMem, cProcPerf, cProcFreq})}
	s.adapters = displayAdapters()
	if p, err := exec.LookPath("nvidia-smi"); err == nil {
		s.smi = p
	} else if p := filepath.Join(windowsDir(), "System32", "nvidia-smi.exe"); fileExists(p) {
		s.smi = p
	}
	return s
}

// Close releases the counters.
func (s *System) Close() { s.q.close() }

// Read takes one reading.
func (s *System) Read() (Raw, error) {
	r := Raw{Time: time.Now(), DiskActive: -1}
	var idle, kern, user windows.Filetime
	if ok, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kern)), uintptr(unsafe.Pointer(&user))); ok != 0 {
		r.Total = CoreTimes{Idle: ft(idle), Kernel: ft(kern), User: ft(user)}
	}
	r.Cores = coreTimes()
	r.Processes, r.Threads, r.Handles = processes()

	if m, err := system.MemoryStatus(); err == nil {
		r.MemTotal, r.MemAvail = m.Total, m.Available
	}
	if p, ok := perfInfo(); ok {
		page := uint64(p.PageSize)
		r.CommitUsed, r.CommitMax, r.Cached = uint64(p.CommitTotal)*page, uint64(p.CommitLimit)*page, uint64(p.SystemCache)*page
	}
	for _, root := range fixedDrives() {
		if d, err := system.DiskUsage(root); err == nil {
			r.Volumes = append(r.Volumes, Volume{Root: root, Total: d.Total, Free: d.Free})
		}
	}
	r.Uptime = system.Uptime()

	s.q.collect()
	r.DiskRead, _ = s.q.value(cDiskRead)
	r.DiskWrite, _ = s.q.value(cDiskWrite)
	if v, ok := s.q.value(cDiskIdle); ok {
		r.DiskActive = clamp(100 - v)
	}
	recv, sent := s.q.array(cNetRecv), s.q.array(cNetSent)
	for name, v := range recv {
		r.Interfaces = append(r.Interfaces, Interface{Name: name, RecvRate: v, SentRate: sent[name]})
	}
	if perf, ok := s.q.value(cProcPerf); ok {
		if base, ok := s.q.value(cProcFreq); ok {
			r.FreqMHz, r.MaxMHz = base*perf/100, base
		}
	}
	r.GPUs = s.gpus()
	return r, nil
}

func clamp(v float64) float64 { return pct(v, 100) }

func ft(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
	procGetPerformanceInfo = kernel32.NewProc("K32GetPerformanceInfo")
)

// processorPerformance mirrors SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION.
type processorPerformance struct {
	IdleTime, KernelTime, UserTime, DpcTime, InterruptTime int64
	InterruptCount                                         uint32
	_                                                      uint32
}

func coreTimes() []CoreTimes {
	n := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
	if n == 0 || n > 256 {
		n = 64
	}
	buf := make([]processorPerformance, n)
	var ret uint32
	if err := windows.NtQuerySystemInformation(windows.SystemProcessorPerformanceInformation, unsafe.Pointer(&buf[0]),
		uint32(len(buf))*uint32(unsafe.Sizeof(buf[0])), &ret); err != nil {
		return nil
	}
	count := int(ret / uint32(unsafe.Sizeof(buf[0])))
	out := make([]CoreTimes, 0, count)
	for _, p := range buf[:count] {
		out = append(out, CoreTimes{Idle: uint64(p.IdleTime), Kernel: uint64(p.KernelTime), User: uint64(p.UserTime)})
	}
	return out
}

// processes reads the kernel's process table in one call, like Task
// Manager, without opening a handle to any process.
func processes() ([]RawProcess, uint32, uint32) {
	size := uint32(1 << 20)
	var buf []byte
	for attempt := 0; attempt < 6; attempt++ {
		buf = make([]byte, size)
		var ret uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &ret)
		if err == nil {
			break
		}
		if st, ok := err.(windows.NTStatus); !ok || uint32(st) != statusInfoLengthMismatch {
			return nil, 0, 0
		}
		size = max(size*2, ret+64*1024)
		buf = nil
	}
	if buf == nil {
		return nil, 0, 0
	}
	var out []RawProcess
	var threads, handles uint32
	off := uint32(0)
	for {
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		name := p.ImageName.String()
		if p.UniqueProcessID == 0 {
			name = "System Idle Process"
		}
		threads += p.NumberOfThreads
		handles += p.HandleCount
		if p.UniqueProcessID != 0 { // the idle process is not a real process
			out = append(out, RawProcess{
				PID: uint32(p.UniqueProcessID), PPID: uint32(p.InheritedFromUniqueProcessID), Name: name,
				CPUTime: uint64(p.UserTime + p.KernelTime), Private: uint64(p.WorkingSetPrivateSize),
				IOBytes: uint64(p.ReadTransferCount + p.WriteTransferCount + p.OtherTransferCount),
				Threads: p.NumberOfThreads, Handles: p.HandleCount, Created: p.CreateTime, Session: p.SessionID,
			})
		}
		if p.NextEntryOffset == 0 {
			break
		}
		off += p.NextEntryOffset
		if int(off) >= len(buf) {
			break
		}
	}
	return out, threads, handles
}

// performanceInformation mirrors PERFORMANCE_INFORMATION.
type performanceInformation struct {
	cb                uint32
	CommitTotal       uintptr
	CommitLimit       uintptr
	CommitPeak        uintptr
	PhysicalTotal     uintptr
	PhysicalAvailable uintptr
	SystemCache       uintptr
	KernelTotal       uintptr
	KernelPaged       uintptr
	KernelNonpaged    uintptr
	PageSize          uintptr
	HandleCount       uint32
	ProcessCount      uint32
	ThreadCount       uint32
}

func perfInfo() (performanceInformation, bool) {
	var p performanceInformation
	p.cb = uint32(unsafe.Sizeof(p))
	r, _, _ := procGetPerformanceInfo.Call(uintptr(unsafe.Pointer(&p)), uintptr(p.cb))
	return p, r != 0
}

func fixedDrives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) == windows.DRIVE_FIXED {
			out = append(out, root)
		}
	}
	return out
}

// gpus combines adapter names from the registry, utilization and memory
// from performance counters, and NVIDIA details from nvidia-smi.
func (s *System) gpus() []GPU {
	if s.smi != "" {
		if time.Since(s.smiAt) > 2*time.Second {
			s.smiGPUs, s.smiAt = nvidiaSMI(s.smi), time.Now()
		}
		if len(s.smiGPUs) > 0 {
			return s.smiGPUs
		}
	}
	util := gpuUtilization(s.q.array(cGPUEngine))
	var used float64
	for _, v := range s.q.array(cGPUMem) {
		used += v
	}
	if len(s.adapters) == 0 && util < 0 {
		return nil
	}
	out := make([]GPU, 0, len(s.adapters))
	for i, a := range s.adapters {
		g := GPU{Name: a.Name, MemoryTotal: a.MemoryTotal, Utilization: -1, Temperature: -1}
		if i == 0 { // counters are per adapter LUID; attribute totals to the primary adapter
			g.Utilization, g.MemoryUsed = util, uint64(used)
		}
		out = append(out, g)
	}
	if len(out) == 0 {
		out = append(out, GPU{Name: "GPU", Utilization: util, MemoryUsed: uint64(used), Temperature: -1})
	}
	return out
}

// gpuUtilization reproduces Task Manager's figure: per adapter and engine
// type, sum the utilization of all processes; the GPU's value is its busiest
// engine type.
func gpuUtilization(engines map[string]float64) float64 {
	if engines == nil {
		return -1
	}
	perType := map[string]float64{}
	for inst, v := range engines {
		luid, engtype := "", ""
		parts := strings.Split(inst, "_")
		for i := 0; i+1 < len(parts); i++ {
			switch parts[i] {
			case "luid":
				if i+2 < len(parts) {
					luid = parts[i+1] + "_" + parts[i+2]
				}
			case "engtype":
				engtype = strings.Join(parts[i+1:], "_")
			}
		}
		perType[luid+"|"+engtype] += v
	}
	best := 0.0
	for _, v := range perType {
		best = max(best, v)
	}
	return clamp(best)
}

// displayAdapters lists display adapters and their dedicated memory from the
// device class registry key (read-only).
func displayAdapters() []GPU {
	const class = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, class, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	var out []GPU
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		desc, _, err := sk.GetStringValue("DriverDesc")
		if err == nil && desc != "" && !strings.Contains(strings.ToLower(desc), "basic display") {
			g := GPU{Name: desc, Utilization: -1, Temperature: -1}
			if v, _, err := sk.GetIntegerValue("HardwareInformation.qwMemorySize"); err == nil {
				g.MemoryTotal = v
			} else if b, _, err := sk.GetBinaryValue("HardwareInformation.MemorySize"); err == nil && len(b) >= 4 {
				g.MemoryTotal = uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
			} else if v, _, err := sk.GetIntegerValue("HardwareInformation.MemorySize"); err == nil {
				g.MemoryTotal = v
			}
			out = append(out, g)
		}
		sk.Close()
	}
	return out
}

// nvidiaSMI queries NVIDIA's tool; its CSV numbers are not localized.
func nvidiaSMI(exe string) []GPU {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu",
		"--format=csv,noheader,nounits")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseNvidiaSMI(string(out))
}

func parseNvidiaSMI(out string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 5 {
			continue
		}
		num := func(s string) float64 {
			v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return -1
			}
			return v
		}
		g := GPU{Name: strings.TrimSpace(f[0]), Utilization: num(f[1]), Temperature: num(f[4])}
		if v := num(f[2]); v >= 0 {
			g.MemoryUsed = uint64(v) << 20
		}
		if v := num(f[3]); v >= 0 {
			g.MemoryTotal = uint64(v) << 20
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func windowsDir() string {
	if d, err := windows.GetSystemWindowsDirectory(); err == nil {
		return d
	}
	return `C:\Windows`
}

func fileExists(p string) bool {
	a, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(p))
	return err == nil && a&windows.FILE_ATTRIBUTE_DIRECTORY == 0
}
