package install

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

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

// RemoveFromUserPath removes dir from the user PATH through store, the same
// compare-and-swap writer `oow repair` uses: the value is written only if it
// still equals what was read, and is read back afterwards.
func RemoveFromUserPath(store envpath.Store, dir string) (int, error) {
	cur, err := store.Read(envpath.User)
	if err != nil {
		return 0, err
	}
	if !cur.Exists {
		return 0, nil
	}
	expand := func(s string) string { x, _ := store.Expand(s); return x }
	next, n := RemoveEntry(cur.Raw, dir, expand)
	if n == 0 {
		return 0, nil
	}
	err = store.WriteUser(cur, envpath.Value{Raw: next, Expand: cur.Expand, Exists: true})
	if errors.Is(err, envpath.ErrChanged) {
		return 0, ErrPathChanged
	}
	if err != nil {
		return 0, err
	}
	// The PATH is already changed; a window that does not answer the
	// broadcast only delays when new terminals see it.
	if err := store.Broadcast(); err != nil {
		slog.Warn("environment change not broadcast", "err", err)
	}
	return n, nil
}
