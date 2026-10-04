package analyzer_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/analyzer"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// TestScanThroughput measures scanner overhead against the operating
// system's own enumeration on a synthetic tree inside the test sandbox.
// It is opt-in (OOW_BENCH=1) because it creates ~100,000 files.
func TestScanThroughput(t *testing.T) {
	if os.Getenv("OOW_BENCH") != "1" {
		t.Skip("set OOW_BENCH=1 to run the scanner throughput benchmark")
	}
	root := filepath.Join(testutil.Dir(t), "tree")
	const dirs, files = 2000, 50
	start := time.Now()
	for d := 0; d < dirs; d++ {
		dir := filepath.Join(root, fmt.Sprintf("a%02d", d%40), fmt.Sprintf("b%04d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < files; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.dat", f)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("created %d files in %v", dirs*files, time.Since(start))

	start = time.Now()
	if out, err := exec.Command("cmd", "/c", "dir /s /a "+root+" > nul").CombinedOutput(); err != nil {
		t.Fatalf("dir: %v %s", err, out)
	}
	t.Logf("dir /s: %v", time.Since(start))

	for _, workers := range []int{1, 4, 8, 16, 32, 64} {
		start = time.Now()
		res, err := analyzer.Scan(context.Background(), root, analyzer.Options{Workers: workers}, nil)
		if err != nil {
			t.Fatal(err)
		}
		el := time.Since(start)
		t.Logf("scan workers=%-2d %v  (%d files, %.0f files/s)", workers, el, res.Root.Files, float64(res.Root.Files)/el.Seconds())
	}
}
