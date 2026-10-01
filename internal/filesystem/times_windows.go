package filesystem

import (
	"time"

	"golang.org/x/sys/windows"
)

// SetTimes sets the creation, last-access and last-write times of a file or
// directory. It is used to build test fixtures and the simulated sandbox; the
// cleanup engine never modifies timestamps.
func SetTimes(path string, created, modified time.Time) error {
	p, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return mapError(err)
	}
	defer windows.CloseHandle(h)
	c := windows.NsecToFiletime(created.UnixNano())
	m := windows.NsecToFiletime(modified.UnixNano())
	return windows.SetFileTime(h, &c, &m, &m)
}
