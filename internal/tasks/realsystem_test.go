package tasks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/tasks"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// Read-only: lists the real task library (CI, or OOW_TEST_REAL_SYSTEM=1).
// Nothing is enabled, disabled or otherwise changed.
func TestRealTaskLibrary(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	acct := tasks.CurrentAccount()
	if acct.SID == "" || acct.Name == "" {
		t.Errorf("current account = %+v", acct)
	}
	list, warnings, err := tasks.System{}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d tasks for %s\\%s, warnings: %v", len(list), acct.Domain, acct.Name, warnings)
	windowsTasks, logon := 0, 0
	for _, task := range list {
		if !strings.HasPrefix(task.Path, `\`) || task.Name() == "" || task.Triggers == nil || task.Actions == nil {
			t.Errorf("incomplete task %+v", task)
		}
		if task.Windows() {
			windowsTasks++
		}
		if task.Has(tasks.TriggerLogon) && !task.Windows() {
			logon++
			t.Logf("logon task %s enabled=%v owned=%v actions=%+v", task.Path, task.Enabled, task.OwnedBy(acct), task.Actions)
		}
	}
	// Every Windows installation has tasks under \Microsoft\Windows that any
	// user can read.
	if windowsTasks == 0 {
		t.Error(`no \Microsoft\ tasks were read`)
	}
	t.Logf("%d Windows tasks, %d other logon tasks", windowsTasks, logon)
}
