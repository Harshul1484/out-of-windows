package optimize

import (
	"strings"
	"testing"
)

// The DISM command lines are fixed: the analysis only reads, the cleanup
// never resets the base or removes service pack backups, and DISM never
// restarts Windows on its own.
func TestDISMCommandLines(t *testing.T) {
	join := func(a []string) string { return strings.ToLower(strings.Join(a, " ")) }
	if got := join(dismAnalyzeArgs); got != "/online /english /cleanup-image /analyzecomponentstore" {
		t.Errorf("analysis = %q", got)
	}
	if got := join(dismCleanupArgs); got != "/online /english /quiet /norestart /cleanup-image /startcomponentcleanup" {
		t.Errorf("cleanup = %q", got)
	}
	for _, args := range [][]string{dismAnalyzeArgs, dismCleanupArgs} {
		for _, a := range args {
			switch strings.ToLower(a) {
			case "/resetbase", "/spsuperseded", "/defer", "/restorehealth", "/revertpendingactions", "/image", "/source":
				t.Errorf("forbidden DISM option %s", a)
			}
			if !strings.HasPrefix(a, "/") || strings.ContainsAny(a, " \"%&|<>^") {
				t.Errorf("unexpected argument %q", a)
			}
		}
	}
	if dismAnalyzeTimeout <= 0 || dismCleanupTimeout < dismAnalyzeTimeout {
		t.Error("time limits")
	}
}

func TestParseDISMSize(t *testing.T) {
	for in, want := range map[string]int64{
		"0 bytes":     0,
		"512 bytes":   512,
		"1,023 bytes": 1023,
		"1.023 bytes": 1023,
		"279.52 KB":   286228,
		"4.98 GB":     5347234284, // 4.98 × 2^30 = 5347234283.52
		"4,98 GB":     5347234284,
		"506.90 MB":   531523174, // 506.9 × 2^20 = 531523174.4
		"1,010.50 MB": 1059586048,
		"1.010,50 MB": 1059586048,
		"1.5 TB":      1649267441664,
		"10 gb":       10 << 30,
	} {
		if got, err := parseDISMSize(in); err != nil || got != want {
			t.Errorf("parseDISMSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "GB", "4.98", "4.98GB", "4.98 Go", "-1 GB", "4.9.8 GB", "4,98.1 GB", "1,0 MB bytes",
		"1,02 bytes", "1.5 bytes", "12,3456 MB", "1 234,56 MB", "x MB", "4.98 GB extra", "1e3 MB", "0x10 MB", ".5 GB", "5. GB"} {
		if got, err := parseDISMSize(in); err == nil {
			t.Errorf("parseDISMSize(%q) = %d, want an error", in, got)
		}
	}
}

func TestDISMErrors(t *testing.T) {
	if got := dismError(nil, 0x800f0806).Error(); !strings.Contains(got, "0x800f0806") || !strings.Contains(got, "restart Windows") {
		t.Errorf("hex code = %q", got)
	}
	if got := dismError([]byte("garbage"), 87).Error(); got != "DISM error 87 (this Windows' DISM does not support this option)" {
		t.Errorf("decimal code = %q", got)
	}
	if got := dismError([]byte("\r\nError: 5\r\n\r\nAccess is denied.\r\n"), 5).Error(); got != "DISM error 5: Access is denied" {
		t.Errorf("message = %q", got)
	}
}
