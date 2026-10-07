package leftovers_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/leftovers"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// Read-only: usage traces and system claims on the real system.
func TestRealTracesAndClaims(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	traces := leftovers.ReadTraces()
	t.Logf("%d usage traces", len(traces))
	for _, tr := range traces {
		if tr.Exe == "" {
			t.Errorf("empty trace %+v", tr)
		}
	}
	claims := leftovers.SystemClaims()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self, _ := safety.Normalize(filepath.Dir(exe))
	found := false
	for _, c := range claims {
		if n, err := safety.Normalize(c.Path); err == nil && safety.Key(n) == safety.Key(self) {
			found = true
		}
	}
	if !found {
		t.Errorf("the running test binary's folder %s is not claimed (%d claims)", self, len(claims))
	}
}

// Read-only: shortcuts on the real system. Nothing is moved.
func TestRealBrokenShortcuts(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	g := safety.NewGuard(safety.DiscoverLocations(), nil)
	if roots := g.ShortcutRoots(); len(roots) != 2 {
		t.Errorf("shortcut roots = %q (want the Start Menu Programs folder and the Desktop)", roots)
	}
	folders := leftovers.SystemShortcutFolders()
	if len(folders) < 3 {
		t.Errorf("shortcut folders = %+v", folders)
	}
	shortcuts := leftovers.FindBrokenShortcuts(context.Background(), g, folders, leftovers.SystemLinks())
	for _, sc := range shortcuts {
		if !strings.EqualFold(filepath.Ext(sc.Target), ".exe") || !strings.EqualFold(filepath.Ext(sc.Path), ".lnk") ||
			sc.Removable == (sc.Scope == "") {
			t.Errorf("shortcut %+v", sc)
		}
		t.Logf("broken: %s -> %s (removable %v %s)", sc.Path, sc.Target, sc.Removable, sc.Note)
	}
	for _, ev := range leftovers.EvidenceFromShortcuts(shortcuts, g) {
		t.Logf("evidence: %s in %s from %v", ev.Name, ev.InstallLocation, ev.Shortcuts)
	}
}
