package filesystem

import (
	"errors"
	"io/fs"
	"syscall"

	"golang.org/x/sys/windows"
)

// entryFromInfo builds an Entry from os.FileInfo. On Windows, FileInfo from
// os.Lstat and DirEntry.Info carries the raw attribute data, so no extra
// system call is made per file.
func entryFromInfo(path string, info fs.FileInfo) Entry {
	e := Entry{Path: path, Name: info.Name()}
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		e.Attributes = d.FileAttributes
		e.Fingerprint = Fingerprint{
			Dir:       d.FileAttributes&attrDirectory != 0,
			Size:      int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
			LastWrite: d.LastWriteTime.Nanoseconds(),
			Created:   d.CreationTime.Nanoseconds(),
		}
	} else {
		e.Fingerprint = Fingerprint{
			Dir:       info.IsDir(),
			Size:      info.Size(),
			LastWrite: info.ModTime().UnixNano(),
			Created:   info.ModTime().UnixNano(),
		}
	}
	if e.Fingerprint.Dir {
		e.Fingerprint.Size = 0
	}
	e.Reparse = e.Attributes&attrReparsePoint != 0 || info.Mode()&fs.ModeSymlink != 0
	e.Cloud = isCloudAttr(e.Attributes)
	return e
}

// mapError converts OS errors into the package's skip-reason errors.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var errno windows.Errno
	if !errors.As(err, &errno) {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return ErrGone
		case errors.Is(err, fs.ErrPermission):
			return ErrAccessDenied
		}
		return err
	}
	switch errno {
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND, windows.ERROR_INVALID_NAME:
		return ErrGone
	case windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_USER_MAPPED_FILE:
		return ErrInUse
	case windows.ERROR_ACCESS_DENIED, windows.ERROR_PRIVILEGE_NOT_HELD:
		return ErrAccessDenied
	case windows.ERROR_DIR_NOT_EMPTY:
		return ErrNotEmpty
	case windows.ERROR_NOT_READY, windows.ERROR_DEV_NOT_EXIST:
		return ErrGone
	}
	return err
}
