package filesystem

import (
	"fmt"
	"sync/atomic"

	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// fence, when set, confines every deletion to one directory tree. It is
// checked against the OS-resolved final path of each object at the moment of
// deletion, below all policy code, so a bug elsewhere cannot escape it.
//
// Tests and sandbox mode always set it. Once set it can only be narrowed to a
// path inside the current fence, never widened or removed.
var fence atomic.Pointer[string]

// SetFence confines all deletions in this process to root. root must exist.
func SetFence(root string) error {
	final, err := FinalPath(root)
	if err != nil {
		return fmt.Errorf("set deletion fence: %w", err)
	}
	if safety.IsVolumeRoot(final) {
		return fmt.Errorf("set deletion fence: %s is a drive root", final)
	}
	for {
		cur := fence.Load()
		if cur != nil && !safety.IsWithin(final, *cur) {
			return fmt.Errorf("set deletion fence: %s is outside the existing fence %s", final, *cur)
		}
		if fence.CompareAndSwap(cur, &final) {
			return nil
		}
	}
}

// Fence returns the active deletion fence, or "" when none is set.
func Fence() string {
	if p := fence.Load(); p != nil {
		return *p
	}
	return ""
}

func checkFence(final string) error {
	f := Fence()
	if f == "" {
		return nil
	}
	if !safety.IsStrictlyWithin(final, f) {
		return fmt.Errorf("%s %w %s", final, ErrOutsideFence, f)
	}
	return nil
}
