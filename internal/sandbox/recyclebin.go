package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// RecycleBin simulates the Windows Recycle Bin with a folder, so the
// Recycle Bin cleanup target can be exercised without touching the real one.
// Like the real bin it keeps the per-user SID folders and removes their
// contents.
type RecycleBin struct{ Dir string }

func (b RecycleBin) items(ctx context.Context) (files, dirs []filesystem.Entry, err error) {
	err = filesystem.Walk(ctx, b.Dir, func(e filesystem.Entry) bool {
		if e.Reparse {
			return false
		}
		rel := strings.TrimPrefix(e.Path[len(b.Dir):], `\`)
		if e.IsDir() {
			if strings.Contains(rel, `\`) { // below a SID folder
				dirs = append(dirs, e)
			}
			return true
		}
		files = append(files, e)
		return false
	}, nil)
	if errors.Is(err, filesystem.ErrGone) {
		err = nil
	}
	return files, dirs, err
}

// Measure counts the simulated bin's files and their size.
func (b RecycleBin) Measure(ctx context.Context) (int, int64, error) {
	files, _, err := b.items(ctx)
	var n int64
	for _, f := range files {
		n += f.Size()
	}
	return len(files), n, err
}

// Clean removes everything below the SID folders through the verified sink.
func (b RecycleBin) Clean(ctx context.Context) (int, int64, error) {
	files, dirs, err := b.items(ctx)
	if err != nil {
		return 0, 0, err
	}
	root, err := safety.Normalize(b.Dir)
	if err != nil {
		return 0, 0, err
	}
	inside := func(final string) error {
		if !safety.IsStrictlyWithin(final, root) {
			return fmt.Errorf("%s is outside the simulated Recycle Bin", final)
		}
		return nil
	}
	var removed int
	var bytes int64
	for _, f := range files {
		if ctx.Err() != nil {
			return removed, bytes, ctx.Err()
		}
		if err := filesystem.RemoveVerified(f.Path, f.Fingerprint, inside); err == nil {
			removed++
			bytes += f.Size()
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].Path) > len(dirs[j].Path) })
	for _, d := range dirs {
		_ = filesystem.RemoveVerified(d.Path, d.Fingerprint, inside)
	}
	return removed, bytes, nil
}
