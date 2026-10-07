// Package ui renders terminal output: styles, formatting helpers, prompts,
// progress, and the interactive Bubble Tea screens.
//
// Color is never required to understand output: every status has a symbol
// and a word. Color is disabled by NO_COLOR, --no-color, ui.color=never, or
// when output is not a terminal.
package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Symbols used for statuses. Each is always paired with text.
const (
	SymOK      = "✓"
	SymWarn    = "⚠"
	SymSkip    = "○"
	SymItem    = "●"
	SymErr     = "✗"
	SymPointer = "›"
	SymDot     = "·"
)

// Palette entries adapt to light and dark terminals.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5B3CC4", Dark: "#A78BFA"}
	colOK     = lipgloss.AdaptiveColor{Light: "#147A3D", Dark: "#4ADE80"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9A5B00", Dark: "#FBBF24"}
	colErr    = lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#F87171"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#667085", Dark: "#8B93A7"}
	colText   = lipgloss.AdaptiveColor{Light: "#101828", Dark: "#E6E8EE"}
	colBorder = lipgloss.AdaptiveColor{Light: "#D0D5DD", Dark: "#3B4252"}
)

// Styles is the shared style set.
var (
	Title  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	Accent = lipgloss.NewStyle().Foreground(colAccent)
	Bold   = lipgloss.NewStyle().Bold(true).Foreground(colText)
	Muted  = lipgloss.NewStyle().Foreground(colMuted)
	OK     = lipgloss.NewStyle().Foreground(colOK)
	Warn   = lipgloss.NewStyle().Foreground(colWarn)
	Err    = lipgloss.NewStyle().Foreground(colErr)
	Key    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	Box    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 2)
	Rule   = lipgloss.NewStyle().Foreground(colBorder)
)

// RenderLines styles each line of s on its own. Rendering a multi-line
// string in one call pads every line to the widest one, which leaves
// trailing spaces after wrapped text.
func RenderLines(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}

var colorEnabled = true

// ColorEnabled reports whether ANSI colors are in use.
func ColorEnabled() bool { return colorEnabled }

// Init configures color support. mode is "auto", "always" or "never".
func Init(mode string, noColorFlag bool) {
	out := termenv.NewOutput(os.Stdout)
	enableVT(out)
	_, noColorEnv := os.LookupEnv("NO_COLOR")
	switch {
	case noColorFlag || noColorEnv || mode == "never":
		colorEnabled = false
	case mode == "always":
		colorEnabled = true
		lipgloss.SetColorProfile(termenv.TrueColor)
	default:
		colorEnabled = out.Profile != termenv.Ascii
	}
	if !colorEnabled {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}
