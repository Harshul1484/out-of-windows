package installer

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// versionInfo reads a file's version resource strings natively with
// GetFileVersionInfo and VerQueryValue. The file is mapped as data, never run.
func versionInfo(path string) (map[string]string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return nil, err
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return nil, err
	}
	block := unsafe.Pointer(&buf[0])
	var langs []string
	var p unsafe.Pointer
	var n uint32
	if windows.VerQueryValue(block, `\VarFileInfo\Translation`, unsafe.Pointer(&p), &n) == nil && n >= 4 {
		pairs := unsafe.Slice((*uint16)(p), n/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			langs = append(langs, fmt.Sprintf("%04x%04x", pairs[i], pairs[i+1]))
		}
	}
	langs = append(langs, "040904b0", "040904e4", "000004b0")
	out := map[string]string{}
	for _, key := range []string{"ProductName", "CompanyName", "FileDescription", "ProductVersion",
		"FileVersion", "OriginalFilename", "InternalName", "Comments"} {
		for _, l := range langs {
			if windows.VerQueryValue(block, `\StringFileInfo\`+l+`\`+key, unsafe.Pointer(&p), &n) != nil || n == 0 {
				continue
			}
			s := strings.TrimSpace(windows.UTF16PtrToString((*uint16)(p)))
			if s != "" {
				out[key] = s
				break
			}
		}
	}
	return out, nil
}

var (
	msi                                    = windows.NewLazySystemDLL("msi.dll")
	procMsiOpenDatabaseW                   = msi.NewProc("MsiOpenDatabaseW")
	procMsiDatabaseOpenViewW               = msi.NewProc("MsiDatabaseOpenViewW")
	procMsiViewExecute                     = msi.NewProc("MsiViewExecute")
	procMsiViewFetch                       = msi.NewProc("MsiViewFetch")
	procMsiViewClose                       = msi.NewProc("MsiViewClose")
	procMsiRecordGetStringW                = msi.NewProc("MsiRecordGetStringW")
	procMsiCloseHandle                     = msi.NewProc("MsiCloseHandle")
	procMsiGetSummaryInformationW          = msi.NewProc("MsiGetSummaryInformationW")
	procMsiSummaryInfoGetPropertyW         = msi.NewProc("MsiSummaryInfoGetPropertyW")
	errNoMoreItems                 uintptr = 259
	errMoreData                    uintptr = 234
)

// msiProperties reads the Property table of a Windows Installer package
// through the Windows Installer database API (read-only; nothing installs).
func msiProperties(path string) (map[string]string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var db uintptr
	const readOnly = 0
	if rc, _, _ := procMsiOpenDatabaseW.Call(uintptr(unsafe.Pointer(p)), readOnly, uintptr(unsafe.Pointer(&db))); rc != 0 {
		return nil, fmt.Errorf("MsiOpenDatabase: error %d", rc)
	}
	defer procMsiCloseHandle.Call(db)
	q, _ := windows.UTF16PtrFromString("SELECT `Property`, `Value` FROM `Property`")
	var view uintptr
	if rc, _, _ := procMsiDatabaseOpenViewW.Call(db, uintptr(unsafe.Pointer(q)), uintptr(unsafe.Pointer(&view))); rc != 0 {
		return nil, fmt.Errorf("MsiDatabaseOpenView: error %d", rc)
	}
	defer procMsiCloseHandle.Call(view)
	if rc, _, _ := procMsiViewExecute.Call(view, 0); rc != 0 {
		return nil, fmt.Errorf("MsiViewExecute: error %d", rc)
	}
	defer procMsiViewClose.Call(view)
	want := map[string]bool{"ProductName": true, "ProductVersion": true, "Manufacturer": true, "ProductCode": true}
	out := map[string]string{}
	for i := 0; i < 10000; i++ {
		var rec uintptr
		rc, _, _ := procMsiViewFetch.Call(view, uintptr(unsafe.Pointer(&rec)))
		if rc == errNoMoreItems {
			break
		}
		if rc != 0 {
			return out, fmt.Errorf("MsiViewFetch: error %d", rc)
		}
		name, err1 := recordString(rec, 1)
		if err1 == nil && want[name] {
			if v, err := recordString(rec, 2); err == nil {
				out[name] = strings.TrimSpace(v)
			}
		}
		procMsiCloseHandle.Call(rec)
	}
	return out, nil
}

func recordString(rec uintptr, field int) (string, error) {
	buf := make([]uint16, 256)
	for {
		n := uint32(len(buf))
		rc, _, _ := procMsiRecordGetStringW.Call(rec, uintptr(field), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
		switch rc {
		case 0:
			return windows.UTF16ToString(buf[:n]), nil
		case errMoreData:
			if n > 1<<20 {
				return "", errors.New("value too long")
			}
			buf = make([]uint16, n+1)
		default:
			return "", fmt.Errorf("MsiRecordGetString: error %d", rc)
		}
	}
}

// msiSummary reads a package's or patch's summary information: title,
// subject and, for patches, the product codes it applies to (PID_TEMPLATE).
func msiSummary(path string) (title, subject string, targets []string, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", "", nil, err
	}
	var si uintptr
	if rc, _, _ := procMsiGetSummaryInformationW.Call(0, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&si))); rc != 0 {
		return "", "", nil, fmt.Errorf("MsiGetSummaryInformation: error %d", rc)
	}
	defer procMsiCloseHandle.Call(si)
	get := func(pid uintptr) string {
		var typ uint32
		var ival int32
		var ft windows.Filetime
		buf := make([]uint16, 1024)
		n := uint32(len(buf))
		rc, _, _ := procMsiSummaryInfoGetPropertyW.Call(si, pid, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&ival)),
			uintptr(unsafe.Pointer(&ft)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
		if rc != 0 || typ != 30 { // VT_LPSTR
			return ""
		}
		return strings.TrimSpace(windows.UTF16ToString(buf[:min(int(n), len(buf))]))
	}
	title, subject = get(2), get(3)
	for _, t := range strings.Split(get(7), ";") {
		if t = strings.TrimSpace(t); strings.HasPrefix(t, "{") {
			targets = append(targets, t)
		}
	}
	return title, subject, targets, nil
}

// SystemFolders returns the user's Downloads, Desktop and Documents folders
// from the Known Folder API (redirection-aware), skipping unavailable ones.
func SystemFolders() []string {
	var out []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Downloads, windows.FOLDERID_Desktop, windows.FOLDERID_Documents} {
		if p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT); err == nil && p != "" {
			out = append(out, p)
		}
	}
	return out
}
