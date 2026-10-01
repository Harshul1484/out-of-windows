package ui

import "github.com/muesli/termenv"

// enableVT turns on ANSI escape processing in classic Windows consoles
// (conhost). Windows Terminal already supports it.
func enableVT(out *termenv.Output) {
	_, _ = termenv.EnableVirtualTerminalProcessing(out)
}
