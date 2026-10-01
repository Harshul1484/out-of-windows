package analyzer

import (
	"io/fs"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
)

// filesystemEntry converts a directory listing entry without extra system
// calls.
func filesystemEntry(path string, info fs.FileInfo) filesystem.Entry {
	return filesystem.EntryFromInfo(path, info)
}
