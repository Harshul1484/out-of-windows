package apps_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
)

// Read-only discovery on the real system; runs on CI's disposable VMs or
// with OOW_TEST_REAL_SYSTEM=1.
func TestRealInventory(t *testing.T) {
	if os.Getenv("OOW_TEST_REAL_SYSTEM") != "1" {
		t.Skip("reads the real system; set OOW_TEST_REAL_SYSTEM=1 to run (CI does)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inv, err := apps.System{}.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Apps) < 3 {
		t.Fatalf("only %d apps found; warnings: %v", len(inv.Apps), inv.Warnings)
	}
	ids := map[string]bool{}
	sources := map[apps.Source]int{}
	for _, a := range inv.Apps {
		if a.ID == "" || strings.TrimSpace(a.Name) == "" {
			t.Errorf("incomplete app %+v", a)
		}
		if ids[a.ID] {
			t.Errorf("duplicate ID %s", a.ID)
		}
		ids[a.ID] = true
		sources[a.Source]++
		if a.Source == apps.SourceMSI && a.ProductCode == "" {
			t.Errorf("MSI app without product code: %+v", a)
		}
	}
	t.Logf("%d apps by source: %v; package managers: %v; warnings: %v", len(inv.Apps), sources, inv.PackageManagers, inv.Warnings)
}
