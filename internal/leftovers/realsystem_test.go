package leftovers_test

import (
	"os"
	"path/filepath"
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
