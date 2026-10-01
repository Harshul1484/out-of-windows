package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Bytes formats a size the way Windows Explorer does (binary units labelled
// KB/MB/GB), so numbers match what users see elsewhere on the system.
func Bytes(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	const unit = 1024
	var s string
	if n < unit {
		s = fmt.Sprintf("%d B", n)
	} else {
		div, exp := int64(unit), 0
		for m := n / unit; m >= unit && exp < 5; m /= unit {
			div *= unit
			exp++
		}
		v := float64(n) / float64(div)
		prec := 1
		if v >= 100 {
			prec = 0
		}
		s = strconv.FormatFloat(v, 'f', prec, 64) + " " + string("KMGTPE"[exp]) + "B"
	}
	if neg {
		return "-" + s
	}
	return s
}

// Count formats an integer with thousands separators.
func Count(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + Count(-n)
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// Plural returns "1 file" / "3 files".
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return Count(n) + " " + plural
}

// Duration formats a short elapsed time.
func Duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// TruncateMiddle shortens s to width runes, keeping both ends of a path.
func TruncateMiddle(s string, width int) string {
	if width <= 0 || utf8.RuneCountInString(s) <= width {
		return s
	}
	if width <= 3 {
		return string([]rune(s)[:width])
	}
	r := []rune(s)
	keep := width - 1
	head := keep / 3
	tail := keep - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// Truncate shortens s to width runes, cutting at the end.
func Truncate(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// PadRight pads a (possibly styled) string to a visible width.
func PadRight(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// PadLeft right-aligns a (possibly styled) string to a visible width.
func PadLeft(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return strings.Repeat(" ", width-w) + s
	}
	return s
}

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// Width returns the terminal width, or 80 when unknown.
func Width() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	return 80
}

// Bar renders a percentage bar of the given width.
func Bar(percent float64, width int) string {
	if width < 1 {
		return ""
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int(percent/100*float64(width) + 0.5)
	style := OK
	switch {
	case percent >= 90:
		style = Err
	case percent >= 75:
		style = Warn
	}
	return style.Render(strings.Repeat("█", filled)) + Muted.Render(strings.Repeat("░", width-filled))
}

// Divider returns a horizontal rule of the given width.
func Divider(width int) string {
	return Rule.Render(strings.Repeat("─", width))
}
