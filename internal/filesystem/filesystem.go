// Package filesystem performs filesystem enumeration and the only deletions
// in the program.
//
// Deletion is identity-verified: the target is opened by handle without
// following reparse points, the handle's metadata is compared with what the
// scan observed, the OS-resolved final path is re-checked by the caller's
// policy, and only then is the object deleted through that same handle. A path
// that was swapped for a junction, replaced by a different file, or modified
// since the scan is skipped, never deleted.
package filesystem

import (
	"errors"
	"time"
)

// Errors describing why an item was not deleted. They are skip reasons, not
// failures of the program.
var (
	ErrGone         = errors.New("no longer exists")
	ErrInUse        = errors.New("in use by another program")
	ErrAccessDenied = errors.New("permission denied")
	ErrChanged      = errors.New("changed since it was scanned")
	ErrReparsePoint = errors.New("is a link or junction (not followed)")
	ErrCloudFile    = errors.New("is a cloud placeholder (not downloaded locally)")
	ErrNotEmpty     = errors.New("directory is not empty")
	ErrOutsideFence = errors.New("is outside the sandbox fence")
	ErrPolicy       = errors.New("refused by safety policy")
	ErrUnsupported  = errors.New("not supported on this system")
)

// Fingerprint identifies the state of a file or directory as observed during
// a scan. Times are Unix nanoseconds taken directly from Windows FILETIMEs so
// comparisons are exact.
type Fingerprint struct {
	Dir       bool
	Size      int64
	LastWrite int64
	Created   int64
}

// ModTime returns the last-write time.
func (f Fingerprint) ModTime() time.Time { return time.Unix(0, f.LastWrite) }

// CreationTime returns the creation time.
func (f Fingerprint) CreationTime() time.Time { return time.Unix(0, f.Created) }

// Newest returns the later of the creation and last-write times. Extracted
// archives keep old modification times but get a fresh creation time, so
// "how old is this file" must consider both.
func (f Fingerprint) Newest() time.Time {
	if f.Created > f.LastWrite {
		return f.CreationTime()
	}
	return f.ModTime()
}

// Entry is one item found while walking a directory tree.
type Entry struct {
	Path        string
	Name        string
	Attributes  uint32
	Reparse     bool // symlink, junction, mount point, or other reparse point
	Cloud       bool // cloud-files placeholder whose data is not local
	Fingerprint Fingerprint
}

// IsDir reports whether the entry is a directory.
func (e Entry) IsDir() bool { return e.Fingerprint.Dir }

// Size returns the logical file size (0 for directories).
func (e Entry) Size() int64 { return e.Fingerprint.Size }

// Windows file attribute bits used across the package.
const (
	attrReadOnly           = 0x00000001
	attrDirectory          = 0x00000010
	attrReparsePoint       = 0x00000400
	attrOffline            = 0x00001000
	attrRecallOnOpen       = 0x00040000
	attrRecallOnDataAccess = 0x00400000
)

func isCloudAttr(attrs uint32) bool {
	return attrs&(attrOffline|attrRecallOnOpen|attrRecallOnDataAccess) != 0
}
