package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// CheckItem is one row of a checklist. Rows with Header set are section
// titles and cannot be selected.
type CheckItem struct {
	Header   string
	Label    string
	Right    string   // right-aligned value, e.g. a size
	Detail   []string // shown below the list for the focused row
	Checked  bool
	Disabled bool   // shown but not selectable
	Note     string // why it is disabled
	Weight   int64  // bytes counted in the selection total
}

// ChecklistResult is the user's decision.
type ChecklistResult struct {
	Confirmed bool
	Checked   []int // indexes into the items slice
}

// ChecklistOptions configure RunChecklist.
type ChecklistOptions struct {
	Title       string
	ConfirmVerb string // e.g. "clean"; shown in the key hint
	ShowWeight  bool   // show the selected byte total
}

type checklist struct {
	opts   ChecklistOptions
	items  []CheckItem
	cursor int
	done   bool
	result ChecklistResult
	width  int
}

// NewChecklistModel returns the Bubble Tea model (exported for tests).
func NewChecklistModel(opts ChecklistOptions, items []CheckItem) tea.Model {
	m := &checklist{opts: opts, items: items, cursor: -1, width: Width()}
	m.move(1)
	return m
}

// RunChecklist shows an interactive checklist inline in the terminal.
func RunChecklist(opts ChecklistOptions, items []CheckItem) (ChecklistResult, error) {
	m := NewChecklistModel(opts, items).(*checklist)
	if m.cursor < 0 {
		return ChecklistResult{}, nil
	}
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return ChecklistResult{}, err
	}
	return final.(*checklist).result, nil
}

// Result returns the outcome of a finished checklist model (for tests).
func ChecklistOutcome(m tea.Model) ChecklistResult { return m.(*checklist).result }

func (m *checklist) Init() tea.Cmd { return nil }

func (m *checklist) selectable(i int) bool {
	return i >= 0 && i < len(m.items) && m.items[i].Header == "" && !m.items[i].Disabled
}

func (m *checklist) move(delta int) {
	for i := m.cursor + delta; i >= 0 && i < len(m.items); i += delta {
		if m.selectable(i) {
			m.cursor = i
			return
		}
	}
}

func (m *checklist) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k", "shift+tab":
			m.move(-1)
		case "down", "j", "tab":
			m.move(1)
		case "home", "g":
			m.cursor = -1
			m.move(1)
		case "end", "G":
			m.cursor = len(m.items)
			m.move(-1)
		case " ", "x":
			if m.selectable(m.cursor) {
				m.items[m.cursor].Checked = !m.items[m.cursor].Checked
			}
		case "a", "A":
			all := true
			for i := range m.items {
				if m.selectable(i) && !m.items[i].Checked {
					all = false
				}
			}
			for i := range m.items {
				if m.selectable(i) {
					m.items[i].Checked = !all
				}
			}
		case "enter":
			m.done = true
			m.result.Confirmed = true
			for i := range m.items {
				if m.selectable(i) && m.items[i].Checked {
					m.result.Checked = append(m.result.Checked, i)
				}
			}
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *checklist) View() string {
	if m.done {
		return ""
	}
	width := min(m.width, 100)
	var b strings.Builder
	b.WriteString("\n " + Title.Render(m.opts.Title) + "\n")

	labelW := 0
	for _, it := range m.items {
		labelW = max(labelW, len([]rune(it.Label)))
	}
	labelW = min(labelW+2, width-24)

	var total int64
	selected := 0
	for i, it := range m.items {
		if it.Header != "" {
			b.WriteString("\n " + Bold.Render(it.Header) + "\n")
			continue
		}
		pointer := "  "
		if i == m.cursor {
			pointer = Accent.Render(SymPointer) + " "
		}
		box := "[ ]"
		switch {
		case it.Disabled:
			box = Muted.Render("[-]")
		case it.Checked:
			box = OK.Render("[" + SymOK + "]")
			total += it.Weight
			selected++
		}
		label := PadRight(TruncateMiddle(it.Label, labelW), labelW)
		if i == m.cursor {
			label = Bold.Render(label)
		} else if it.Disabled {
			label = Muted.Render(label)
		}
		right := it.Right
		if it.Disabled && it.Note != "" {
			right = Muted.Render(TruncateMiddle(it.Note, max(10, width-labelW-12)))
		}
		fmt.Fprintf(&b, " %s%s %s %s\n", pointer, box, label, right)
	}

	if m.selectable(m.cursor) && len(m.items[m.cursor].Detail) > 0 {
		b.WriteString("\n")
		for _, line := range m.items[m.cursor].Detail {
			b.WriteString("   " + Muted.Render(wrap(line, width-6, "   ")) + "\n")
		}
	}

	b.WriteString("\n " + Divider(width-2) + "\n ")
	if m.opts.ShowWeight {
		b.WriteString(Bold.Render("Selected ") + Accent.Render(Bytes(total)) +
			Muted.Render(fmt.Sprintf(" in %s", Plural(selected, "item", "items"))) + "   ")
	}
	verb := m.opts.ConfirmVerb
	if verb == "" {
		verb = "confirm"
	}
	b.WriteString(Muted.Render(fmt.Sprintf("↑↓ move %s space toggle %s a all %s enter %s %s q cancel",
		SymDot, SymDot, SymDot, verb, SymDot)) + "\n")
	return b.String()
}

// wrap breaks s into lines of at most width runes, indenting continuation
// lines.
func wrap(s string, width int, indent string) string {
	if width < 20 {
		return s
	}
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n"+indent)
}

// Wrap is wrap for callers outside the package.
func Wrap(s string, width int, indent string) string { return wrap(s, width, indent) }
