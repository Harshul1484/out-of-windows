package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Harshul1484/out-of-windows/internal/monitor"
)

// StatusOptions configure the live dashboard.
type StatusOptions struct {
	OS       string
	Interval time.Duration
	Sample   func() (monitor.Snapshot, error)
}

type statusSample struct {
	snap monitor.Snapshot
	err  error
}

type statusTick struct{}

type status struct {
	opts      StatusOptions
	snap      *monitor.Snapshot
	err       error
	cores     bool
	sortBy    string
	recvTotal float64
	sentTotal float64
	lastAt    time.Time
	width     int
	height    int
	quit      bool
}

// NewStatusModel returns the dashboard model (exported for tests).
func NewStatusModel(opts StatusOptions) tea.Model {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	return &status{opts: opts, sortBy: "cpu", width: Width(), height: 30}
}

// RunStatus shows the live dashboard until the user quits.
func RunStatus(opts StatusOptions) error {
	_, err := tea.NewProgram(NewStatusModel(opts), tea.WithAltScreen()).Run()
	return err
}

func (m *status) sample() tea.Cmd {
	return func() tea.Msg {
		s, err := m.opts.Sample()
		return statusSample{s, err}
	}
}

func (m *status) Init() tea.Cmd { return m.sample() }

func (m *status) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case statusSample:
		m.err = msg.err
		if msg.err == nil {
			s := msg.snap
			if !m.lastAt.IsZero() {
				dt := s.Time.Sub(m.lastAt).Seconds()
				m.recvTotal += s.NetRecv * dt
				m.sentTotal += s.NetSent * dt
			}
			m.lastAt = s.Time
			m.snap = &s
		}
		return m, tea.Tick(m.opts.Interval, func(time.Time) tea.Msg { return statusTick{} })
	case statusTick:
		return m, m.sample()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "c":
			m.cores = !m.cores
		case "p":
			m.sortBy = map[string]string{"cpu": "memory", "memory": "io", "io": "cpu"}[m.sortBy]
		}
	}
	return m, nil
}

// Sparkline renders values 0-100 as block characters.
func Sparkline(values []float64) string {
	const blocks = "▁▂▃▄▅▆▇█"
	r := []rune(blocks)
	var b strings.Builder
	for _, v := range values {
		i := int(v / 100 * float64(len(r)-1))
		i = min(max(i, 0), len(r)-1)
		b.WriteRune(r[i])
	}
	return b.String()
}

func (m *status) View() string {
	if m.quit {
		return ""
	}
	if m.snap == nil {
		if m.err != nil {
			return "\n " + Err.Render("Could not read system metrics: "+m.err.Error()) + "\n"
		}
		return "\n " + Muted.Render("Measuring…") + "\n"
	}
	return RenderStatus(*m.snap, StatusView{
		OS: m.opts.OS, Width: m.width, Height: m.height, Cores: m.cores, SortBy: m.sortBy,
		RecvTotal: m.recvTotal, SentTotal: m.sentTotal, Live: true,
	})
}

// StatusView controls RenderStatus.
type StatusView struct {
	OS                   string
	Width, Height        int
	Cores                bool
	SortBy               string
	RecvTotal, SentTotal float64
	Live                 bool
	Processes            int // rows of processes (0 = fit the screen)
}

// RenderStatus draws a snapshot; it is used by the live dashboard and for
// one-shot text output.
func RenderStatus(s monitor.Snapshot, v StatusView) string {
	width := min(max(v.Width, 60), 140)
	bar := 16
	label := func(l string) string { return Bold.Render(PadRight(l, 9)) }
	var b strings.Builder
	head := Title.Render("SYSTEM STATUS")
	right := strings.Join(nonEmpty(v.OS, "up "+uptime(time.Duration(s.Uptime*float64(time.Second))), s.Time.Format("15:04:05")), " "+SymDot+" ")
	b.WriteString("\n " + head + "  " + Muted.Render(right) + "\n\n")

	cpuInfo := Plural(len(s.Cores), "logical processor", "logical processors")
	if s.FreqMHz > 0 {
		cpuInfo += fmt.Sprintf(" %s %.2f GHz", SymDot, s.FreqMHz/1000)
	}
	fmt.Fprintf(&b, " %s %s %s  %s\n", label("CPU"), Bar(s.CPU, bar), PadLeft(fmt.Sprintf("%.0f%%", s.CPU), 4), Muted.Render(cpuInfo))
	if len(s.Cores) > 0 {
		if v.Cores {
			for i := 0; i < len(s.Cores); i += 4 {
				var parts []string
				for j := i; j < min(i+4, len(s.Cores)); j++ {
					parts = append(parts, fmt.Sprintf("%-3d %s %3.0f%%", j, Bar(s.Cores[j], 8), s.Cores[j]))
				}
				b.WriteString(" " + PadRight("", 10) + strings.Join(parts, "  ") + "\n")
			}
		} else {
			b.WriteString(" " + PadRight("", 10) + Accent.Render(Sparkline(s.Cores)) + Muted.Render("  per core (c expands)") + "\n")
		}
	}

	mem := fmt.Sprintf("%s / %s", Bytes(int64(s.MemUsed)), Bytes(int64(s.MemTotal)))
	if s.CommitMax > 0 {
		mem += fmt.Sprintf(" %s committed %s / %s", SymDot, Bytes(int64(s.CommitUsed)), Bytes(int64(s.CommitMax)))
	}
	if s.Cached > 0 {
		mem += fmt.Sprintf(" %s cached %s", SymDot, Bytes(int64(s.Cached)))
	}
	fmt.Fprintf(&b, " %s %s %s  %s\n", label("Memory"), Bar(s.MemPercent(), bar), PadLeft(fmt.Sprintf("%.0f%%", s.MemPercent()), 4), Muted.Render(mem))

	if len(s.GPUs) == 0 {
		fmt.Fprintf(&b, " %s %s\n", label("GPU"), Muted.Render("not available"))
	}
	for i, g := range s.GPUs {
		name := label("GPU")
		if i > 0 {
			name = label("")
		}
		info := []string{g.Name}
		if g.MemoryTotal > 0 {
			info = append(info, fmt.Sprintf("%s / %s", Bytes(int64(g.MemoryUsed)), Bytes(int64(g.MemoryTotal))))
		} else if g.MemoryUsed > 0 {
			info = append(info, Bytes(int64(g.MemoryUsed))+" used")
		}
		if g.Temperature >= 0 {
			info = append(info, fmt.Sprintf("%.0f°C", g.Temperature))
		}
		if g.Utilization >= 0 {
			fmt.Fprintf(&b, " %s %s %s  %s\n", name, Bar(g.Utilization, bar), PadLeft(fmt.Sprintf("%.0f%%", g.Utilization), 4), Muted.Render(strings.Join(info, " "+SymDot+" ")))
		} else {
			fmt.Fprintf(&b, " %s %s  %s\n", name, PadRight(Muted.Render("usage not available"), bar+5), Muted.Render(strings.Join(info, " "+SymDot+" ")))
		}
	}

	io := fmt.Sprintf("read %s/s %s write %s/s", Bytes(int64(s.DiskRead)), SymDot, Bytes(int64(s.DiskWrite)))
	if s.DiskActive >= 0 {
		fmt.Fprintf(&b, " %s %s %s  %s\n", label("Disk"), Bar(s.DiskActive, bar), PadLeft(fmt.Sprintf("%.0f%%", s.DiskActive), 4), Muted.Render("active "+SymDot+" "+io))
	} else {
		fmt.Fprintf(&b, " %s %s\n", label("Disk"), Muted.Render(io))
	}
	var vols []string
	for _, vol := range s.Volumes {
		used := vol.Total - vol.Free
		vols = append(vols, fmt.Sprintf("%s %s / %s (%.0f%%)", strings.TrimSuffix(vol.Root, `\`), Bytes(int64(used)), Bytes(int64(vol.Total)),
			float64(used)/float64(max(vol.Total, 1))*100))
	}
	if len(vols) > 0 {
		b.WriteString(" " + PadRight("", 10) + Muted.Render(strings.Join(vols, "   ")) + "\n")
	}

	net := fmt.Sprintf("↓ %s/s  ↑ %s/s", Bytes(int64(s.NetRecv)), Bytes(int64(s.NetSent)))
	if v.Live {
		net += Muted.Render(fmt.Sprintf("  %s this session ↓ %s ↑ %s", SymDot, Bytes(int64(v.RecvTotal)), Bytes(int64(v.SentTotal))))
	}
	if busiest := busiestInterface(s.Interfaces); busiest != "" {
		net += Muted.Render("  " + SymDot + " " + TruncateMiddle(busiest, 30))
	}
	fmt.Fprintf(&b, " %s %s\n", label("Network"), net)

	procs := append([]monitor.Process(nil), s.Processes...)
	sortBy := v.SortBy
	if sortBy == "" {
		sortBy = "cpu"
	}
	monitor.SortProcesses(procs, sortBy)
	rows := v.Processes
	if rows == 0 {
		rows = max(5, v.Height-20)
	}
	nameW := max(16, width-50)
	fmt.Fprintf(&b, "\n %s %s %s %s %s\n", Bold.Render(PadRight(fmt.Sprintf("TOP PROCESSES (by %s)", sortBy), nameW+8)),
		Bold.Render(PadLeft("CPU", 7)), Bold.Render(PadLeft("Memory", 10)), Bold.Render(PadLeft("I/O", 11)), Bold.Render(PadLeft("PID", 7)))
	for i, p := range procs {
		if i == rows {
			break
		}
		fmt.Fprintf(&b, " %s %s %s %s %s\n", PadRight(TruncateMiddle(p.Name, nameW+8), nameW+8), PadLeft(fmt.Sprintf("%.1f%%", p.CPU), 7),
			PadLeft(Bytes(int64(p.Memory)), 10), PadLeft(Bytes(int64(p.IORate))+"/s", 11), Muted.Render(PadLeft(fmt.Sprint(p.PID), 7)))
	}
	b.WriteString(" " + Muted.Render(fmt.Sprintf("%s %s %s threads %s %s handles", Plural(s.ProcCount, "process", "processes"),
		SymDot, Count(int(s.Threads)), SymDot, Count(int(s.Handles)))) + "\n")
	if v.Live {
		b.WriteString("\n " + Muted.Render("c cores "+SymDot+" p sort processes "+SymDot+" q quit") + "\n")
	}
	return b.String()
}

func busiestInterface(list []monitor.Interface) string {
	best, name := -1.0, ""
	for _, i := range list {
		if t := i.RecvRate + i.SentRate; t > best {
			best, name = t, i.Name
		}
	}
	return name
}

func uptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func nonEmpty(v ...string) []string {
	var out []string
	for _, s := range v {
		if strings.TrimSpace(s) != "" && s != "up 0m" {
			out = append(out, s)
		}
	}
	return out
}
