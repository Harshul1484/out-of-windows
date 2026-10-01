package analyzer_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/analyzer"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func tree(t *testing.T) *testutil.Fixture {
	f := testutil.NewFixture(t)
	f.File("root/a.bin", 1000, time.Hour)
	f.File("root/docs/b.txt", 300, time.Hour)
	f.File("root/docs/deep/c.dat", 5000, time.Hour)
	f.File("root/media/big.mp4", 90000, time.Hour)
	f.File("root/media/small.jpg", 200, time.Hour)
	f.File("elsewhere/huge.iso", 500000, time.Hour)
	f.Junction("root/link", f.Path("elsewhere"))
	return f
}

func TestScanTotals(t *testing.T) {
	f := tree(t)
	res, err := analyzer.Scan(context.Background(), f.Path("root"), analyzer.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Root
	if r.Size != 96500 || r.Files != 5 {
		t.Fatalf("root = %d bytes, %d files; want 96500, 5", r.Size, r.Files)
	}
	if res.Links != 1 {
		t.Errorf("links = %d, want 1 (junction not followed)", res.Links)
	}
	docs := r.Find(f.Path("root/docs"))
	if docs == nil || docs.Size != 5300 || docs.Files != 2 || docs.Own != 300 {
		t.Fatalf("docs = %+v", docs)
	}
	kids := r.SortedChildren()
	if kids[0].Name != "media" || kids[1].Name != "docs" {
		t.Errorf("order = %s, %s", kids[0].Name, kids[1].Name)
	}
	if r.Dirs() != 4 { // docs, docs/deep, media, link
		t.Errorf("dirs = %d", r.Dirs())
	}
}

func TestLargestFilesAndMinSize(t *testing.T) {
	f := tree(t)
	res, err := analyzer.Scan(context.Background(), f.Path("root"), analyzer.Options{TopFiles: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Largest) != 2 || filepath.Base(res.Largest[0].Path) != "big.mp4" || filepath.Base(res.Largest[1].Path) != "c.dat" {
		t.Fatalf("largest = %+v", res.Largest)
	}
	res, _ = analyzer.Scan(context.Background(), f.Path("root"), analyzer.Options{MinFileSize: 1000}, nil)
	if len(res.Largest) != 3 {
		t.Errorf("files >= 1000 B: %+v", res.Largest)
	}
}

func TestInaccessibleFolderCounted(t *testing.T) {
	f := tree(t)
	dir := f.Path("root/docs/deep")
	if out, err := exec.Command("icacls", dir, "/deny", "*S-1-1-0:(RD)").CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("icacls", dir, "/remove:d", "*S-1-1-0").Run() })
	res, err := analyzer.Scan(context.Background(), f.Path("root"), analyzer.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Errors != 1 || res.Root.Size != 91500 {
		t.Errorf("errors = %d, size = %d", res.Errors, res.Root.Size)
	}
	if n := res.Root.Find(dir); n == nil || n.Err == nil {
		t.Error("unreadable folder not marked")
	}
}

func TestCancellation(t *testing.T) {
	f := testutil.NewFixture(t)
	for i := 0; i < 30; i++ {
		f.File(filepath.Join("root", "d"+string(rune('a'+i%26)), "f"+string(rune('a'+i/26))), 10, 0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := analyzer.Scan(ctx, f.Path("root"), analyzer.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cancelled || res.Root.Files != 0 {
		t.Errorf("cancelled=%v files=%d", res.Cancelled, res.Root.Files)
	}
}

func TestRootMustBeRealFolder(t *testing.T) {
	f := tree(t)
	if _, err := analyzer.Scan(context.Background(), f.Path("root/link"), analyzer.Options{}, nil); err == nil {
		t.Error("junction root accepted")
	}
	if _, err := analyzer.Scan(context.Background(), f.Path("root/a.bin"), analyzer.Options{}, nil); err == nil {
		t.Error("file root accepted")
	}
}

func TestListAndRemove(t *testing.T) {
	f := tree(t)
	res, _ := analyzer.Scan(context.Background(), f.Path("root"), analyzer.Options{}, nil)
	entries := analyzer.List(res.Root)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = e.IsDir
	}
	if len(entries) != 4 || !names["docs"] || names["a.bin"] {
		t.Fatalf("entries = %+v", entries)
	}
	res.Root.Remove(f.Path("root/media"), true, 0, 0)
	if res.Root.Size != 6300 || res.Root.Files != 3 {
		t.Errorf("after removing media: %d bytes, %d files", res.Root.Size, res.Root.Files)
	}
	res.Root.Remove(f.Path("root/docs/b.txt"), false, 300, 1)
	docs := res.Root.Find(f.Path("root/docs"))
	if docs.Size != 5000 || res.Root.Size != 6000 {
		t.Errorf("after removing file: docs %d, root %d", docs.Size, res.Root.Size)
	}
}
