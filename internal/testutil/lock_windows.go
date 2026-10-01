package testutil

import (
	"testing"

	"golang.org/x/sys/windows"
)

// LockFile opens path the way an application holding a file open would:
// without FILE_SHARE_DELETE, so no one else can delete it. Call the returned
// function to release it.
func LockFile(t testing.TB, path string) (unlock func()) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	released := false
	unlock = func() {
		if !released {
			released = true
			windows.CloseHandle(h)
		}
	}
	t.Cleanup(unlock)
	return unlock
}
