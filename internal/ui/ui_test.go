package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB",
		10 << 20: "10.0 MB", 150 << 20: "150 MB", 8_420_000_000: "7.8 GB", -2048: "-2.0 KB",
	} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCountAndPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := Count(n); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
	if Plural(1, "file", "files") != "1 file" || Plural(2000, "file", "files") != "2,000 files" {
		t.Error("Plural")
	}
}

func TestTruncateMiddle(t *testing.T) {
	p := `C:\Users\someone\AppData\Local\Temp\very\deep\path\file.txt`
	got := TruncateMiddle(p, 30)
	if len([]rune(got)) != 30 || !strings.HasSuffix(got, "file.txt") || !strings.Contains(got, "…") {
		t.Errorf("TruncateMiddle = %q", got)
	}
	if TruncateMiddle("short", 30) != "short" {
		t.Error("short string changed")
	}
}

func TestDuration(t *testing.T) {
	if Duration(250*time.Millisecond) != "250ms" || Duration(1500*time.Millisecond) != "1.5s" {
		t.Error("Duration")
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m tea.Model, keys ...string) tea.Model {
	for _, k := range keys {
		m, _ = m.Update(key(k))
	}
	return m
}

func checklistItems() []CheckItem {
	return []CheckItem{
		{Header: "Temporary files"},
		{Label: "User temp", Checked: true, Weight: 100},
		{Label: "Windows temp", Disabled: true, Note: "requires administrator"},
		{Header: "Caches"},
		{Label: "Shader cache", Weight: 50},
	}
}

func TestChecklistSkipsHeadersAndDisabled(t *testing.T) {
	m := NewChecklistModel(ChecklistOptions{Title: "t"}, checklistItems())
	// Cursor starts on "User temp"; down skips the disabled row and header.
	m = press(m, "down", "space", "enter")
	r := ChecklistOutcome(m)
	if !r.Confirmed || len(r.Checked) != 2 || r.Checked[0] != 1 || r.Checked[1] != 4 {
		t.Fatalf("result = %+v", r)
	}
}

func TestChecklistToggleAllAndCancel(t *testing.T) {
	m := press(NewChecklistModel(ChecklistOptions{}, checklistItems()), "a", "enter")
	if r := ChecklistOutcome(m); len(r.Checked) != 2 {
		t.Fatalf("select all = %+v", r)
	}
	m = press(NewChecklistModel(ChecklistOptions{}, checklistItems()), "a", "a", "enter")
	if r := ChecklistOutcome(m); len(r.Checked) != 0 {
		t.Fatalf("deselect all = %+v", r)
	}
	m = press(NewChecklistModel(ChecklistOptions{}, checklistItems()), "space", "q")
	if r := ChecklistOutcome(m); r.Confirmed {
		t.Fatal("cancel confirmed")
	}
}

func TestChecklistViewShowsNotesAndTotal(t *testing.T) {
	Init("never", true)
	m := NewChecklistModel(ChecklistOptions{Title: "Select", ShowWeight: true}, checklistItems())
	v := m.View()
	for _, want := range []string{"Select", "Temporary files", "requires administrator", "Selected 100 B", "[✓]"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func homeOpts() HomeOptions {
	return HomeOptions{
		Product: "OOW", OS: "Windows 11",
		Items: []MenuItem{
			{Section: "Clean", Label: "Deep Clean", Command: "clean", Available: true},
			{Label: "Uninstall", Command: "uninstall", Note: "coming soon"},
		},
		Stats: func() HomeStats {
			return HomeStats{CPU: 17, CPUReady: true, MemUsed: 11 << 30, MemTotal: 32 << 30,
				DiskRoot: `C:\`, DiskUsed: 700 << 30, DiskTotal: 950 << 30, NetDown: -1}
		},
	}
}

func TestHomeSelectByNumberAndEnter(t *testing.T) {
	if got := HomeChoice(press(NewHomeModel(homeOpts()), "1")); got != "clean" {
		t.Errorf("number: %q", got)
	}
	if got := HomeChoice(press(NewHomeModel(homeOpts()), "down", "up", "enter")); got != "clean" {
		t.Errorf("arrows: %q", got)
	}
}

func TestHomeUnavailableShowsNote(t *testing.T) {
	m := press(NewHomeModel(homeOpts()), "2")
	if HomeChoice(m) != "" {
		t.Fatal("unavailable item chosen")
	}
	if !strings.Contains(m.View(), "coming soon") {
		t.Error("note not shown")
	}
	if HomeChoice(press(m, "q")) != "" {
		t.Error("quit returned a choice")
	}
}

func TestHomeViewShowsStats(t *testing.T) {
	Init("never", true)
	v := NewHomeModel(homeOpts()).View()
	for _, want := range []string{"OOW", "Windows 11", "CPU", "17%", "MEMORY", "11.0 GB / 32.0 GB", "DISK C:", "Deep Clean", "soon"} {
		if !strings.Contains(v, want) {
			t.Errorf("home view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "\x1b[") {
		t.Error("ANSI escapes with color disabled")
	}
}
