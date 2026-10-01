// Package system reads information about the running Windows system:
// version, elevation, and live resource usage. Everything here is read-only.
package system

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// OSInfo describes the Windows installation.
type OSInfo struct {
	Name           string `json:"name"`            // "Windows 11 Home"
	DisplayVersion string `json:"display_version"` // "24H2"
	Build          uint32 `json:"build"`
	UBR            uint32 `json:"ubr"` // update build revision
	Edition        string `json:"edition"`
}

// Short returns e.g. "Windows 11".
func (o OSInfo) Short() string {
	if o.Build >= 22000 {
		return "Windows 11"
	}
	if o.Build >= 10240 {
		return "Windows 10"
	}
	return "Windows"
}

// String returns e.g. "Windows 11 Home 24H2 (build 26100.2894)".
func (o OSInfo) String() string {
	s := o.Name
	if o.DisplayVersion != "" {
		s += " " + o.DisplayVersion
	}
	return fmt.Sprintf("%s (build %d.%d)", s, o.Build, o.UBR)
}

// Supported reports whether this Windows version is supported (Windows 10
// 1809 / build 17763 or newer).
func (o OSInfo) Supported() bool { return o.Build >= 17763 }

// OS reads version information. RtlGetVersion is used for the build number
// because GetVersionEx lies to unmanifested programs; the registry supplies
// edition and marketing version. ProductName still says "Windows 10" on
// Windows 11, so the name is derived from the build number.
func OS() OSInfo {
	v := windows.RtlGetVersion()
	info := OSInfo{Build: v.BuildNumber}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		info.Name, _, _ = k.GetStringValue("ProductName")
		info.DisplayVersion, _, _ = k.GetStringValue("DisplayVersion")
		if info.DisplayVersion == "" {
			info.DisplayVersion, _, _ = k.GetStringValue("ReleaseId")
		}
		info.Edition, _, _ = k.GetStringValue("EditionID")
		if ubr, _, err := k.GetIntegerValue("UBR"); err == nil {
			info.UBR = uint32(ubr)
		}
	}
	if info.Name == "" {
		info.Name = info.Short()
	}
	if info.Build >= 22000 {
		info.Name = strings.Replace(info.Name, "Windows 10", "Windows 11", 1)
	}
	return info
}

// IsElevated reports whether the process runs with an elevated token.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// Memory is a physical memory snapshot.
type Memory struct {
	Total       uint64  `json:"total_bytes"`
	Available   uint64  `json:"available_bytes"`
	Used        uint64  `json:"used_bytes"`
	CommitTotal uint64  `json:"commit_limit_bytes"`
	CommitUsed  uint64  `json:"commit_used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// MemoryStatus reads physical memory usage.
func MemoryStatus() (Memory, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return Memory{}, err
	}
	used := m.TotalPhys - m.AvailPhys
	out := Memory{
		Total:       m.TotalPhys,
		Available:   m.AvailPhys,
		Used:        used,
		CommitTotal: m.TotalPageFile,
		CommitUsed:  m.TotalPageFile - m.AvailPageFile,
	}
	if m.TotalPhys > 0 {
		out.UsedPercent = float64(used) / float64(m.TotalPhys) * 100
	}
	return out, nil
}

// Disk is the capacity of one volume.
type Disk struct {
	Root        string  `json:"root"`
	Total       uint64  `json:"total_bytes"`
	Free        uint64  `json:"free_bytes"`
	Used        uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// DiskUsage reads capacity for the volume containing root (e.g. C:\).
func DiskUsage(root string) (Disk, error) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Disk{}, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return Disk{}, err
	}
	d := Disk{Root: root, Total: total, Free: free, Used: total - free}
	if total > 0 {
		d.UsedPercent = float64(d.Used) / float64(total) * 100
	}
	return d, nil
}

// SystemDrive returns the drive root holding Windows, e.g. C:\.
func SystemDrive() string {
	if win, err := windows.GetSystemWindowsDirectory(); err == nil && len(win) >= 3 {
		return strings.ToUpper(win[:1]) + `:\`
	}
	return `C:\`
}

// CPUSampler computes total CPU utilisation between successive calls.
type CPUSampler struct {
	mu                       sync.Mutex
	lastIdle, lastKern, last uint64
	lastUser                 uint64
	primed                   bool
}

func filetimeU64(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

// Sample returns CPU usage in percent since the previous call. The first
// call primes the sampler and reports ok=false.
func (s *CPUSampler) Sample() (percent float64, ok bool) {
	var idle, kern, user windows.Filetime
	r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kern)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0, false
	}
	i, k, u := filetimeU64(idle), filetimeU64(kern), filetimeU64(user)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.lastIdle, s.lastKern, s.lastUser, s.primed = i, k, u, true }()
	if !s.primed {
		return 0, false
	}
	// Kernel time includes idle time.
	total := (k - s.lastKern) + (u - s.lastUser)
	if total == 0 {
		return 0, false
	}
	busy := total - (i - s.lastIdle)
	return float64(busy) / float64(total) * 100, true
}

// Process is a running process.
type Process struct {
	PID  uint32 `json:"pid"`
	PPID uint32 `json:"ppid"`
	Name string `json:"name"`
}

// Processes lists running processes.
func Processes() ([]Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Process
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		out = append(out, Process{PID: e.ProcessID, PPID: e.ParentProcessID, Name: windows.UTF16ToString(e.ExeFile[:])})
	}
	return out, nil
}

// RunningNames returns the lower-case executable names of running processes.
func RunningNames() map[string]bool {
	procs, err := Processes()
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(procs))
	for _, p := range procs {
		out[strings.ToLower(p.Name)] = true
	}
	return out
}

// Uptime returns time since boot.
func Uptime() time.Duration {
	return time.Duration(windows.DurationSinceBoot())
}
