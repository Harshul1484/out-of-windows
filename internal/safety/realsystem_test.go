package safety_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

// These tests read (never modify) the real system. They run on CI's
// disposable Windows VMs, or locally with OOW_TEST_REAL_SYSTEM=1.

func TestRealDiscovery(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	l := safety.DiscoverLocations()
	for name, p := range map[string]string{
		"Windows": l.Windows, "ProgramFiles": l.ProgramFiles, "ProgramData": l.ProgramData,
		"UserProfile": l.UserProfile, "LocalAppData": l.LocalAppData, "RoamingAppData": l.RoamingAppData,
		"Temp": l.Temp, "UsersRoot": l.UsersRoot,
	} {
		if p == "" {
			t.Errorf("%s not discovered", name)
			continue
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s = %s does not exist: %v", name, p, err)
		}
		if strings.Contains(p, "~") {
			t.Errorf("%s = %s contains an 8.3 short name", name, p)
		}
	}
	if len(l.UserContent) < 4 || len(l.FixedDrives) == 0 {
		t.Errorf("user content %v, drives %v", l.UserContent, l.FixedDrives)
	}
}

func TestRealSystemPathsProtected(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	l := safety.DiscoverLocations()
	g := safety.NewGuard(l, nil)
	sys32 := filepath.Join(l.Windows, "System32")
	targets := []string{
		l.Windows, sys32, filepath.Join(sys32, "kernel32.dll"), l.ProgramFiles, l.ProgramFilesX86,
		l.ProgramData, l.UsersRoot, l.UserProfile, l.LocalAppData, l.RoamingAppData, l.SystemDrive,
	}
	targets = append(targets, l.UserContent...)
	for _, p := range targets {
		if p == "" {
			continue
		}
		for _, scope := range []string{"", l.WindowsTemp, filepath.Join(l.LocalAppData, "Temp")} {
			if d := g.Check(safety.Request{Path: p, Purpose: safety.PurposeCleanup, Scope: scope}); d.Allowed {
				t.Errorf("cleanup allowed for real path %s (scope %q)", p, scope)
			}
		}
		if _, err := g.ValidateRoot(p); err == nil {
			t.Errorf("real path %s accepted as cleanup root", p)
		}
	}
}

// 8.3 short names (C:\PROGRA~1) must resolve to protected long names.
func TestRealShortNamesResolve(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	l := safety.DiscoverLocations()
	g := safety.NewGuard(l, nil)
	for _, long := range []string{l.ProgramFiles, l.ProgramFilesX86, l.ProgramData} {
		short := shortPath(long)
		if short == "" || strings.EqualFold(short, long) {
			continue // 8.3 names disabled on this volume
		}
		resolved := safety.LongPath(short)
		if !strings.EqualFold(resolved, long) {
			t.Errorf("LongPath(%s) = %s, want %s", short, resolved, long)
		}
		if d := g.Check(safety.Request{Path: resolved, Purpose: safety.PurposeUserSelected}); d.Allowed {
			t.Errorf("short name %s of %s allowed", short, long)
		}
	}
}

func shortPath(p string) string {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 512)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
