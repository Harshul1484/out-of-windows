package optimize_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Harshul1484/out-of-windows/internal/optimize"
)

// The fixtures in testdata/dism are DISM output byte for byte (CRLF line ends,
// the progress bar redrawn with CR): Microsoft's documented sample report, a
// real refusal captured from a non-elevated terminal, and reports in the same
// layout for the other cases (recommended, locale number format, localized,
// cut off, pending operations).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "dism", name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\r\n") {
		t.Fatalf("%s lost its CRLF line ends (check .gitattributes)", name)
	}
	return b
}

const (
	kb = int64(1) << 10
	mb = int64(1) << 20
	gb = int64(1) << 30
)

func approx(n float64, unit int64) int64 { return int64(n*float64(unit) + 0.5) }

func TestParseComponentStoreReport(t *testing.T) {
	for _, c := range []struct {
		file string
		want optimize.ComponentStore
	}{
		{"documented-not-recommended.txt", optimize.ComponentStore{
			ExplorerBytes: approx(4.98, gb), ActualBytes: approx(4.88, gb), SharedBytes: approx(4.38, gb),
			BackupsBytes: approx(506.90, mb), CacheBytes: approx(279.52, kb), ReclaimablePackages: 0,
			Recommended: false, LastCleanup: "2021-06-24 23:32:22",
		}},
		{"recommended.txt", optimize.ComponentStore{
			ExplorerBytes: approx(11.22, gb), ActualBytes: approx(10.89, gb), SharedBytes: approx(6.15, gb),
			BackupsBytes: approx(4.74, gb), CacheBytes: 0, ReclaimablePackages: 3,
			Recommended: true, LastCleanup: "2024-03-18 09:58:02",
		}},
		{"recommended-locale-numbers.txt", optimize.ComponentStore{
			ExplorerBytes: approx(9.03, gb), ActualBytes: approx(8.77, gb), SharedBytes: approx(7.79, gb),
			BackupsBytes: approx(1010.50, mb), CacheBytes: approx(12.04, mb), ReclaimablePackages: 1,
			Recommended: true, LastCleanup: "18.03.2024 09:58:02",
		}},
	} {
		got, err := optimize.ParseComponentStoreReport(fixture(t, c.file))
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.file, got, c.want)
		}
	}
}

// Anything DISM did not clearly report is an error: never zero bytes, never
// "recommended".
func TestParseComponentStoreReportFailsClosed(t *testing.T) {
	for _, c := range []struct {
		file, want string
	}{
		{"localized-german.txt", `no "Actual Size of Component Store" line`},
		{"truncated.txt", `no "Cache and Temporary Data" line`},
		{"error-740-not-elevated.txt", "DISM error 740: Elevated permissions are required to run DISM"},
		{"error-pending.txt", "DISM error 0x800f0806: The operation could not be completed due to pending operations (Windows has servicing operations pending: restart Windows"},
	} {
		got, err := optimize.ParseComponentStoreReport(fixture(t, c.file))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.file, err, c.want)
		}
		if got != (optimize.ComponentStore{}) {
			t.Errorf("%s: returned values with an error: %+v", c.file, got)
		}
	}
	if _, err := optimize.ParseComponentStoreReport(fixture(t, "localized-german.txt")); !errors.Is(err, optimize.ErrUnreadableReport) {
		t.Errorf("a localized report must be ErrUnreadableReport, got %v", err)
	}

	base := string(fixture(t, "recommended.txt"))
	for name, edit := range map[string][2]string{
		"empty":                  {base, ""},
		"recommendation unknown": {"Recommended : Yes", "Recommended : Unknown"},
		"recommendation missing": {"Component Store Cleanup Recommended : Yes", ""},
		"localized yes":          {"Recommended : Yes", "Recommended : Ja"},
		"size without unit":      {"Actual Size of Component Store : 10.89 GB", "Actual Size of Component Store : 10.89"},
		"unknown unit":           {"Actual Size of Component Store : 10.89 GB", "Actual Size of Component Store : 10.89 Go"},
		"negative size":          {"Backups and Disabled Features : 4.74 GB", "Backups and Disabled Features : -4.74 GB"},
		"ambiguous number":       {"Backups and Disabled Features : 4.74 GB", "Backups and Disabled Features : 4.7.4 GB"},
		"fractional bytes":       {"Cache and Temporary Data :  0 bytes", "Cache and Temporary Data : 0.5 bytes"},
		"package count":          {"Number of Reclaimable Packages : 3", "Number of Reclaimable Packages : three"},
		"negative count":         {"Number of Reclaimable Packages : 3", "Number of Reclaimable Packages : -3"},
		"conflicting duplicate":  {"Component Store Cleanup Recommended : Yes", "Component Store Cleanup Recommended : Yes\r\nComponent Store Cleanup Recommended : No"},
		"overhead exceeds store": {"Backups and Disabled Features : 4.74 GB", "Backups and Disabled Features : 47.4 GB"},
		"error line":             {"The operation completed successfully.", "Error: 1726\r\n\r\nThe remote procedure call failed."},
	} {
		in := strings.Replace(base, edit[0], edit[1], 1)
		if in == base {
			t.Fatalf("%s: the edit did not apply", name)
		}
		got, err := optimize.ParseComponentStoreReport([]byte(in))
		if err == nil {
			t.Errorf("%s: parsed as %+v", name, got)
		}
	}
	// Optional display values that cannot be read stay unknown (-1).
	in := strings.Replace(base, "Shared with Windows : 6.15 GB", "Shared with Windows : n/a", 1)
	if got, err := optimize.ParseComponentStoreReport([]byte(in)); err != nil || got.SharedBytes != -1 || !got.Recommended {
		t.Errorf("optional value: %+v, %v", got, err)
	}
}

func TestParseComponentStoreReportUTF16(t *testing.T) {
	text := string(fixture(t, "recommended.txt"))
	u := utf16.Encode([]rune(text))
	for _, bom := range []bool{true, false} {
		var b []byte
		if bom {
			b = append(b, 0xFF, 0xFE)
		}
		for _, r := range u {
			b = append(b, byte(r), byte(r>>8))
		}
		got, err := optimize.ParseComponentStoreReport(b)
		if err != nil || !got.Recommended || got.ReclaimablePackages != 3 {
			t.Errorf("UTF-16 (bom %v): %+v, %v", bom, got, err)
		}
	}
}

func TestComponentStoreOverhead(t *testing.T) {
	c, err := optimize.ParseComponentStoreReport(fixture(t, "documented-not-recommended.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Microsoft's example: "the actual overhead ... is 507.18 MB".
	if got := float64(c.OverheadBytes()) / float64(mb); got < 507.17 || got > 507.19 {
		t.Errorf("overhead = %.2f MB, want 507.18 MB", got)
	}
}
