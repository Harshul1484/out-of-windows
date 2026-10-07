package cli

import (
	"strings"
	"testing"
)

func plainRender(s ...string) string { return strings.Join(s, " ") }

// Advice sentences wrap by words even when they contain a command with a
// path; only summaries and details that name a path stay on one line.
func TestDoctorWrapKeepsAdviceWhole(t *testing.T) {
	next := "Run `oow clean` to remove temporary files and caches, or `oow analyze D:\\` to see what uses the space."
	got := wrapRender(plainRender, next, 50, "  ", false)
	if strings.Contains(got, "…") {
		t.Errorf("advice was shortened:\n%s", got)
	}
	if joined := strings.Join(strings.Fields(got), " "); joined != next {
		t.Errorf("advice lost words:\n got %q\nwant %q", joined, next)
	}
	for _, line := range strings.Split(got, "\n") {
		if n := len([]rune(strings.TrimLeft(line, " "))); n > 50 {
			t.Errorf("line of %d columns: %q", n, line)
		}
	}

	detail := `missing: C:\Program Files\Some Vendor With A Long Name\Some Product\bin`
	if got := wrapRender(plainRender, detail, 40, "  ", true); strings.Contains(got, "\n") || !strings.Contains(got, "…") {
		t.Errorf("a detail naming a path should stay one line, shortened in the middle: %q", got)
	}
}
