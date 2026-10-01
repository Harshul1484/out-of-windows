package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// recyclePaths is reached only from the interactive explorer, so it is
// tested directly: user files may be recycled, system, sensitive and
// protected locations never.
func TestRecyclePathsGuard(t *testing.T) {
	root := testutil.Dir(t)
	if err := sandbox.Seed(root); err != nil {
		t.Fatal(err)
	}
	app := &App{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, SandboxDir: root, NoColor: true}
	if err := app.setup(); err != nil {
		t.Fatal(err)
	}
	defer app.close()
	j := func(rel string) string { return filepath.Join(root, rel) }
	paths := []string{
		j(`C\Users\sandbox\Documents\thesis.docx`),                // user file: allowed
		j(`C\Users\sandbox\AppData\Local\Temp\7zS1A2B.tmp`),       // ordinary folder: allowed
		j(`C\Windows\System32\kernel32.dll`),                      // system: refused
		j(`C\Users\sandbox\.ssh\id_ed25519`),                      // sensitive: refused
		j(`C\Users\sandbox\Documents`),                            // user folder root: refused
		j(`C\Users\sandbox\AppData\Local\Temp\link-to-documents`), // junction: refused
	}
	errs := app.recyclePaths(paths)
	want := []bool{true, true, false, false, false, false}
	for i, p := range paths {
		if (errs[i] == nil) != want[i] {
			t.Errorf("%s: err = %v, want allowed=%v", p, errs[i], want[i])
		}
	}
	for i, p := range paths {
		_, err := os.Lstat(p)
		if want[i] && err == nil {
			t.Errorf("%s still exists", p)
		}
		if !want[i] && err != nil {
			t.Errorf("%s was removed", p)
		}
	}
	bin, _ := os.ReadDir(filepath.Join(sandbox.RecycleBinDir(root), "S-1-5-21-sandbox"))
	if len(bin) != 3+2 {
		t.Errorf("Recycle Bin entries = %d, want 5", len(bin))
	}
	// Documents is intact (the junction was not followed).
	if _, err := os.Stat(j(`C\Users\sandbox\Documents\old-notes.txt`)); err != nil {
		t.Error("Documents changed")
	}
}
