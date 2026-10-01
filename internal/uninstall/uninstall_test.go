package uninstall

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
)

func TestNewPlan(t *testing.T) {
	code := "{6F1D5C3A-2B4E-4C8D-9A7F-1E2D3C4B5A69}"
	cases := []struct {
		name  string
		app   apps.App
		quiet bool
		want  string
		meth  Method
		elev  bool
	}{
		{"msi", apps.App{Name: "A", Source: apps.SourceMSI, ProductCode: code}, false, "msiexec.exe /x " + code, MethodMSI, false},
		{"msi quiet", apps.App{Name: "A", Source: apps.SourceMSI, ProductCode: code}, true, "msiexec.exe /x " + code + " /qn /norestart", MethodMSI, false},
		{"exe", apps.App{Name: "B", Source: apps.SourceEXE, UninstallString: `"C:\Tools\B\uninstall.exe" /S`}, false,
			`C:\Tools\B\uninstall.exe /S`, MethodEXE, false},
		{"exe quiet string", apps.App{Name: "B", Source: apps.SourceEXE, UninstallString: `"C:\B\u.exe"`, QuietUninstallString: `"C:\B\u.exe" /quiet`}, true,
			`C:\B\u.exe /quiet`, MethodEXE, false},
		{"msiexec /I becomes /x", apps.App{Name: "C", Source: apps.SourceEXE, UninstallString: "MsiExec.exe /I" + code}, false,
			"msiexec.exe /x " + code, MethodMSI, false},
		{"scoop", apps.App{Name: "git", Source: apps.SourceScoop, Scope: apps.ScopeUser, PackageName: "git"}, false,
			"cmd.exe /c scoop uninstall git", MethodScoop, false},
		{"choco", apps.App{Name: "Git", Source: apps.SourceChoco, PackageName: "git"}, false,
			"choco.exe uninstall git -y", MethodChoco, true},
		{"appx", apps.App{Name: "Calc", Source: apps.SourceAppX, PackageName: "Contoso.Calc_1.0.0.0_x64__abc"}, false,
			"Remove-AppxPackage -Package Contoso.Calc_1.0.0.0_x64__abc", MethodAppX, false},
	}
	for _, c := range cases {
		p, err := NewPlan(c.app, c.quiet)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if p.Command != c.want || p.Method != c.meth || p.Elevate != c.elev {
			t.Errorf("%s: plan = %q %s elevate=%v", c.name, p.Command, p.Method, p.Elevate)
		}
	}

	p, _ := NewPlan(apps.App{Name: "D", Source: apps.SourceEXE, UninstallString: `"C:\D\u.exe"`}, true)
	if p.Quiet || len(p.Notes) == 0 {
		t.Errorf("quiet request without quiet string: %+v", p)
	}
	for _, a := range []apps.App{
		{Name: "E", Source: apps.SourceEXE},
		{Name: "F", Source: apps.SourceEXE, UninstallString: "x", NoRemove: true},
		{Name: "G", Source: apps.SourceEXE, UninstallString: "x", Problems: []string{apps.ProblemUninstallerMissing}},
		{Name: "H", Source: apps.SourceEXE, UninstallString: "MsiExec.exe /I"},
	} {
		if _, err := NewPlan(a, false); err == nil {
			t.Errorf("plan for %s accepted", a.Name)
		}
	}
}

type fakeRunner struct {
	code int
	err  error
	ran  atomic.Int32
}

func (f *fakeRunner) Run(ctx context.Context, p Plan) (int, error) {
	f.ran.Add(1)
	return f.code, f.err
}

// fakeChecker reports the app installed for the first `after` checks.
type fakeChecker struct {
	after  int32
	checks atomic.Int32
}

func (f *fakeChecker) Installed(ctx context.Context, a apps.App) (bool, error) {
	return f.checks.Add(1) <= f.after, nil
}

func plan() Plan {
	return Plan{App: apps.App{Name: "X"}, Method: MethodEXE, Exe: "x.exe"}
}

func fast() Options { return Options{WaitTimeout: 300 * time.Millisecond, Poll: 10 * time.Millisecond} }

func TestExecuteRemovedImmediately(t *testing.T) {
	out, err := Execute(context.Background(), plan(), &fakeRunner{}, &fakeChecker{}, fast())
	if err != nil || !out.Removed || !out.Started {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestExecuteWaitsForHandOff(t *testing.T) {
	// The started process exits at once; the app disappears a bit later.
	c := &fakeChecker{after: 5}
	out, err := Execute(context.Background(), plan(), &fakeRunner{}, c, fast())
	if err != nil || !out.Removed || c.checks.Load() != 6 {
		t.Fatalf("out = %+v, err = %v, checks = %d", out, err, c.checks.Load())
	}
}

func TestExecuteStillInstalled(t *testing.T) {
	out, err := Execute(context.Background(), plan(), &fakeRunner{}, &fakeChecker{after: 1 << 30}, fast())
	if !errors.Is(err, ErrNotRemoved) || out.Removed {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	out, err = Execute(context.Background(), plan(), &fakeRunner{code: 1603}, &fakeChecker{after: 1 << 30}, fast())
	if !errors.Is(err, ErrNotRemoved) || !strings.Contains(out.Message, "1603") {
		t.Fatalf("failure: out = %+v, err = %v", out, err)
	}
}

func TestExecuteCancelledByUser(t *testing.T) {
	c := &fakeChecker{after: 1 << 30}
	out, err := Execute(context.Background(), plan(), &fakeRunner{code: msiUserCancelled}, c, fast())
	if !errors.Is(err, ErrNotRemoved) || !strings.Contains(out.Message, "cancelled") || c.checks.Load() != 0 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestExecuteRestartRequired(t *testing.T) {
	out, err := Execute(context.Background(), plan(), &fakeRunner{code: msiRebootRequired}, &fakeChecker{}, fast())
	if err != nil || !out.Restart || !strings.Contains(out.Message, "restart") {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestExecuteStopWaiting(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	opts := Options{WaitTimeout: time.Hour, Poll: time.Hour, StopWaiting: stop}
	start := time.Now()
	_, err := Execute(context.Background(), plan(), &fakeRunner{}, &fakeChecker{after: 1 << 30}, opts)
	if !errors.Is(err, ErrNotRemoved) || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestExecuteRunnerError(t *testing.T) {
	out, err := Execute(context.Background(), plan(), &fakeRunner{err: errors.New("declined")}, &fakeChecker{}, fast())
	if err == nil || out.Started || out.Removed {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
