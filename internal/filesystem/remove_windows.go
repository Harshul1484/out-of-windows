package filesystem

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// CheckFunc re-validates the OS-resolved final path of an object right before
// it is deleted. Returning an error refuses the deletion.
type CheckFunc func(finalPath string) error

// RemoveVerified deletes the file or empty directory at path if, and only if:
//
//   - it is not a reparse point (the link itself is not removed either),
//   - it is not a cloud placeholder,
//   - it is the same object the scan saw (type, size, timestamps),
//   - its OS-resolved final path is inside the deletion fence, and
//   - check approves that final path.
//
// All checks and the deletion use one handle, so the object cannot be swapped
// between verification and deletion. It returns one of the package's skip
// errors, ErrPolicy (wrapping the check error), or nil on success.
func RemoveVerified(path string, want Fingerprint, check CheckFunc) error {
	h, err := openForDelete(path)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return mapError(err)
	}
	if info.FileAttributes&attrReparsePoint != 0 {
		return ErrReparsePoint
	}
	if isCloudAttr(info.FileAttributes) {
		return ErrCloudFile
	}
	if !sameObject(info, want) {
		return ErrChanged
	}

	final, err := finalPathByHandle(h)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve final path: %v", ErrPolicy, err)
	}
	if err := checkFence(final); err != nil {
		return err
	}
	if check != nil {
		if err := check(final); err != nil {
			return fmt.Errorf("%w: %v", ErrPolicy, err)
		}
	}
	return deleteByHandle(h, info.FileAttributes)
}

func sameObject(info windows.ByHandleFileInformation, want Fingerprint) bool {
	isDir := info.FileAttributes&attrDirectory != 0
	if isDir != want.Dir {
		return false
	}
	if info.CreationTime.Nanoseconds() != want.Created {
		return false
	}
	if isDir {
		// A directory's write time changes whenever its children change,
		// including when we delete them, so only its creation time is stable.
		return true
	}
	size := int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	return size == want.Size && info.LastWriteTime.Nanoseconds() == want.LastWrite
}

func openForDelete(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return 0, fmt.Errorf("%w: invalid path", ErrPolicy)
	}
	const share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	const flags = windows.FILE_FLAG_OPEN_REPARSE_POINT | windows.FILE_FLAG_BACKUP_SEMANTICS
	access := uint32(windows.DELETE | windows.FILE_READ_ATTRIBUTES | windows.FILE_WRITE_ATTRIBUTES)
	h, err := windows.CreateFile(p, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// Some ACLs grant DELETE but not FILE_WRITE_ATTRIBUTES. The
		// attribute write is only needed for the read-only fallback.
		access = windows.DELETE | windows.FILE_READ_ATTRIBUTES
		h, err = windows.CreateFile(p, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	}
	if err != nil {
		return 0, mapError(err)
	}
	return h, nil
}

// fileDispositionInfoEx mirrors FILE_DISPOSITION_INFO_EX.
type fileDispositionInfoEx struct {
	Flags uint32
}

// fileDispositionInfo mirrors FILE_DISPOSITION_INFO.
type fileDispositionInfo struct {
	DeleteFile uint8
}

// fileBasicInfo mirrors FILE_BASIC_INFO.
type fileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

func deleteByHandle(h windows.Handle, attrs uint32) error {
	// Windows 10 1809+: POSIX semantics remove the name immediately even if
	// another process holds a shared handle, and the read-only attribute is
	// ignored without having to modify it.
	ex := fileDispositionInfoEx{Flags: windows.FILE_DISPOSITION_DELETE |
		windows.FILE_DISPOSITION_POSIX_SEMANTICS |
		windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE}
	err := windows.SetFileInformationByHandle(h, windows.FileDispositionInfoEx,
		(*byte)(unsafe.Pointer(&ex)), uint32(unsafe.Sizeof(ex)))
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) &&
		!errors.Is(err, windows.ERROR_NOT_SUPPORTED) &&
		!errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return mapError(err)
	}

	// Older Windows or a file system without POSIX delete (FAT32, exFAT).
	if attrs&attrReadOnly != 0 {
		basic := fileBasicInfo{FileAttributes: attrs &^ attrReadOnly}
		if basic.FileAttributes == 0 {
			basic.FileAttributes = windows.FILE_ATTRIBUTE_NORMAL
		}
		if err := windows.SetFileInformationByHandle(h, windows.FileBasicInfo,
			(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
			return mapError(err)
		}
	}
	classic := fileDispositionInfo{DeleteFile: 1}
	err = windows.SetFileInformationByHandle(h, windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&classic)), uint32(unsafe.Sizeof(classic)))
	if err != nil && attrs&attrReadOnly != 0 {
		// Put the read-only attribute back if we could not delete.
		basic := fileBasicInfo{FileAttributes: attrs}
		_ = windows.SetFileInformationByHandle(h, windows.FileBasicInfo,
			(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic)))
	}
	return mapError(err)
}

// FinalPath resolves path (following links) to the normalized final path the
// OS reports for it.
func FinalPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", mapError(err)
	}
	defer windows.CloseHandle(h)
	return finalPathByHandle(h)
}

// GetFinalPathNameByHandle flags (not exported by x/sys/windows).
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
)

func finalPathByHandle(h windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)),
			fileNameNormalized|volumeNameDOS)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return safety.Normalize(windows.UTF16ToString(buf[:n]))
		}
		buf = make([]uint16, n+1)
	}
}

// extendedPath adds the \\?\ prefix so long paths and names with trailing
// dots or spaces are addressed exactly as enumerated.
func extendedPath(p string) string {
	switch {
	case strings.HasPrefix(p, `\\?\`):
		return p
	case strings.HasPrefix(p, `\\`):
		return `\\?\UNC\` + p[2:]
	case len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'):
		return `\\?\` + strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// FreeSpace returns the bytes available to the caller on the volume that
// holds path.
func FreeSpace(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &totalFree); err != nil {
		return 0, mapError(err)
	}
	return avail, nil
}

// VolumeOf returns the volume root (e.g. C:\) for a normalized path.
func VolumeOf(path string) string {
	n, err := safety.Normalize(path)
	if err != nil || len(n) < 3 || n[1] != ':' {
		return ""
	}
	return n[:3]
}
