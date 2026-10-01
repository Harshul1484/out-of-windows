package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PickItem is one row of a picker.
type PickItem struct {
	Title    string
	Subtitle string // shown muted after the title (version, publisher, ...)
	Right    string // right-aligned value, e.g. a size
	Size     int64  // used when sorting by size
	Disabled bool
	Note     string // why it cannot be picked
}

// PickerOptions configure RunPicker.
type PickerOptions struct {
	Title       string
	Multi       bool
	ConfirmVerb string
	Noun        string // "app"
}

// PickerResult is the user's decision.
type PickerResult struct {
	Confirmed bool
	Selected  []int // indexes into the items slice
}

type picker struct {
	opts      PickerOptions
	items     []PickItem
	view      []int // filtered and sorted indexes
	cursor    int   // position in view
	offset    int
	selected  map[int]bool
	filter    string
	filtering bool
	bySize    bool
	width     int
	height    int
	done      bool
	result    PickerResult
}

// NewPickerModel returns the Bubble Tea model (exported for tests).
func NewPickerModel(opts PickerOptions, items []PickItem) tea.Model {
	if opts.Noun == "" {
		opts.Noun = "item"
	}
	m := &picker{opts: opts, items: items, selected: map[int]bool{}, width: Width(), height: 24}
	m.refresh()
	return m
}

// RunPicker shows a searchable list and returns the chosen items.
func RunPicker(opts PickerOptions, items []PickItem) (PickerResult, error) {
	final, err := tea.NewProgram(NewPickerModel(opts, items), tea.WithAltScreen()).Run()
	if err != nil {
		return PickerResult{}, err
	}
	return final.(*picker).result, nil
}

// PickerOutcome returns the result of a finished picker model (for tests).
func PickerOutcome(m tea.Model) PickerResult { return m.(*picker).result }

func (m *picker) refresh() {
	q := strings.ToLower(strings.TrimSpace(m.filter))
	m.view = m.view[:0]
	for i, it := range m.items {
		if q == "" || strings.Contains(strings.ToLower(it.Title+" "+it.Subtitle), q) {
			m.view = append(m.view, i)
		}
	}
	if m.bySize {
		sort.SliceStable(m.view, func(a, b int) bool { return m.items[m.view[a]].Size > m.items[m.view[b]].Size })
	}
	if m.cursor >= len(m.view) {
		m.cursor = max(0, len(m.view)-1)
	}
	m.scroll()
}

func (m *picker) rows() int { return max(3, m.height-8) }

func (m *picker) scroll() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.rows() {
		m.offset = m.cursor - m.rows() + 1
	}
}

func (m *picker) move(d int) {
	if len(m.view) == 0 {
		return
	}
	m.cursor = min(max(0, m.cursor+d), len(m.view)-1)
	m.scroll()
}

func (m *picker) current() (int, bool) {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return 0, false
	}
	return m.view[m.cursor], true
}

func (m *picker) Init() tea.Cmd { return nil }

func (m *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
	case tea.KeyMsg:
		k := msg.String()
		if m.filtering {
			switch k {
			case "enter":
				m.filtering = false
			case "esc":
				m.filtering, m.filter = false, ""
				m.refresh()
			case "backspace":
				if r := []rune(m.filter); len(r) > 0 {
					m.filter = string(r[:len(r)-1])
					m.refresh()
				}
			case "up":
				m.move(-1)
			case "down":
				m.move(1)
			case "ctrl+c":
				m.done = true
				return m, tea.Quit
			default:
				if msg.Type == tea.KeyRunes || k == " " {
					m.filter += string(msg.Runes)
					if k == " " && len(msg.Runes) == 0 {
						m.filter += " "
					}
					m.cursor = 0
					m.refresh()
				}
			}
			return m, nil
		}
		switch k {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-m.rows())
		case "pgdown":
			m.move(m.rows())
		case "home", "g":
			m.move(-len(m.view))
		case "end", "G":
			m.move(len(m.view))
		case "/":
			m.filtering = true
		case "s":
			m.bySize = !m.bySize
			m.refresh()
		case " ", "x":
			if i, ok := m.current(); ok && m.opts.Multi && !m.items[i].Disabled {
				m.selected[i] = !m.selected[i]
			}
		case "enter":
			for i := range m.items {
				if m.selected[i] {
					m.result.Selected = append(m.result.Selected, i)
				}
			}
			if len(m.result.Selected) == 0 {
				i, ok := m.current()
				if !ok || m.items[i].Disabled {
					return m, nil
				}
				m.result.Selected = []int{i}
			}
			m.result.Confirmed, m.done = true, true
			return m, tea.Quit
		case "esc":
			if m.filter != "" {
				m.filter = ""
				m.refresh()
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		case "q", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *picker) View() string {
	if m.done {
		return ""
	}
	width := min(m.width, 120)
	var b strings.Builder
	b.WriteString("\n " + Title.Render(m.opts.Title) + "\n")
	switch {
	case m.filtering:
		b.WriteString(" " + Accent.Render("/") + " " + m.filter + Accent.Render("█") + "\n")
	case m.filter != "":
		b.WriteString(" " + Muted.Render("filter: ") + m.filter + Muted.Render("  (esc clears)") + "\n")
	default:
		b.WriteString("\n")
	}
	b.WriteString("\n")

	titleW := 0
	for _, i := range m.view {
		titleW = max(titleW, len([]rune(m.items[i].Title)))
	}
	titleW = min(titleW, max(20, width/2-8))
	rightW := 10
	subW := max(10, width-titleW-rightW-12)

	end := min(len(m.view), m.offset+m.rows())
	for pos := m.offset; pos < end; pos++ {
		i := m.view[pos]
		it := m.items[i]
		pointer := "  "
		if pos == m.cursor {
			pointer = Accent.Render(SymPointer) + " "
		}
		box := ""
		if m.opts.Multi {
			switch {
			case it.Disabled:
				box = Muted.Render("[-]") + " "
			case m.selected[i]:
				box = OK.Render("["+SymOK+"]") + " "
			default:
				box = "[ ] "
			}
		}
		title := PadRight(TruncateMiddle(it.Title, titleW), titleW)
		sub := it.Subtitle
		if it.Disabled && it.Note != "" {
			sub = it.Note
		}
		switch {
		case pos == m.cursor:
			title = Bold.Render(title)
		case it.Disabled:
			title = Muted.Render(title)
		}
		fmt.Fprintf(&b, " %s%s%s  %s %s\n", pointer, box, title,
			PadRight(Muted.Render(TruncateMiddle(sub, subW)), subW), PadLeft(it.Right, rightW))
	}
	if len(m.view) == 0 {
		b.WriteString("   " + Muted.Render("No matches.") + "\n")
	}

	b.WriteString("\n " + Divider(width-2) + "\n ")
	order := "name"
	if m.bySize {
		order = "size"
	}
	count := fmt.Sprintf("%s · sorted by %s", Plural(len(m.view), m.opts.Noun, m.opts.Noun+"s"), order)
	if n := len(m.selected); m.opts.Multi && n > 0 {
		sel := 0
		for _, v := range m.selected {
			if v {
				sel++
			}
		}
		count += fmt.Sprintf(" · %d selected", sel)
	}
	verb := m.opts.ConfirmVerb
	if verb == "" {
		verb = "choose"
	}
	keys := "↑↓ move · / search · s sort · enter " + verb + " · q quit"
	if m.opts.Multi {
		keys = "↑↓ move · / search · s sort · space select · enter " + verb + " · q quit"
	}
	b.WriteString(Muted.Render(count) + "\n " + Muted.Render(keys) + "\n")
	return b.String()
}
