package ui

import "strings"

// The oow mark ("Exit Pane": a window frame whose corner block has already
// left it) and the pixel wordmark, on the same grids the brand masters in
// assets/brand are traced from, so the terminal shows the logo itself rather
// than an approximation. A cell is one character wide and half a line tall:
// two rows of cells share one line of half-block characters.
var (
	logoFrame = []string{
		"........",
		"........",
		"####....",
		"#.......",
		"#....#..",
		"#....#..",
		"#....#..",
		"######..",
	}
	logoBlock = []string{
		"......##",
		"......##",
		"........",
		"........",
		"........",
		"........",
		"........",
		"........",
	}
	logoWord = []string{ // "oow", 5 rows, set on the frame's baseline
		".###...###..#...#",
		"#...#.#...#.#...#",
		"#...#.#...#.#.#.#",
		"#...#.#...#.#.#.#",
		".###...###...#.#.",
	}
)

const (
	logoWordGap = 2 // cells between the mark and the wordmark
	logoWordTop = 3 // first row of the wordmark: its baseline is the frame's
)

type logoCell int

const (
	cellEmpty logoCell = iota
	cellInk
	cellAccent
)

// LogoLockup returns the mark with the "oow" wordmark beside it: 4 lines of
// equal display width (27 columns). The block outside the frame uses the
// accent colour, everything else the bold text style; without colour it is
// plain characters and reads the same.
func LogoLockup() []string { return renderLogo(true) }

// LogoMark returns the mark alone: 4 lines, 8 columns.
func LogoMark() []string { return renderLogo(false) }

func renderLogo(word bool) []string {
	width := len(logoFrame[0])
	if word {
		width += logoWordGap + len(logoWord[0])
	}
	cell := func(x, y int) logoCell {
		switch {
		case x < len(logoFrame[0]):
			if logoBlock[y][x] == '#' {
				return cellAccent
			}
			if logoFrame[y][x] == '#' {
				return cellInk
			}
		case word && x >= len(logoFrame[0])+logoWordGap:
			wy := y - logoWordTop
			if wy >= 0 && wy < len(logoWord) && logoWord[wy][x-len(logoFrame[0])-logoWordGap] == '#' {
				return cellInk
			}
		}
		return cellEmpty
	}
	var lines []string
	for y := 0; y < len(logoFrame); y += 2 {
		var b strings.Builder
		var run strings.Builder
		runKind := cellEmpty
		flush := func() {
			switch runKind {
			case cellInk:
				b.WriteString(Bold.Render(run.String()))
			case cellAccent:
				b.WriteString(Accent.Render(run.String()))
			default:
				b.WriteString(run.String())
			}
			run.Reset()
		}
		for x := 0; x < width; x++ {
			top, bottom := cell(x, y), cell(x, y+1)
			kind, ch := top, " "
			switch {
			case top != cellEmpty && bottom != cellEmpty:
				ch = "█"
			case top != cellEmpty:
				ch = "▀"
			case bottom != cellEmpty:
				kind, ch = bottom, "▄"
			}
			if kind != runKind {
				flush()
				runKind = kind
			}
			run.WriteString(ch)
		}
		flush()
		lines = append(lines, b.String())
	}
	return lines
}
