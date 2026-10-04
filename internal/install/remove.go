package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// ErrRunning is returned for the executable of the running program: Windows
// does not let a running program delete its own file. The caller shows the
// user the one command that removes it after exit.
var ErrRunning = errors.New("it is the running program, and Windows does not let a running program delete its own file")

// ExeRemover removes the installed executable, then the install folder if
// nothing else is left in it.
type ExeRemover interface {
	RemoveExe(exe, dir string) (dirRemoved bool, err error)
}

// RunningExe is the ExeRemover for the program that is running. It removes
// nothing and returns ErrRunning.
type RunningExe struct{}

// RemoveExe implements ExeRemover.
func (RunningExe) RemoveExe(exe, dir string) (bool, error) { return false, ErrRunning }

// VerifiedExe removes an executable that is not running (the simulated one
// in sandbox mode) through the verified deletion sink. Check, when set, must
// also approve each OS-resolved final path.
type VerifiedExe struct {
	Check filesystem.CheckFunc
}

// RemoveExe implements ExeRemover.
func (v VerifiedExe) RemoveExe(exe, dir string) (bool, error) {
	if !InDir(exe, dir) {
		return false, fmt.Errorf("%s is not in the install folder %s", exe, dir)
	}
	if err := RemoveOwnFile(exe, v.Check); err != nil {
		return false, err
	}
	return RemoveEmptyDir(dir, v.Check)
}

// RemoveOwnFile deletes one file oow put there itself (the installed
// executable, or the .old/.new files of an update) through the verified
// deletion sink. It refuses directories and links, and the OS-resolved final
// path must be exactly the expected one. A missing file is not an error.
func RemoveOwnFile(path string, check filesystem.CheckFunc) error {
	e, err := filesystem.Lstat(path)
	if errors.Is(err, filesystem.ErrGone) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.IsDir() || e.Reparse {
		return fmt.Errorf("%s is not a plain file; leaving it alone", path)
	}
	return filesystem.RemoveVerified(path, e.Fingerprint, exactly(path, check))
}

// RemoveEmptyDir removes dir if it is an empty, real directory (not a link).
// A folder that still holds anything is kept and reported as not removed.
func RemoveEmptyDir(dir string, check filesystem.CheckFunc) (bool, error) {
	e, err := filesystem.Lstat(dir)
	if errors.Is(err, filesystem.ErrGone) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !e.IsDir() || e.Reparse {
		return false, fmt.Errorf("%s is not a plain folder; leaving it alone", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	if len(entries) > 0 {
		return false, nil
	}
	if err := filesystem.RemoveVerified(dir, e.Fingerprint, exactly(dir, check)); err != nil {
		if errors.Is(err, filesystem.ErrNotEmpty) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// exactly wraps check so the final path must also be exactly path (its
// parent resolved by the OS, so 8.3 names and casing do not matter).
func exactly(path string, check filesystem.CheckFunc) filesystem.CheckFunc {
	return func(final string) error {
		parent, err := filesystem.FinalPath(filepath.Dir(path))
		if err != nil {
			return err
		}
		if safety.Key(final) != safety.Key(parent+`\`+filepath.Base(path)) {
			return fmt.Errorf("%s resolves to %s", path, final)
		}
		if check != nil {
			return check(final)
		}
		return nil
	}
}
