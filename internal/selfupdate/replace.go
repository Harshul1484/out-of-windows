package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/Harshul1484/out-of-windows/internal/install"
)

// OldPath is where the previous executable is kept after an update until the
// next start removes it. Windows lets a running program be renamed, not
// deleted, so the old version moves aside instead.
func OldPath(exe string) string { return exe + ".old" }

// StagedPath is where the new executable is downloaded and verified, in the
// same folder as exe so the final move is a rename on one volume.
func StagedPath(exe string) string { return exe + ".new" }

// rename moves a file without ever replacing an existing one. Tests swap it
// to simulate failures.
var rename = func(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_WRITE_THROUGH)
}

// Stage downloads the planned asset to StagedPath(exe), verifies its size
// and SHA-256 while downloading and again from disk, and returns the staged
// path. On any failure the partial file is removed and nothing else changes.
func Stage(ctx context.Context, src Source, p *Plan, exe string) (string, error) {
	staged := StagedPath(exe)
	if err := install.RemoveOwnFile(staged, nil); err != nil {
		return "", fmt.Errorf("a previous update left %s, which cannot be removed: %w", staged, err)
	}
	f, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return "", fmt.Errorf("cannot write next to %s: %w", exe, err)
	}
	derr := src.Download(ctx, p, f)
	cerr := errors.Join(f.Sync(), f.Close())
	if err := errors.Join(derr, cerr); err != nil {
		return "", discard(staged, err)
	}
	if err := VerifyFile(staged, p.SHA256); err != nil {
		return "", discard(staged, err)
	}
	return staged, nil
}

func discard(staged string, cause error) error {
	if rerr := install.RemoveOwnFile(staged, nil); rerr != nil {
		return fmt.Errorf("%w (the rejected download %s could not be removed: %v)", cause, staged, rerr)
	}
	return cause
}

// VerifyFile checks the SHA-256 of the file at path.
func VerifyFile(path, sha string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fmt.Errorf("%w for %s: expected %s, got %s", ErrChecksumMismatch, filepath.Base(path), sha, got)
	}
	return nil
}

// Replace swaps the verified staged file in for exe:
//
//  1. a stale OldPath(exe) from an earlier update is removed (verified sink);
//  2. exe is renamed to OldPath(exe) (allowed while it runs);
//  3. staged is renamed to exe;
//  4. if step 3 fails, step 2 is undone, so exe is never left missing.
//
// Renames never overwrite an existing file.
func Replace(exe, staged string) error {
	old := OldPath(exe)
	if err := install.RemoveOwnFile(old, nil); err != nil {
		return fmt.Errorf("a previous update left %s, which cannot be removed yet (%v); close other %s windows and try again",
			old, err, filepath.Base(exe))
	}
	if err := rename(exe, old); err != nil {
		return fmt.Errorf("cannot move the current executable aside (%v); nothing was changed", err)
	}
	if err := rename(staged, exe); err != nil {
		if rerr := rename(old, exe); rerr != nil {
			return fmt.Errorf("installing the new version failed (%v) and restoring the previous one failed (%v): rename %s to %s to recover",
				err, rerr, old, exe)
		}
		return fmt.Errorf("installing the new version failed; the previous version was restored: %v", err)
	}
	return nil
}

// CleanupOld removes the executable an earlier update moved aside. It is
// called at start-up; a file that is still in use (another instance of the
// old version is running) stays until a later start.
func CleanupOld(exe string) error {
	if exe == "" {
		return nil
	}
	return install.RemoveOwnFile(OldPath(exe), nil)
}
