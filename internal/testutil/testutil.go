// Package testutil confines tests to a sandbox inside the repository.
//
// Every test package that creates or deletes files must call Main from its
// TestMain. Main creates <repo>/.sandbox/<run>, sets the process-wide deletion
// fence to it, and removes it afterwards. Fixture helpers refuse to work if
// the fence is not in place, so a test can never delete outside the sandbox
// even if the code under test is wrong.
package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
)

var runDir string

// RepoRoot returns the directory containing go.mod.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// Main sets up the sandbox and deletion fence, runs the tests, and cleans up.
// Use it as: func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }
func Main(m *testing.M) int {
	root, err := RepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil:", err)
		return 1
	}
	runDir = filepath.Join(root, ".sandbox", fmt.Sprintf("run-%d-%s", os.Getpid(), randomSuffix()))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "testutil:", err)
		return 1
	}
	if err := filesystem.SetFence(runDir); err != nil {
		fmt.Fprintln(os.Stderr, "testutil:", err)
		return 1
	}
	code := m.Run()
	if os.Getenv("OOW_KEEP_SANDBOX") == "" {
		_ = os.RemoveAll(runDir)
	}
	return code
}

// RealSystem reports whether tests may read (never modify) the real system,
// e.g. query Known Folder paths. Enabled with OOW_TEST_REAL_SYSTEM=1; CI sets
// it on disposable Windows runners.
func RealSystem() bool { return os.Getenv("OOW_TEST_REAL_SYSTEM") == "1" }

// SkipUnlessRealSystem skips a test that reads the real system.
func SkipUnlessRealSystem(t testing.TB) {
	t.Helper()
	if !RealSystem() {
		t.Skip("reads the real system; set OOW_TEST_REAL_SYSTEM=1 to run (CI does)")
	}
}

// Dir returns a fresh directory inside the sandbox for one test.
func Dir(t testing.TB) string {
	t.Helper()
	if runDir == "" || filesystem.Fence() == "" {
		t.Fatal("testutil.Main was not called from TestMain: refusing to run without a deletion fence")
	}
	name := regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(t.Name(), "_")
	if len(name) > 40 {
		name = name[:40]
	}
	d := filepath.Join(runDir, name+"-"+randomSuffix())
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// Fixture builds file trees inside a test's sandbox directory.
type Fixture struct {
	T    testing.TB
	Root string
}

// NewFixture returns a fixture rooted in a fresh sandbox directory.
func NewFixture(t testing.TB) *Fixture {
	t.Helper()
	return &Fixture{T: t, Root: Dir(t)}
}

// Path joins rel onto the fixture root.
func (f *Fixture) Path(rel string) string {
	return filepath.Join(f.Root, filepath.FromSlash(rel))
}

// File creates a file of size bytes whose creation and modification times are
// age in the past.
func (f *Fixture) File(rel string, size int, age time.Duration) string {
	f.T.Helper()
	p := f.Path(rel)
	if err := sandbox.WriteFile(p, size, time.Now().Add(-age)); err != nil {
		f.T.Fatal(err)
	}
	return p
}

// Dir creates a directory and sets its timestamps age in the past.
func (f *Fixture) Dir(rel string, age time.Duration) string {
	f.T.Helper()
	p := f.Path(rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.T.Fatal(err)
	}
	f.Age(rel, age)
	return p
}

// Age sets an existing item's creation and modification times.
func (f *Fixture) Age(rel string, age time.Duration) {
	f.T.Helper()
	t := time.Now().Add(-age)
	if err := filesystem.SetTimes(f.Path(rel), t, t); err != nil {
		f.T.Fatal(err)
	}
}

// Junction creates a directory junction at rel pointing to target (absolute).
func (f *Fixture) Junction(rel, target string) string {
	f.T.Helper()
	p := f.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.T.Fatal(err)
	}
	if err := sandbox.MakeJunction(p, target); err != nil {
		f.T.Fatal(err)
	}
	return p
}

// Exists reports whether rel exists (without following links).
func (f *Fixture) Exists(rel string) bool {
	_, err := os.Lstat(f.Path(rel))
	return err == nil
}

// Snapshot records every item below the root (not following junctions) with
// its type, size and modification time, for before/after comparisons.
func (f *Fixture) Snapshot() map[string]string {
	f.T.Helper()
	return SnapshotDir(f.T, f.Root)
}

// SnapshotDir is Snapshot for an arbitrary directory.
func SnapshotDir(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		e, err := filesystem.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case e.Reparse:
			out[rel] = "link"
			if d.IsDir() {
				return filepath.SkipDir
			}
		case d.IsDir():
			out[rel] = "dir"
		default:
			out[rel] = fmt.Sprintf("file:%d:%d", info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Locations returns a simulated Windows layout inside the fixture.
func (f *Fixture) Locations() safety.Locations {
	return sandbox.Locations(f.Root)
}

// AssertInSandbox fails the test if p is not inside the sandbox run dir.
func AssertInSandbox(t testing.TB, p string) {
	t.Helper()
	n, err := safety.Normalize(p)
	if err != nil {
		t.Fatalf("invalid path %q: %v", p, err)
	}
	rd, _ := safety.Normalize(runDir)
	if !safety.IsStrictlyWithin(n, rd) && !strings.EqualFold(n, rd) {
		t.Fatalf("path %s escapes the test sandbox %s", n, rd)
	}
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
