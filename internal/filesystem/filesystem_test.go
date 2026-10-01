package filesystem_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func lstat(t *testing.T, p string) filesystem.Entry {
	t.Helper()
	e, err := filesystem.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func allow(string) error { return nil }

func TestWalkDoesNotFollowJunctions(t *testing.T) {
	f := testutil.NewFixture(t)
	f.File("root/a.txt", 10, 0)
	f.File("root/sub/b.txt", 20, 0)
	f.File("precious/secret.txt", 30, 0)
	f.Junction("root/link", f.Path("precious"))

	var files, reparse []string
	err := filesystem.Walk(context.Background(), f.Path("root"), func(e filesystem.Entry) bool {
		rel, _ := filepath.Rel(f.Path("root"), e.Path)
		switch {
		case e.Reparse:
			reparse = append(reparse, rel)
		case !e.IsDir():
			files = append(files, rel)
		}
		return true // ask to descend everywhere; links must still not be followed
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if want := []string{"a.txt", `sub\b.txt`}; !equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	if want := []string{"link"}; !equal(reparse, want) {
		t.Errorf("reparse = %v, want %v", reparse, want)
	}
}

func TestWalkRootJunctionRefused(t *testing.T) {
	f := testutil.NewFixture(t)
	f.File("target/x.txt", 1, 0)
	f.Junction("link", f.Path("target"))
	err := filesystem.Walk(context.Background(), f.Path("link"), func(filesystem.Entry) bool { return true }, nil)
	if !errors.Is(err, filesystem.ErrReparsePoint) {
		t.Fatalf("Walk(junction root) = %v, want ErrReparsePoint", err)
	}
}

func TestWalkCancellation(t *testing.T) {
	f := testutil.NewFixture(t)
	for i := 0; i < 50; i++ {
		f.File(filepath.Join("d", string(rune('a'+i%26))+string(rune('a'+i/26))+".txt"), 1, 0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err := filesystem.Walk(ctx, f.Path("d"), func(filesystem.Entry) bool {
		n++
		if n == 5 {
			cancel()
		}
		return true
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n > 6 {
		t.Errorf("walk continued for %d entries after cancellation", n)
	}
}

func TestWalkUnreadableDirectoryContinues(t *testing.T) {
	f := testutil.NewFixture(t)
	f.File("root/ok.txt", 1, 0)
	f.File("root/locked/hidden.txt", 1, 0)
	denyRead(t, f.Path("root/locked"))

	var seen []string
	var errs []string
	err := filesystem.Walk(context.Background(), f.Path("root"), func(e filesystem.Entry) bool {
		seen = append(seen, e.Name)
		return true
	}, func(p string, err error) {
		errs = append(errs, filepath.Base(p))
		if !errors.Is(err, filesystem.ErrAccessDenied) {
			t.Errorf("error = %v, want ErrAccessDenied", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(seen, "ok.txt") || contains(seen, "hidden.txt") {
		t.Errorf("seen = %v", seen)
	}
	if !equal(errs, []string{"locked"}) {
		t.Errorf("errors = %v, want [locked]", errs)
	}
}

func TestRemoveVerifiedDeletesMatchingFile(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("a.bin", 1234, time.Hour)
	if err := filesystem.RemoveVerified(p, lstat(t, p).Fingerprint, allow); err != nil {
		t.Fatal(err)
	}
	if f.Exists("a.bin") {
		t.Fatal("file still exists")
	}
}

func TestRemoveVerifiedReadOnlyFile(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("ro.txt", 10, time.Hour)
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.RemoveVerified(p, lstat(t, p).Fingerprint, allow); err != nil {
		t.Fatalf("read-only file: %v", err)
	}
	if f.Exists("ro.txt") {
		t.Fatal("read-only file still exists")
	}
}

func TestRemoveVerifiedRefusesModifiedFile(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("a.txt", 10, time.Hour)
	fp := lstat(t, p).Fingerprint
	if err := os.WriteFile(p, []byte("now different content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.RemoveVerified(p, fp, allow); !errors.Is(err, filesystem.ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
	if !f.Exists("a.txt") {
		t.Fatal("modified file was deleted")
	}
}

func TestRemoveVerifiedRefusesTypeChange(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("x", 0, time.Hour)
	fp := lstat(t, p).Fingerprint
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.RemoveVerified(p, fp, allow); !errors.Is(err, filesystem.ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
}

func TestRemoveVerifiedRefusesJunction(t *testing.T) {
	f := testutil.NewFixture(t)
	f.File("precious/keep.txt", 5, 0)
	link := f.Junction("link", f.Path("precious"))
	if err := filesystem.RemoveVerified(link, lstat(t, link).Fingerprint, allow); !errors.Is(err, filesystem.ErrReparsePoint) {
		t.Fatalf("err = %v, want ErrReparsePoint", err)
	}
	if !f.Exists("precious/keep.txt") || !f.Exists("link") {
		t.Fatal("junction or its target was modified")
	}
}

// The classic time-of-check/time-of-use attack: after the scan, a directory
// inside the cleanup root is replaced by a junction to precious data holding
// a file with identical name, size and timestamps. Deletion must re-resolve
// the final path and refuse.
func TestRemoveVerifiedJunctionSwapAttack(t *testing.T) {
	f := testutil.NewFixture(t)
	root := f.Path("cleanroot")
	victim := f.File("cleanroot/sub/a.txt", 100, 48*time.Hour)
	fp := lstat(t, victim).Fingerprint

	// Precious data with an identical fingerprint.
	precious := f.File("precious/a.txt", 100, 0)
	if err := filesystem.SetTimes(precious, fp.CreationTime(), fp.ModTime()); err != nil {
		t.Fatal(err)
	}
	if lstat(t, precious).Fingerprint != fp {
		t.Fatal("test setup: fingerprints differ")
	}

	// Swap: move the scanned directory away and put a junction in its place.
	if err := os.Rename(f.Path("cleanroot/sub"), f.Path("moved")); err != nil {
		t.Fatal(err)
	}
	f.Junction("cleanroot/sub", f.Path("precious"))

	scopeCheck := func(final string) error {
		r, _ := safety.Normalize(root)
		if !safety.IsStrictlyWithin(final, r) {
			return errors.New("outside root")
		}
		return nil
	}
	err := filesystem.RemoveVerified(victim, fp, scopeCheck)
	if !errors.Is(err, filesystem.ErrPolicy) {
		t.Fatalf("err = %v, want ErrPolicy", err)
	}
	if !f.Exists("precious/a.txt") {
		t.Fatal("precious file deleted through a swapped junction")
	}
}

func TestRemoveVerifiedLockedFile(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("locked.dat", 10, time.Hour)
	fp := lstat(t, p).Fingerprint
	unlock := testutil.LockFile(t, p)
	if err := filesystem.RemoveVerified(p, fp, allow); !errors.Is(err, filesystem.ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
	unlock()
	if err := filesystem.RemoveVerified(p, fp, allow); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
}

func TestRemoveVerifiedDirectories(t *testing.T) {
	f := testutil.NewFixture(t)
	full := f.Dir("full", time.Hour)
	f.File("full/child.txt", 1, time.Hour)
	if err := filesystem.RemoveVerified(full, lstat(t, full).Fingerprint, allow); !errors.Is(err, filesystem.ErrNotEmpty) {
		t.Fatalf("non-empty dir: err = %v, want ErrNotEmpty", err)
	}
	empty := f.Dir("empty", time.Hour)
	if err := filesystem.RemoveVerified(empty, lstat(t, empty).Fingerprint, allow); err != nil {
		t.Fatalf("empty dir: %v", err)
	}
}

func TestRemoveVerifiedPolicyAndGone(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("a.txt", 1, time.Hour)
	fp := lstat(t, p).Fingerprint
	deny := func(string) error { return errors.New("no") }
	if err := filesystem.RemoveVerified(p, fp, deny); !errors.Is(err, filesystem.ErrPolicy) {
		t.Fatalf("err = %v, want ErrPolicy", err)
	}
	if !f.Exists("a.txt") {
		t.Fatal("deleted despite policy refusal")
	}
	if err := filesystem.RemoveVerified(f.Path("missing.txt"), fp, allow); !errors.Is(err, filesystem.ErrGone) {
		t.Fatalf("missing: err = %v, want ErrGone", err)
	}
}

func TestRemoveVerifiedPermissionDenied(t *testing.T) {
	f := testutil.NewFixture(t)
	p := f.File("guarded/a.txt", 1, time.Hour)
	fp := lstat(t, p).Fingerprint
	denyDelete(t, p)
	if err := filesystem.RemoveVerified(p, fp, allow); !errors.Is(err, filesystem.ErrAccessDenied) {
		t.Fatalf("err = %v, want ErrAccessDenied", err)
	}
}

func TestFenceCannotBeWidened(t *testing.T) {
	fence := filesystem.Fence()
	if fence == "" {
		t.Fatal("no fence set by TestMain")
	}
	if err := filesystem.SetFence(filepath.Dir(fence)); err == nil {
		t.Fatal("fence widened to its parent")
	}
	if err := filesystem.SetFence(`C:\`); err == nil {
		t.Fatal("fence set to a drive root")
	}
	if filesystem.Fence() != fence {
		t.Fatal("fence changed")
	}
}

func TestFinalPathResolvesJunction(t *testing.T) {
	f := testutil.NewFixture(t)
	f.Dir("target", 0)
	f.Junction("link", f.Path("target"))
	got, err := filesystem.FinalPath(f.Path("link"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filesystem.FinalPath(f.Path("target"))
	if safety.Key(got) != safety.Key(want) {
		t.Errorf("FinalPath(link) = %s, want %s", got, want)
	}
}

func TestSizeOf(t *testing.T) {
	f := testutil.NewFixture(t)
	f.File("d/a", 100, 0)
	f.File("d/e/b", 50, 0)
	f.File("outside/big", 1000, 0)
	f.Junction("d/link", f.Path("outside"))
	n, files, err := filesystem.SizeOf(context.Background(), f.Path("d"))
	if err != nil || n != 150 || files != 2 {
		t.Errorf("SizeOf = %d, %d, %v; want 150, 2", n, files, err)
	}
}

// denyRead removes the current user's permission to list a directory, and
// restores it when the test ends so the sandbox can be cleaned up.
func denyRead(t *testing.T, dir string) {
	t.Helper()
	icacls(t, dir, "/deny", "*S-1-1-0:(RD)")
	t.Cleanup(func() { icacls(t, dir, "/remove:d", "*S-1-1-0") })
}

// denyDelete removes delete permission on a file and on its parent's
// "delete child" right.
func denyDelete(t *testing.T, file string) {
	t.Helper()
	icacls(t, file, "/deny", "*S-1-1-0:(DE)")
	icacls(t, filepath.Dir(file), "/deny", "*S-1-1-0:(DC)")
	t.Cleanup(func() {
		icacls(t, filepath.Dir(file), "/remove:d", "*S-1-1-0")
		icacls(t, file, "/remove:d", "*S-1-1-0")
	})
}

func icacls(t *testing.T, args ...string) {
	t.Helper()
	testutil.AssertInSandbox(t, args[0])
	out, err := exec.Command("icacls", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %v: %v: %s", args, err, out)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
