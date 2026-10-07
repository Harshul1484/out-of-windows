package optimize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/system"
)

// System runs the tasks on the real Windows system.
type System struct{}

var (
	dnsapi                    = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsFlushResolverCache = dnsapi.NewProc("DnsFlushResolverCache")
)

// FlushDNS calls DnsFlushResolverCache, which ipconfig /flushdns uses.
func (System) FlushDNS(ctx context.Context) error {
	if err := procDnsFlushResolverCache.Find(); err != nil {
		return fmt.Errorf("the DNS Client API is not available: %w", err)
	}
	r, _, callErr := procDnsFlushResolverCache.Call()
	if r == 0 {
		return fmt.Errorf("the DNS Client did not flush its cache (is the DNS Client service running?): %v", callErr)
	}
	return nil
}

// doCacheDir is the default Delivery Optimization cache folder, owned by the
// Network Service account.
func doCacheDir() string {
	win, err := windows.GetWindowsDirectory()
	if err != nil {
		return ""
	}
	return filepath.Join(win, `ServiceProfiles\NetworkService\AppData\Local\Microsoft\Windows\DeliveryOptimization\Cache`)
}

func doModuleInstalled() bool {
	_, err := os.Stat(filepath.Join(system.SystemDir(), `WindowsPowerShell\v1.0\Modules\DeliveryOptimization`))
	return err == nil
}

// DeliveryOptimization reports whether the cmdlets exist and measures the
// cache folder read-only (links are not followed). The folder is readable
// only by administrators, so its size is unknown (-1) otherwise.
func (System) DeliveryOptimization(ctx context.Context, elevated bool) DOCache {
	c := DOCache{Available: doModuleInstalled(), Bytes: -1, Path: doCacheDir()}
	if !c.Available && elevated {
		_, err := powershell(ctx, 30*time.Second, "Get-Command Delete-DeliveryOptimizationCache -ErrorAction Stop | Out-Null")
		c.Available = err == nil
	}
	if !elevated || c.Path == "" {
		return c
	}
	if _, err := os.Lstat(c.Path); errors.Is(err, os.ErrNotExist) {
		c.Bytes = 0
		return c
	}
	var total int64
	unreadable := false
	err := filesystem.Walk(ctx, c.Path, func(e filesystem.Entry) bool {
		total += e.Size()
		return true
	}, func(string, error) { unreadable = true })
	if err == nil && !unreadable {
		c.Bytes = total
	}
	return c
}

// ClearDeliveryOptimization runs Delete-DeliveryOptimizationCache, the
// owner's supported way to empty the cache (pinned files are kept).
func (System) ClearDeliveryOptimization(ctx context.Context) error {
	_, err := powershell(ctx, 10*time.Minute, "Delete-DeliveryOptimizationCache -Force")
	return err
}

// Storage property query (IOCTL_STORAGE_QUERY_PROPERTY).
const (
	ioctlStorageQueryProperty = 0x2D1400
	storageDeviceSeekPenalty  = 7
	storageDeviceTrim         = 8
)

type storagePropertyQuery struct {
	PropertyID uint32
	QueryType  uint32
	Additional [4]byte
}

type boolDescriptor struct {
	Version uint32
	Size    uint32
	Value   byte
	_       [3]byte
}

func queryBool(h windows.Handle, property uint32) (bool, error) {
	q := storagePropertyQuery{PropertyID: property}
	var d boolDescriptor
	var n uint32
	err := windows.DeviceIoControl(h, ioctlStorageQueryProperty, (*byte)(unsafe.Pointer(&q)), uint32(unsafe.Sizeof(q)),
		(*byte)(unsafe.Pointer(&d)), uint32(unsafe.Sizeof(d)), &n, nil)
	if err != nil {
		return false, err
	}
	if n < 9 {
		return false, errors.New("short storage property")
	}
	return d.Value != 0, nil
}

// SSDVolumes lists fixed NTFS or ReFS volumes on drives without a seek
// penalty that support TRIM. The volume is opened with no access rights,
// which is enough to query storage properties and changes nothing.
func (System) SSDVolumes(ctx context.Context) ([]Volume, error) {
	var out []Volume
	for _, root := range system.FixedDrives() {
		fs := fileSystem(root)
		if fs != "NTFS" && fs != "ReFS" {
			continue
		}
		dev, _ := windows.UTF16PtrFromString(`\\.\` + root[:2])
		h, err := windows.CreateFile(dev, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			continue
		}
		seek, err1 := queryBool(h, storageDeviceSeekPenalty)
		trim, err2 := queryBool(h, storageDeviceTrim)
		windows.CloseHandle(h)
		if err1 == nil && err2 == nil && !seek && trim {
			out = append(out, Volume{Root: root, FileSystem: fs})
		}
	}
	return out, nil
}

func fileSystem(root string) string {
	r, _ := windows.UTF16PtrFromString(root)
	buf := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(r, nil, 0, nil, nil, nil, &buf[0], uint32(len(buf))); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf)
}

var driveRoot = regexp.MustCompile(`^[A-Za-z]:\\$`)

// ReTrim runs Optimize-Volume -ReTrim on one volume.
func (System) ReTrim(ctx context.Context, v Volume) error {
	if !driveRoot.MatchString(v.Root) {
		return fmt.Errorf("unexpected volume %q", v.Root)
	}
	_, err := powershell(ctx, 15*time.Minute, "Optimize-Volume -DriveLetter "+strings.ToUpper(v.Root[:1])+" -ReTrim")
	return err
}

// AnalyzeComponentStore runs DISM's read-only analysis of the component
// store. If ctx is cancelled oow stops waiting; the analysis changes nothing
// and finishes on its own.
func (System) AnalyzeComponentStore(ctx context.Context) (ComponentStore, error) {
	out, exit, err := runDISM(ctx, dismAnalyzeTimeout, true, dismAnalyzeArgs)
	if err != nil {
		return ComponentStore{}, err
	}
	if exit != 0 {
		return ComponentStore{}, dismError(out, exit)
	}
	return ParseComponentStoreReport(out)
}

// CleanupComponentStore runs DISM /StartComponentCleanup and waits for it.
// Cancelling ctx does not stop DISM or the wait: a servicing operation is
// never interrupted midway.
func (System) CleanupComponentStore(ctx context.Context) (restart bool, err error) {
	out, exit, err := runDISM(ctx, dismCleanupTimeout, false, dismCleanupArgs)
	switch {
	case err != nil:
		return false, err
	case exit == dismRestartRequired:
		return true, nil
	case exit != 0:
		return false, dismError(out, exit)
	}
	return false, nil
}

// runDISM starts Dism.exe from System32 by full path (never from PATH), with
// fixed arguments, hidden, in its own console and process group so Ctrl+C in
// oow's console never reaches it. oow never terminates DISM: when the time
// limit passes (or ctx is cancelled and stopOnCancel is set) it stops waiting
// and DISM finishes on its own.
func runDISM(ctx context.Context, timeout time.Duration, stopOnCancel bool, args []string) ([]byte, uint32, error) {
	if stopOnCancel && ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	exe := filepath.Join(system.SystemDir(), "Dism.exe")
	if fi, err := os.Stat(exe); err != nil || !fi.Mode().IsRegular() {
		return nil, 0, ErrNoDISM
	}
	var isWow64 bool
	if windows.IsWow64Process(windows.CurrentProcess(), &isWow64) == nil && isWow64 {
		return nil, 0, errors.New("a 32-bit build cannot run the 64-bit DISM: use the 64-bit " + buildinfo.Name)
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("could not start DISM: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var cancelled <-chan struct{}
	if stopOnCancel {
		cancelled = ctx.Done()
	}
	select {
	case err := <-done:
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			return out.Bytes(), 0, fmt.Errorf("DISM: %w", err)
		}
		return out.Bytes(), uint32(cmd.ProcessState.ExitCode()), nil
	case <-cancelled:
		return nil, 0, ctx.Err()
	case <-timer.C:
		return nil, 0, fmt.Errorf("DISM did not finish within %s and was left running (stopping it midway is not safe); "+
			"run `%s optimize --dry-run` later to see the result", timeout, buildinfo.Name)
	}
}

// powershell runs a fixed script with Windows PowerShell from System32 (not
// from PATH), hidden, with a timeout.
func powershell(ctx context.Context, timeout time.Duration, script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exe := filepath.Join(system.SystemDir(), `WindowsPowerShell\v1.0\powershell.exe`)
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		"$ErrorActionPreference='Stop'; "+script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%s", firstLine(string(ee.Stderr)))
		}
		return out, err
	}
	return out, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
