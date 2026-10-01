package filesystem

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrNoRecycleBin is returned for volumes without a Recycle Bin (removable
// and network drives), where "recycling" would delete permanently.
var ErrNoRecycleBin = errors.New("the drive has no Recycle Bin")

// Recycler moves a verified path to a Recycle Bin.
type Recycler interface {
	Recycle(finalPath string) error
}

// Verify checks, through a handle that does not follow reparse points, that
// path is still the object described by want, is not a link or cloud
// placeholder, and that its OS-resolved final path is inside the deletion
// fence and approved by check. It returns the final path.
func Verify(path string, want Fingerprint, check CheckFunc) (string, error) {
	p, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return "", fmt.Errorf("%w: invalid path", ErrPolicy)
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", mapError(err)
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", mapError(err)
	}
	if info.FileAttributes&attrReparsePoint != 0 {
		return "", ErrReparsePoint
	}
	if isCloudAttr(info.FileAttributes) {
		return "", ErrCloudFile
	}
	if !sameObject(info, want) {
		return "", ErrChanged
	}
	final, err := finalPathByHandle(h)
	if err != nil {
		return "", fmt.Errorf("%w: cannot resolve final path: %v", ErrPolicy, err)
	}
	if err := checkFence(final); err != nil {
		return "", err
	}
	if check != nil {
		if err := check(final); err != nil {
			return "", fmt.Errorf("%w: %v", ErrPolicy, err)
		}
	}
	return final, nil
}

// RecycleVerified verifies path (see Verify) and moves it to the Recycle Bin
// with r. Folders are moved as a whole.
func RecycleVerified(path string, want Fingerprint, check CheckFunc, r Recycler) error {
	final, err := Verify(path, want, check)
	if err != nil {
		return err
	}
	return r.Recycle(final)
}

// shFileOpStruct mirrors SHFILEOPSTRUCTW (64-bit layout).
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

const (
	foDelete            = 0x0003
	fofSilent           = 0x0004
	fofNoConfirmation   = 0x0010
	fofAllowUndo        = 0x0040
	fofNoErrorUI        = 0x0400
	fofWantNukeWarning  = 0x4000
	errorCancelledShell = 0x4C7 // ERROR_CANCELLED
)

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
)

// ShellRecycler moves items to the Windows Recycle Bin through the Shell,
// like Delete in File Explorer. Only fixed drives are accepted.
type ShellRecycler struct{}

// Recycle moves finalPath to the Recycle Bin.
func (ShellRecycler) Recycle(finalPath string) error {
	vol := VolumeOf(finalPath)
	if vol == "" {
		return ErrNoRecycleBin
	}
	root, _ := windows.UTF16PtrFromString(vol)
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return ErrNoRecycleBin
	}
	from, err := windows.UTF16FromString(finalPath)
	if err != nil {
		return err
	}
	from = append(from, 0) // the list is double-NUL terminated
	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofWantNukeWarning,
	}
	rc, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	switch {
	case rc == errorCancelledShell || op.fAnyOperationsAborted != 0:
		return fmt.Errorf("moving to the Recycle Bin was cancelled")
	case rc == 5 || rc == 0x78: // ERROR_ACCESS_DENIED, DE_ACCESSDENIEDSRC
		return ErrAccessDenied
	case rc == 0x20: // ERROR_SHARING_VIOLATION
		return ErrInUse
	case rc != 0:
		return fmt.Errorf("moving to the Recycle Bin failed (code 0x%X)", rc)
	}
	return nil
}
