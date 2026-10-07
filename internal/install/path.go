package install

import (
	"errors"
	"strings"

	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// UserPath reads and writes the user's PATH variable, unexpanded.
type UserPath interface {
	// Get returns the raw value and whether it is stored as an expandable
	// string (REG_EXPAND_SZ). A missing value is "" without error.
	Get() (value string, expandable bool, err error)
	// Set stores value with the given kind and tells running programs that
	// the environment changed.
	Set(value string, expandable bool) error
	// Expand expands %VARIABLES% in one entry, for comparison only.
	Expand(entry string) string
}

// ErrPathChanged means the user PATH changed between reading and writing.
var ErrPathChanged = errors.New("the user PATH changed while it was being edited; run the command again")

// SameDir reports whether a PATH entry names dir, ignoring case, a trailing
// backslash and %VARIABLES% (expanded with expand).
func SameDir(entry, dir string, expand func(string) string) bool {
	e := strings.TrimSpace(strings.Trim(strings.TrimSpace(entry), `"`))
	if e == "" || dir == "" {
		return false
	}
	if expand != nil && strings.Contains(e, "%") {
		e = expand(e)
	}
	a, err1 := safety.Normalize(e)
	b, err2 := safety.Normalize(dir)
	return err1 == nil && err2 == nil && safety.Key(a) == safety.Key(b)
}

// HasEntry reports whether value contains an entry for dir.
func HasEntry(value, dir string, expand func(string) string) bool {
	for _, e := range strings.Split(value, ";") {
		if SameDir(e, dir, expand) {
			return true
		}
	}
	return false
}

// RemoveEntry returns value without the entries that name dir, and how many
// were removed. Every other entry is kept exactly as written, in order.
func RemoveEntry(value, dir string, expand func(string) string) (string, int) {
	parts := strings.Split(value, ";")
	kept := parts[:0:0]
	removed := 0
	for _, e := range parts {
		if SameDir(e, dir, expand) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed == 0 {
		return value, 0
	}
	return strings.Join(kept, ";"), removed
}

// RemoveFromUserPath removes dir from the user PATH. It re-reads the value
// right before writing and refuses to write if it changed in between.
func RemoveFromUserPath(up UserPath, dir string) (int, error) {
	value, expandable, err := up.Get()
	if err != nil {
		return 0, err
	}
	next, n := RemoveEntry(value, dir, up.Expand)
	if n == 0 {
		return 0, nil
	}
	again, againExpandable, err := up.Get()
	if err != nil {
		return 0, err
	}
	if again != value || againExpandable != expandable {
		return 0, ErrPathChanged
	}
	return n, up.Set(next, expandable)
}
