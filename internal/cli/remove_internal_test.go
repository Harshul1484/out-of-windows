package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// On a real system the executable is the running program, which Windows
// does not let delete itself: remove must leave it in place, finish
// everything else, and hand the user the exact command for the last step.
// Simulated here with the sandbox layout and Running set, so nothing real is
// touched.
func TestRemoveRunningExecutableNeedsAManualStep(t *testing.T) {
	root := testutil.Dir(t)
	if err := sandbox.Seed(root); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.SeedInstall(root); err != nil {
		t.Fatal(err)
	}
	app := &App{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, SandboxDir: root, NoColor: true}
	if err := app.setup(); err != nil {
		t.Fatal(err)
	}
	defer app.close()
	self := selfInfo{Exe: sandbox.SelfExe(root), Raw: sandbox.SelfExe(root), Running: true}

	items := planRemove(app, self, removeOptions{})
	executeRemove(app, self, items)
	status := map[string]string{}
	for _, it := range items {
		status[it.Kind] = it.Status
	}
	if status[kindExe] != stManual || status[kindInstallDir] != stManual || status[kindPathEntry] != stDone || status[kindDataDir] != stDone {
		t.Fatalf("statuses = %v", status)
	}
	if _, err := os.Stat(self.Exe); err != nil {
		t.Fatal("the running executable was touched")
	}
	steps := manualSteps(self.Exe, app.installDir())
	if len(steps) != 2 || !strings.Contains(steps[0].Command, "Remove-Item -LiteralPath '"+self.Exe+"'") ||
		!strings.Contains(steps[1].Command, `del "`+self.Exe+`" && rmdir "`+app.installDir()+`"`) {
		t.Errorf("steps = %+v", steps)
	}
	// Quotes in paths cannot break out of the PowerShell string.
	if s := manualSteps(`C:\it's\oow.exe`, ""); s[0].Command != `Remove-Item -LiteralPath 'C:\it''s\oow.exe'` {
		t.Errorf("quoting: %s", s[0].Command)
	}
}
