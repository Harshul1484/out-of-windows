package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HomeStats is one refresh of the live numbers on the home screen.
type HomeStats struct {
	CPU       float64
	CPUReady  bool
	MemUsed   uint64
	MemTotal  uint64
	DiskRoot  string
	DiskUsed  uint64
	DiskTotal uint64
	NetDown   float64 // bytes/s; negative when unavailable
	NetUp     float64
}

// MenuItem is one entry on the home screen.
type MenuItem struct {
	Section   string // section title shown before the first item of a section
	Label     string
	Command   string // command to run when chosen
	Available bool
	Note      string // shown when an unavailable item is chosen
}

// HomeOptions configure the home screen.
type HomeOptions struct {
	Product  string // shown beside the logo, e.g. "out-of-windows 0.1.0"
	OS       string // e.g. "Windows 11"
	Banner   string // e.g. sandbox mode notice
	Elevated bool
	Items    []MenuItem
	Stats    func() HomeStats
	Interval time.Duration
}

type homeTick time.Time

type home struct {
	opts    HomeOptions
	stats   HomeStats
	cursor  int
	chosen  string
	message string
	width   int
	height  int
}

// NewHomeModel returns the Bubble Tea model (exported for tests).
func NewHomeModel(opts HomeOptions) tea.Model {
	if opts.Interval == 0 {
		opts.Interval = time.Second
	}
	m := &home{opts: opts, width: Width()}
	if opts.Stats != nil {
		m.stats = opts.Stats()
	}
	return m
}

// RunHome shows the home screen and returns the chosen command ("" to quit).
func RunHome(opts HomeOptions) (string, error) {
	final, err := tea.NewProgram(NewHomeModel(opts), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	return final.(*home).chosen, nil
}

// HomeChoice returns the command chosen in a finished home model (for tests).
func HomeChoice(m tea.Model) string { return m.(*home).chosen }

func (m *home) Init() tea.Cmd { return m.tick() }

func (m *home) tick() tea.Cmd {
	return tea.Tick(m.opts.Interval, func(t time.Time) tea.Msg { return homeTick(t) })
}

func (m *home) choose(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.opts.Items) {
		return m, nil
	}
	m.cursor = i
	it := m.opts.Items[i]
	if !it.Available {
		m.message = it.Note
		return m, nil
	}
	m.chosen = it.Command
	return m, tea.Quit
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case homeTick:
		if m.opts.Stats != nil {
			m.stats = m.opts.Stats()
		}
		return m, m.tick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		k := msg.String()
		switch k {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.cursor = (m.cursor - 1 + len(m.opts.Items)) % len(m.opts.Items)
			m.message = ""
		case "down", "j", "tab":
			m.cursor = (m.cursor + 1) % len(m.opts.Items)
			m.message = ""
		case "enter", " ":
			return m.choose(m.cursor)
		default:
			if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
				n := int(k[0] - '0')
				if n == 0 {
					n = 10
				}
				return m.choose(n - 1)
			}
		}
	}
	return m, nil
}

var circled = []string{"①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧", "⑨", "⑩", "⑪", "⑫"}

func (m *home) View() string {
	inner := min(max(m.width-6, 44), 72)
	var b strings.Builder

	// The logo, with product, OS and elevation right-aligned beside the
	// wordmark; on a terminal too narrow for both they go below it.
	info := []string{m.opts.Product, Muted.Render(m.opts.OS)}
	if m.opts.Elevated {
		info = append(info, Warn.Render("admin"))
	}
	logo := LogoLockup()
	logoW, infoW := lipgloss.Width(logo[0]), 0
	for _, s := range info {
		infoW = max(infoW, lipgloss.Width(s))
	}
	if logoW+2+infoW <= inner {
		for i, l := range logo {
			r := ""
			if i >= 1 && i-1 < len(info) {
				r = info[i-1]
			}
			b.WriteString(l + strings.Repeat(" ", inner-logoW-lipgloss.Width(r)) + r + "\n")
		}
	} else {
		b.WriteString(strings.Join(logo, "\n") + "\n" + strings.Join(info, "\n") + "\n")
	}
	if m.opts.Banner != "" {
		b.WriteString(Warn.Render(m.opts.Banner) + "\n")
	}
	b.WriteString("\n")

	s := m.stats
	bar := 18
	cpu := Muted.Render("measuring…")
	if s.CPUReady {
		cpu = Bar(s.CPU, bar) + " " + fmt.Sprintf("%3.0f%%", s.CPU)
	}
	b.WriteString(statLine("CPU", cpu))
	if s.MemTotal > 0 {
		pct := float64(s.MemUsed) / float64(s.MemTotal) * 100
		b.WriteString(statLine("MEMORY", Bar(pct, bar)+" "+fmt.Sprintf("%s / %s", Bytes(int64(s.MemUsed)), Bytes(int64(s.MemTotal)))))
	}
	if s.DiskTotal > 0 {
		pct := float64(s.DiskUsed) / float64(s.DiskTotal) * 100
		b.WriteString(statLine("DISK "+strings.TrimSuffix(s.DiskRoot, `\`),
			Bar(pct, bar)+" "+fmt.Sprintf("%s / %s", Bytes(int64(s.DiskUsed)), Bytes(int64(s.DiskTotal)))))
	}
	if s.NetDown >= 0 && (s.NetDown > 0 || s.NetUp > 0) {
		b.WriteString(statLine("NETWORK", fmt.Sprintf("↓ %s/s   ↑ %s/s", Bytes(int64(s.NetDown)), Bytes(int64(s.NetUp)))))
	}

	b.WriteString("\n" + Divider(inner) + "\n")
	for i, it := range m.opts.Items {
		if it.Section != "" {
			b.WriteString("\n" + Muted.Render(strings.ToUpper(it.Section)) + "\n")
		}
		num := circled[i%len(circled)]
		label := it.Label
		var line string
		switch {
		case i == m.cursor:
			line = Accent.Render(SymPointer+" "+num+" ") + Bold.Render(label)
		case !it.Available:
			line = Muted.Render("  " + num + " " + label)
		default:
			line = "  " + Accent.Render(num) + " " + label
		}
		if !it.Available {
			line += Muted.Render("  soon")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	if m.message != "" {
		b.WriteString(Warn.Render(m.message) + "\n")
	}
	b.WriteString(Muted.Render("number or ↑↓ + enter to select " + SymDot + " q to quit"))
	return Box.Width(inner+4).Render(b.String()) + "\n"
}

func statLine(label, value string) string {
	return Accent.Render(SymItem) + " " + PadRight(Bold.Render(label), 10) + " " + value + "\n"
}
