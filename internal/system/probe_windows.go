package system

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Presence is whether a path named by a configuration value (a startup
// command, a PATH entry) exists. Unknown is used whenever checking could be
// wrong or have side effects, so that nothing is ever called "missing" on a
// guess.
type Presence string

// Presence values.
const (
	Present Presence = "found"
	Absent  Presence = "missing"
	Unknown Presence = "unknown"
)

// ProbePath reports whether the absolute path p exists, without touching the
// network or removable media: UNC paths and paths on drives that are not
// fixed (USB sticks, network and optical drives, drives that are not
// connected) are Unknown, as are paths that cannot be read. The note says why
// a result is Unknown.
func ProbePath(p string) (Presence, string) {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return Unknown, "no path"
	case strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`):
		return Unknown, "network path (not checked)"
	case len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/'):
		return Unknown, "not an absolute path"
	}
	root := strings.ToUpper(p[:1]) + `:\`
	rp, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Unknown, "invalid path"
	}
	switch windows.GetDriveType(rp) {
	case windows.DRIVE_FIXED:
	case windows.DRIVE_NO_ROOT_DIR, windows.DRIVE_UNKNOWN:
		return Unknown, fmt.Sprintf("drive %s is not connected", root[:2])
	default:
		return Unknown, fmt.Sprintf("on removable or network drive %s (not checked)", root[:2])
	}
	_, err = os.Lstat(p)
	switch {
	case err == nil:
		return Present, ""
	case errors.Is(err, fs.ErrNotExist):
		return Absent, ""
	default:
		return Unknown, "could not be checked: " + err.Error()
	}
}

// CanCreateIn reports whether the current user may create files and folders
// in dir. It asks the system for the access by opening the directory with
// FILE_ADD_FILE and FILE_ADD_SUBDIRECTORY rights and closes it again, so
// nothing is written. Backup privileges are not enabled, so ACLs apply.
func CanCreateIn(dir string) error {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	const fileAddFile, fileAddSubdirectory = 0x2, 0x4
	h, err := windows.CreateFile(p, fileAddFile|fileAddSubdirectory,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}

// FixedDrives lists the roots of fixed drives, e.g. C:\.
func FixedDrives() []string {
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

// SystemDir returns the System32 directory (never taken from PATH).
func SystemDir() string {
	if d, err := windows.GetSystemDirectory(); err == nil {
		return d
	}
	return `C:\Windows\System32`
}
