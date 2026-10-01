package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Recycler simulates moving items to the Recycle Bin by renaming them into
// the sandbox's simulated bin, so the real Recycle Bin is never touched.
type Recycler struct{ Root string }

// Recycle moves finalPath into <root>\C\$Recycle.Bin\S-1-5-21-sandbox.
func (r Recycler) Recycle(finalPath string) error {
	root, err := safety.Normalize(r.Root)
	if err != nil {
		return err
	}
	src, err := safety.Normalize(finalPath)
	if err != nil {
		return err
	}
	if !safety.IsStrictlyWithin(src, root) {
		return fmt.Errorf("%s is outside the sandbox", src)
	}
	bin := filepath.Join(RecycleBinDir(r.Root), "S-1-5-21-sandbox")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return os.Rename(src, filepath.Join(bin, "$R"+hex.EncodeToString(b)+filepath.Ext(src)))
}
