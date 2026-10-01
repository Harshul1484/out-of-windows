package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// VisitFunc is called for every entry below the walk root. For directories,
// returning true descends into them. Reparse points are never descended into,
// whatever the return value.
type VisitFunc func(e Entry) (descend bool)

// ErrorFunc receives directories that could not be read. The walk continues.
type ErrorFunc func(path string, err error)

// Walk enumerates the tree below root depth-first without following links or
// junctions. The root itself is not visited. It stops early only when ctx is
// cancelled, returning ctx.Err().
func Walk(ctx context.Context, root string, visit VisitFunc, onErr ErrorFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return mapError(err)
	}
	rootEntry := entryFromInfo(root, info)
	if rootEntry.Reparse {
		return ErrReparsePoint
	}
	if !rootEntry.IsDir() {
		return &fs.PathError{Op: "walk", Path: root, Err: errors.New("not a directory")}
	}

	stack := []string{root}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := os.ReadDir(dir)
		if err != nil && len(entries) == 0 {
			if onErr != nil {
				onErr(dir, mapError(err))
			}
			continue
		}
		for _, de := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			p := filepath.Join(dir, de.Name())
			info, err := de.Info()
			if err != nil {
				// The entry vanished between listing and stat; nothing to do.
				continue
			}
			e := entryFromInfo(p, info)
			descend := visit(e)
			if descend && e.IsDir() && !e.Reparse {
				stack = append(stack, p)
			}
		}
	}
	return nil
}

// Lstat returns the entry for a single path without following links.
func Lstat(path string) (Entry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Entry{}, mapError(err)
	}
	return entryFromInfo(path, info), nil
}

// SizeOf sums the logical size of all files below root (or of root itself if
// it is a file). Links are not followed. Unreadable directories are skipped.
func SizeOf(ctx context.Context, root string) (bytes int64, files int, err error) {
	e, err := Lstat(root)
	if err != nil {
		return 0, 0, err
	}
	if !e.IsDir() || e.Reparse {
		return e.Size(), 1, nil
	}
	err = Walk(ctx, root, func(e Entry) bool {
		if !e.IsDir() && !e.Reparse {
			bytes += e.Size()
			files++
		}
		return true
	}, nil)
	return bytes, files, err
}
