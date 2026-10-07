package doctor

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/system"
)

// System probes the running Windows system. It only reads.
type System struct{}

// Drives returns the capacity of every fixed drive.
func (System) Drives() ([]Drive, error) {
	var out []Drive
	for _, root := range system.FixedDrives() {
		d, err := system.DiskUsage(root)
		if err != nil {
			continue // e.g. an unformatted volume
		}
		out = append(out, Drive{Root: root, Total: d.Total, Free: d.Free})
	}
	return out, nil
}

// keyExists reports whether an HKLM key exists (64-bit view). Errors other
// than "not found" are returned so they can be reported as unknown.
func keyExists(path string) (bool, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	k.Close()
	return true, nil
}

// Reboot reads the pending-restart flags.
func (System) Reboot() (Reboot, error) {
	var r Reboot
	var err error
	if r.ComponentServicing, err = keyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`); err != nil {
		r.Unreadable = append(r.Unreadable, "component servicing")
	}
	if r.WindowsUpdate, err = keyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`); err != nil {
		r.Unreadable = append(r.Unreadable, "Windows Update")
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		r.Unreadable = append(r.Unreadable, "pending file renames")
		return r, nil
	}
	defer k.Close()
	for _, name := range []string{"PendingFileRenameOperations", "PendingFileRenameOperations2"} {
		v, _, err := k.GetStringsValue(name)
		if err == nil {
			for _, s := range v {
				if s != "" {
					r.FileRenames = true
				}
			}
		}
	}
	return r, nil
}

// Update reads Windows Update settings from the registry.
func (System) Update() (Update, error) {
	u := Update{ServiceStart: -1}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\wuauserv`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("Start"); err == nil {
			u.ServiceStart = int(v)
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU`,
		registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		if v, _, err := k.GetIntegerValue("NoAutoUpdate"); err == nil && v == 1 {
			u.NoAutoUpdate = true
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate`,
		registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		if v, _, err := k.GetStringValue("WUServer"); err == nil && v != "" {
			u.Managed = true
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\WindowsUpdate\UX\Settings`,
		registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		for _, name := range []string{"PauseUpdatesExpiryTime", "PauseQualityUpdatesEndTime", "PauseFeatureUpdatesEndTime"} {
			if v, _, err := k.GetStringValue(name); err == nil {
				if t, err := time.Parse(time.RFC3339, v); err == nil && t.After(u.PausedUntil) {
					u.PausedUntil = t
				}
			}
		}
		k.Close()
	}
	return u, nil
}

// Adapters lists network adapters (except loopback and tunnels) with their
// addresses, gateways and DNS servers, from GetAdaptersAddresses. No traffic
// is sent.
func (System) Adapters() ([]Adapter, error) {
	const flags = windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST
	size := uint32(16 * 1024)
	var buf []byte
	for i := 0; i < 4; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) || i == 3 {
			return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
	}
	var out []Adapter
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK || a.IfType == windows.IF_TYPE_TUNNEL {
			continue
		}
		ad := Adapter{Name: windows.UTF16PtrToString(a.FriendlyName), Up: a.OperStatus == windows.IfOperStatusUp,
			Addresses: []string{}, Gateways: []string{}, DNS: []string{}}
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip != nil && !ip.IsLinkLocalUnicast() {
				ad.Addresses = append(ad.Addresses, ip.String())
			}
		}
		for g := a.FirstGatewayAddress; g != nil; g = g.Next {
			if ip := g.Address.IP(); ip != nil && !ip.IsUnspecified() {
				ad.Gateways = append(ad.Gateways, ip.String())
			}
		}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			if ip := d.Address.IP(); ip != nil {
				ad.DNS = append(ad.DNS, ip.String())
			}
		}
		out = append(out, ad)
	}
	return out, nil
}

// PackageManagers finds winget, scoop and choco on PATH.
func (System) PackageManagers() map[string]string {
	out := map[string]string{}
	for _, pm := range []string{"winget", "scoop", "choco"} {
		if p, err := exec.LookPath(pm); err == nil {
			out[pm] = p
		}
	}
	return out
}

// Writable checks that files can be created in dir without creating any.
func (System) Writable(dir string) error { return system.CanCreateIn(dir) }
