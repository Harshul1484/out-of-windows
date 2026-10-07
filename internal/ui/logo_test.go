package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(lines []string) string {
	var out []string
	for _, l := range lines {
		out = append(out, strings.TrimRight(ansi.ReplaceAllString(l, ""), " "))
	}
	return strings.Join(out, "\n")
}

// The terminal logo must be the same drawing as the brand masters: the
// rendering committed with them in assets/brand is generated from the same
// grids, so any drift between the two fails here.
func TestLogoMatchesBrandAssets(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "brand", "oow-terminal.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(string(data), "\r\n", "\n")
	for name, lines := range map[string][]string{"mark": LogoMark(), "lockup": LogoLockup()} {
		if got := plain(lines); !strings.Contains(want, got+"\n") {
			t.Errorf("%s rendering is not in assets/brand/oow-terminal.txt:\n%s", name, got)
		}
	}
}

func TestLogoShape(t *testing.T) {
	for name, c := range map[string]struct {
		lines []string
		width int
	}{"mark": {LogoMark(), 8}, "lockup": {LogoLockup(), 27}} {
		if len(c.lines) != 4 {
			t.Fatalf("%s: %d lines, want 4", name, len(c.lines))
		}
		for i, l := range c.lines {
			if w := lipgloss.Width(l); w != c.width {
				t.Errorf("%s line %d: width %d, want %d", name, i, w, c.width)
			}
		}
	}
}
