package apps

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AppX packages are listed with PowerShell's Get-AppxPackage: the native
// Windows.Management.Deployment API is WinRT and impractical to call from Go.
// The output is JSON with fixed property names, so it does not depend on the
// display language. Inbox (System-signed), framework and non-removable
// packages are excluded.
const appxScript = `$ErrorActionPreference='Stop';` +
	`$p = Get-AppxPackage | Where-Object { -not $_.IsFramework -and -not $_.NonRemovable -and "$($_.SignatureKind)" -ne 'System' -and -not $_.IsResourcePackage -and -not $_.IsBundle } |` +
	` Select-Object Name,PackageFullName,PackageFamilyName,@{n='Version';e={"$($_.Version)"}},Publisher,InstallLocation,@{n='SignatureKind';e={"$($_.SignatureKind)"}};` +
	`ConvertTo-Json -Compress -Depth 2 -InputObject @($p)`

type appxPackage struct {
	Name              string
	PackageFullName   string
	PackageFamilyName string
	Version           string
	Publisher         string
	InstallLocation   string
	SignatureKind     string
}

func readAppX(ctx context.Context) ([]App, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := powershell(ctx, appxScript)
	if err != nil {
		return nil, fmt.Errorf("Microsoft Store apps could not be listed: %w", err)
	}
	var pkgs []appxPackage
	if err := json.Unmarshal(out, &pkgs); err != nil {
		return nil, fmt.Errorf("Microsoft Store apps: unexpected output: %w", err)
	}
	apps := make([]App, 0, len(pkgs))
	for _, p := range pkgs {
		name, publisher := manifestNames(p)
		apps = append(apps, App{
			ID:              "appx:" + p.PackageFullName,
			Name:            name,
			Version:         p.Version,
			Publisher:       publisher,
			Source:          SourceAppX,
			Scope:           ScopeUser,
			InstallLocation: p.InstallLocation,
			PackageName:     p.PackageFullName,
		})
	}
	return apps, nil
}

// manifestNames reads the display name and publisher from the package
// manifest, resolving ms-resource references; it falls back to the package
// identity when they cannot be resolved.
func manifestNames(p appxPackage) (string, string) {
	name, publisher := p.Name, publisherCN(p.Publisher)
	data, err := os.ReadFile(filepath.Join(p.InstallLocation, "AppxManifest.xml"))
	if err != nil {
		return name, publisher
	}
	var m struct {
		Properties struct {
			DisplayName          string `xml:"DisplayName"`
			PublisherDisplayName string `xml:"PublisherDisplayName"`
		} `xml:"Properties"`
	}
	if xml.Unmarshal(data, &m) != nil {
		return name, publisher
	}
	if n := resolveResource(p, m.Properties.DisplayName); n != "" {
		name = n
	}
	if n := resolveResource(p, m.Properties.PublisherDisplayName); n != "" {
		publisher = n
	}
	return name, publisher
}

var (
	shlwapi                   = windows.NewLazySystemDLL("shlwapi.dll")
	procSHLoadIndirectStringW = shlwapi.NewProc("SHLoadIndirectString")
)

func resolveResource(p appxPackage, value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "ms-resource:") {
		return value
	}
	key := strings.TrimPrefix(value, "ms-resource:")
	var uri string
	switch {
	case strings.HasPrefix(key, "//"):
		uri = "ms-resource:" + key
	case strings.Contains(key, "/"):
		uri = "ms-resource://" + p.Name + "/" + strings.TrimPrefix(key, "/")
	default:
		uri = "ms-resource://" + p.Name + "/Resources/" + key
	}
	src, err := windows.UTF16PtrFromString("@{" + p.PackageFullName + "?" + uri + "}")
	if err != nil {
		return ""
	}
	buf := make([]uint16, 512)
	hr, _, _ := procSHLoadIndirectStringW.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	if hr != 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(buf))
}

// publisherCN extracts the CN from a distinguished name such as
// "CN=Contoso Ltd., O=Contoso Ltd., C=US".
func publisherCN(dn string) string {
	for _, part := range strings.Split(dn, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToUpper(part), "CN=") {
			return strings.Trim(part[3:], `"`)
		}
	}
	return dn
}

// powershell runs a script with Windows PowerShell without a profile and
// without a visible window, returning stdout.
func powershell(ctx context.Context, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// RemoveAppX removes a Microsoft Store / MSIX package for the current user.
func RemoveAppX(ctx context.Context, packageFullName string) error {
	if strings.ContainsAny(packageFullName, "'\"`$;") {
		return fmt.Errorf("unexpected package name %q", packageFullName)
	}
	_, err := powershell(ctx, "$ErrorActionPreference='Stop'; Remove-AppxPackage -Package '"+packageFullName+"'")
	return err
}
