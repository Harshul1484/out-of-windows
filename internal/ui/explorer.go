package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Harshul1484/out-of-windows/internal/analyzer"
)

// ExplorerOptions configure the disk explorer.
type ExplorerOptions struct {
	Title      string
	Root       *analyzer.Node
	Largest    []analyzer.File
	DiskTotal  int64 // capacity of the volume, for "% of disk"
	Notes      []string
	StartLarge bool
	// Recycle moves the given paths to the Recycle Bin after safety checks
	// and returns, per path, nil or the reason it was not moved.
	Recycle func(paths []string) []error
	// Reveal shows a path in File Explorer.
	Reveal func(path string)
	// Rescan measures a folder again and returns a fresh node.
	Rescan func(path string) (*analyzer.Node, error)
}

type sortMode int

const (
	sortSize sortMode = iota
	sortName
	sortFiles
	sortModified
)

func (s sortMode) String() string {
	return [...]string{"size", "name", "files", "modified"}[s]
}

type inputMode int

const (
	inputNone inputMode = iota
	inputFilter
	inputSearch
)

type explorer struct {
	opts    ExplorerOptions
	cur     *analyzer.Node
	entries []analyzer.Entry
	view    []int
	cursor  int
	offset  int
	sort    sortMode
	filter  string
	search  string
	input   inputMode
	marked  map[string]analyzer.Entry
	large   bool
	confirm bool
	message string
	width   int
	height  int
	quit    bool
}

// NewExplorerModel returns the Bubble Tea model (exported for tests).
func NewExplorerModel(opts ExplorerOptions) tea.Model {
	m := &explorer{opts: opts, cur: opts.Root, marked: map[string]analyzer.Entry{}, width: Width(), height: 30,
		large: opts.StartLarge}
	m.load()
	return m
}

// RunExplorer shows the interactive disk explorer.
func RunExplorer(opts ExplorerOptions) error {
	_, err := tea.NewProgram(NewExplorerModel(opts), tea.WithAltScreen()).Run()
	return err
}

// ExplorerPath returns the folder shown by a model (for tests).
func ExplorerPath(m tea.Model) string { return m.(*explorer).cur.Path }

func (m *explorer) load() {
	if m.large {
		m.entries = m.entries[:0]
		for _, f := range m.opts.Largest {
			m.entries = append(m.entries, analyzer.Entry{Name: f.Path, Path: f.Path, Size: f.Size, Files: 1, Modified: f.Modified})
		}
	} else {
		m.entries = analyzer.List(m.cur)
	}
	m.applyView()
}

func (m *explorer) applyView() {
	q := strings.ToLower(m.filter)
	m.view = m.view[:0]
	for i, e := range m.entries {
		if q == "" || strings.Contains(strings.ToLower(e.Name), q) {
			m.view = append(m.view, i)
		}
	}
	less := func(a, b analyzer.Entry) bool {
		switch m.sort {
		case sortName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortFiles:
			return a.Files > b.Files
		case sortModified:
			return a.Modified.After(b.Modified)
		}
		return a.Size > b.Size
	}
	sort.SliceStable(m.view, func(i, j int) bool {
		a, b := m.entries[m.view[i]], m.entries[m.view[j]]
		if !m.large && m.sort == sortName && a.IsDir != b.IsDir {
			return a.IsDir
		}
		return less(a, b)
	})
	m.cursor = min(m.cursor, max(0, len(m.view)-1))
	m.scroll()
}

func (m *explorer) rows() int { return max(3, m.height-10) }

func (m *explorer) scroll() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.rows() {
		m.offset = m.cursor - m.rows() + 1
	}
}

func (m *explorer) move(d int) {
	if len(m.view) > 0 {
		m.cursor = min(max(0, m.cursor+d), len(m.view)-1)
		m.scroll()
	}
}

func (m *explorer) current() (analyzer.Entry, bool) {
	if m.cursor >= 0 && m.cursor < len(m.view) {
		return m.entries[m.view[m.cursor]], true
	}
	return analyzer.Entry{}, false
}

func (m *explorer) open() {
	e, ok := m.current()
	if !ok || !e.IsDir || e.Reparse || e.Node == nil {
		if ok && e.Reparse {
			m.message = "Links and junctions are not followed."
		}
		return
	}
	m.cur, m.cursor, m.offset, m.filter = e.Node, 0, 0, ""
	m.load()
}

func (m *explorer) up() {
	if m.large {
		m.large = false
		m.load()
		return
	}
	if m.cur.Parent == nil {
		return
	}
	child := m.cur
	m.cur, m.filter = m.cur.Parent, ""
	m.load()
	for i, idx := range m.view {
		if m.entries[idx].Node == child {
			m.cursor = i
			m.scroll()
		}
	}
}

func (m *explorer) findNext(from int) {
	q := strings.ToLower(m.search)
	if q == "" {
		return
	}
	for k := 1; k <= len(m.view); k++ {
		i := (from + k) % len(m.view)
		if strings.Contains(strings.ToLower(m.entries[m.view[i]].Name), q) {
			m.cursor = i
			m.scroll()
			return
		}
	}
	m.message = "No match for " + m.search
}

func (m *explorer) Init() tea.Cmd { return nil }

func (m *explorer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
		return m, nil
	case tea.KeyMsg:
		k := msg.String()
		if k == "ctrl+c" {
			m.quit = true
			return m, tea.Quit
		}
		if m.confirm {
			return m.updateConfirm(k)
		}
		if m.input != inputNone {
			return m.updateInput(msg)
		}
		m.message = ""
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
		case "enter", "right", "l":
			m.open()
		case "backspace", "left", "h":
			m.up()
		case "s":
			m.sort = (m.sort + 1) % 4
			m.applyView()
		case "f":
			m.input = inputFilter
		case "/":
			m.input, m.search = inputSearch, ""
		case "n":
			m.findNext(m.cursor)
		case "L":
			m.large = !m.large
			m.cursor, m.offset, m.filter = 0, 0, ""
			m.load()
		case " ", "x":
			if e, ok := m.current(); ok {
				if _, on := m.marked[e.Path]; on {
					delete(m.marked, e.Path)
				} else {
					m.marked[e.Path] = e
				}
				m.move(1)
			}
		case "d", "delete":
			if len(m.marked) == 0 {
				if e, ok := m.current(); ok {
					m.marked[e.Path] = e
				}
			}
			if len(m.marked) > 0 && m.opts.Recycle != nil {
				m.confirm = true
			}
		case "o":
			if e, ok := m.current(); ok && m.opts.Reveal != nil {
				m.opts.Reveal(e.Path)
				m.message = "Shown in File Explorer."
			}
		case "r":
			if m.opts.Rescan != nil && !m.large {
				m.rescan()
			}
		case "esc":
			if m.filter != "" {
				m.filter = ""
				m.applyView()
				return m, nil
			}
			m.quit = true
			return m, tea.Quit
		case "q":
			m.quit = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *explorer) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	target := &m.filter
	if m.input == inputSearch {
		target = &m.search
	}
	switch msg.String() {
	case "enter":
		m.input = inputNone
		return m, nil
	case "esc":
		*target = ""
		m.input = inputNone
	case "backspace":
		if r := []rune(*target); len(r) > 0 {
			*target = string(r[:len(r)-1])
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			*target += string(msg.Runes)
			if msg.Type == tea.KeySpace && len(msg.Runes) == 0 {
				*target += " "
			}
		}
	}
	if m.input == inputSearch {
		m.findNext(m.cursor - 1)
	} else {
		m.cursor = 0
		m.applyView()
	}
	return m, nil
}

func (m *explorer) updateConfirm(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "y", "Y":
		m.confirm = false
		var paths []string
		for p := range m.marked {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		errs := m.opts.Recycle(paths)
		moved, refused := 0, []string{}
		var bytes int64
		for i, p := range paths {
			e := m.marked[p]
			if errs[i] != nil {
				refused = append(refused, filepath.Base(p)+": "+errs[i].Error())
				continue
			}
			moved++
			bytes += e.Size
			m.opts.Root.Remove(p, e.IsDir, e.Size, e.Files)
			// Drop the file, or every file inside the folder, from the
			// largest-files list.
			kept := m.opts.Largest[:0]
			for _, f := range m.opts.Largest {
				inside := strings.EqualFold(f.Path, p) ||
					(e.IsDir && strings.HasPrefix(strings.ToLower(f.Path), strings.ToLower(strings.TrimSuffix(p, `\`)+`\`)))
				if !inside {
					kept = append(kept, f)
				}
			}
			m.opts.Largest = kept
		}
		m.marked = map[string]analyzer.Entry{}
		m.message = fmt.Sprintf("%s (%s) moved to the Recycle Bin.", Plural(moved, "item", "items"), Bytes(bytes))
		if len(refused) > 0 {
			m.message += " Not moved: " + strings.Join(refused, "; ")
		}
		m.load()
	case "n", "N", "esc", "q":
		m.confirm = false
		m.message = "Nothing was moved."
	}
	return m, nil
}

func (m *explorer) rescan() {
	node, err := m.opts.Rescan(m.cur.Path)
	if err != nil {
		m.message = "Rescan failed: " + err.Error()
		return
	}
	old := m.cur
	node.Name, node.Parent = old.Name, old.Parent
	if old.Parent != nil {
		for i, c := range old.Parent.Children {
			if c == old {
				old.Parent.Children[i] = node
			}
		}
		for p := old.Parent; p != nil; p = p.Parent {
			p.Size += node.Size - old.Size
			p.Files += node.Files - old.Files
		}
	} else {
		m.opts.Root = node
	}
	m.cur = node
	m.load()
	m.message = "Rescanned."
}

func (m *explorer) View() string {
	if m.quit {
		return ""
	}
	width := min(m.width, 140)
	var b strings.Builder
	total := m.cur.Size
	title := m.cur.Path
	if m.large {
		title = fmt.Sprintf("Largest files in %s", m.opts.Root.Path)
		total = m.opts.Root.Size
	}
	b.WriteString("\n " + Title.Render(m.opts.Title) + "  " + Bold.Render(TruncateMiddle(title, width-len(m.opts.Title)-6)) + "\n")
	stats := fmt.Sprintf("%s in %s", Bytes(total), Plural(int(m.cur.Files), "file", "files"))
	if m.opts.DiskTotal > 0 {
		stats += fmt.Sprintf(" · %.1f%% of the disk", float64(total)/float64(m.opts.DiskTotal)*100)
	}
	b.WriteString(" " + Muted.Render(stats) + "\n")
	for _, n := range m.opts.Notes {
		b.WriteString(" " + Muted.Render(n) + "\n")
	}
	switch {
	case m.input == inputFilter:
		b.WriteString(" " + Accent.Render("filter: ") + m.filter + Accent.Render("█") + "\n")
	case m.input == inputSearch:
		b.WriteString(" " + Accent.Render("/") + m.search + Accent.Render("█") + "\n")
	case m.filter != "":
		b.WriteString(" " + Muted.Render("filter: "+m.filter+"  (esc clears)") + "\n")
	default:
		b.WriteString("\n")
	}

	nameW := max(20, width-48)
	barW := 14
	end := min(len(m.view), m.offset+m.rows())
	for pos := m.offset; pos < end; pos++ {
		e := m.entries[m.view[pos]]
		pointer := "  "
		if pos == m.cursor {
			pointer = Accent.Render(SymPointer) + " "
		}
		mark := " "
		if _, on := m.marked[e.Path]; on {
			mark = Warn.Render(SymItem)
		}
		icon := Muted.Render("·")
		name := e.Name
		switch {
		case e.Reparse:
			icon, name = Muted.Render("↪"), e.Name+" (link, not followed)"
		case e.IsDir:
			icon = Accent.Render("▸")
		}
		name = PadRight(TruncateMiddle(name, nameW), nameW)
		if pos == m.cursor {
			name = Bold.Render(name)
		}
		pct := 0.0
		if total > 0 {
			pct = float64(e.Size) / float64(total) * 100
		}
		extra := ""
		if e.IsDir && !e.Reparse {
			extra = Muted.Render(ui0(e.Files))
		}
		fmt.Fprintf(&b, " %s%s %s %s %s %s %s %s\n", pointer, mark, icon, name, PadLeft(Bytes(e.Size), 9),
			Bar(pct, barW), PadLeft(fmt.Sprintf("%.1f%%", pct), 6), extra)
	}
	if len(m.view) == 0 {
		b.WriteString("   " + Muted.Render("Nothing here.") + "\n")
	}

	b.WriteString("\n")
	if m.confirm {
		var bytes int64
		for _, e := range m.marked {
			bytes += e.Size
		}
		b.WriteString(" " + Warn.Render(fmt.Sprintf("Move %s (%s) to the Recycle Bin? ", Plural(len(m.marked), "item", "items"), Bytes(bytes))) +
			Bold.Render("y") + Muted.Render("/") + Bold.Render("n") + "\n")
		return b.String()
	}
	if m.message != "" {
		b.WriteString(" " + Muted.Render(m.message) + "\n")
	} else if len(m.marked) > 0 {
		b.WriteString(" " + Warn.Render(fmt.Sprintf("%d marked", len(m.marked))) + Muted.Render(" · d to move to the Recycle Bin") + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(" " + Muted.Render(fmt.Sprintf("↑↓ move · enter open · ⌫ back · s sort (%s) · f filter · / search · L largest · space mark · d recycle · o show · r rescan · q quit", m.sort)) + "\n")
	return b.String()
}

func ui0(files int64) string {
	if files == 1 {
		return "1 file"
	}
	return Count(int(files)) + " files"
}
