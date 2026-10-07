package startup_test

import (
	"context"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// Read-only checks against the real system (CI, or OOW_TEST_REAL_SYSTEM=1).

func TestRealStartupEntries(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	entries, warnings, err := startup.System{}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d entries, warnings: %v", len(entries), warnings)
	firstBytes := map[string]int{}
	for _, e := range entries {
		if e.ID == "" || e.Name == "" || e.SourceLabel == "" || e.State == "" || e.TargetState == "" {
			t.Errorf("incomplete entry %+v", e)
		}
		if e.Approval != nil && e.Approval.Data != "" {
			b, _ := hex.DecodeString(e.Approval.Data)
			if _, _, known := startup.ParseApproval(b); !known {
				t.Logf("unrecognized StartupApproved value for %s: %s", e.ID, e.Approval.Data)
			}
			firstBytes[e.Approval.Data[:2]]++
		}
		t.Logf("%-10s %-9s %-8s %s -> %s", e.Source, e.State, e.TargetState, e.Name, e.Target)
	}
	t.Logf("StartupApproved first bytes seen: %v", firstBytes)
}

// Every shortcut in the Start Menu must parse without panicking; most should
// yield a target.
func TestRealShortcutsParse(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	var roots []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Programs, windows.FOLDERID_CommonPrograms} {
		if p, err := windows.KnownFolderPath(id, 0); err == nil {
			roots = append(roots, p)
		}
	}
	expand := func(s string) string { x, _ := registry.ExpandString(s); return x }
	var total, resolved int
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".lnk") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			total++
			l, err := startup.ParseLink(data, nil)
			if err != nil {
				t.Logf("%s: %v", p, err)
				return nil
			}
			if l.TargetPath(filepath.Dir(p), expand) != "" {
				resolved++
			}
			return nil
		})
	}
	t.Logf("%d shortcuts, %d with a file target", total, resolved)
}
