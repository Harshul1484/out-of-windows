package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/monitor"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// monitorSource returns the real metrics source, or a simulated one in
// sandbox mode.
func (a *App) monitorSource() monitor.Source {
	if a.Sandbox != "" {
		return &sandbox.Monitor{}
	}
	return monitor.NewSystem()
}

// firstSnapshot primes a sampler and returns a snapshot with real rates
// measured over interval.
func firstSnapshot(ctx context.Context, s *monitor.Sampler, interval time.Duration) (monitor.Snapshot, error) {
	if _, err := s.Sample(); err != nil {
		return monitor.Snapshot{}, err
	}
	select {
	case <-ctx.Done():
		return monitor.Snapshot{}, errCancelled
	case <-time.After(interval):
	}
	return s.Sample()
}

type statusOptions struct {
	watch    bool
	interval time.Duration
	count    int
}

func newStatusCmd(app *App) *cobra.Command {
	var o statusOptions
	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Live CPU, GPU, memory, disk, network and process dashboard",
		GroupID: "analyze",
		Long: "Show a live, read-only dashboard of CPU (total and per core), memory, GPU, disk and\n" +
			"network activity and the busiest processes. With --json, print one snapshot; add --watch\n" +
			"to stream one JSON object per line (NDJSON).",
		Example: "  " + buildinfo.Name + " status\n" +
			"  " + buildinfo.Name + " status --json\n" +
			"  " + buildinfo.Name + " status --json --watch --interval 5s",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), app, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.watch, "watch", false, "with --json, stream a snapshot every interval (NDJSON)")
	f.DurationVar(&o.interval, "interval", time.Second, "refresh interval")
	f.IntVar(&o.count, "count", 0, "with --watch, stop after this many snapshots (0 = until Ctrl+C)")
	return cmd
}

func runStatus(ctx context.Context, app *App, o statusOptions) error {
	if o.interval < 200*time.Millisecond {
		return withCode(ExitUsage, "--interval must be at least 200ms")
	}
	src := app.monitorSource()
	defer src.Close()
	sampler := &monitor.Sampler{Source: src}
	osName := system.OS().Short()
	if app.Sandbox != "" {
		osName = "Simulated system"
	}

	switch {
	case app.JSON && o.watch:
		enc := json.NewEncoder(app.Out)
		if _, err := sampler.Sample(); err != nil {
			return err
		}
		for i := 0; o.count == 0 || i < o.count; i++ {
			select {
			case <-ctx.Done():
				return nil // stopping a stream with Ctrl+C is the normal end
			case <-time.After(o.interval):
			}
			s, err := sampler.Sample()
			if err != nil {
				return err
			}
			if err := enc.Encode(statusDoc(s)); err != nil {
				return err
			}
		}
		return nil
	case app.JSON:
		s, err := firstSnapshot(ctx, sampler, o.interval)
		if err != nil {
			return err
		}
		return app.printJSON(statusDoc(s))
	case app.interactive():
		if _, err := sampler.Sample(); err != nil {
			return err
		}
		return ui.RunStatus(ui.StatusOptions{OS: osName, Interval: o.interval, Sample: sampler.Sample})
	}
	s, err := firstSnapshot(ctx, sampler, o.interval)
	if err != nil {
		return err
	}
	app.printf("%s\n", ui.RenderStatus(s, ui.StatusView{OS: osName, Width: ui.Width(), Processes: 10}))
	return nil
}

// statusDoc is the oow.status/v1 document.
func statusDoc(s monitor.Snapshot) map[string]any {
	top := s.Processes
	if len(top) > 15 {
		top = top[:15]
	}
	return map[string]any{
		"schema":         "oow.status/v1",
		"time":           s.Time,
		"cpu":            map[string]any{"percent": s.CPU, "cores_percent": s.Cores, "frequency_mhz": s.FreqMHz, "max_frequency_mhz": s.MaxMHz},
		"memory":         map[string]any{"total_bytes": s.MemTotal, "used_bytes": s.MemUsed, "available_bytes": s.MemAvail, "used_percent": s.MemPercent(), "commit_used_bytes": s.CommitUsed, "commit_limit_bytes": s.CommitMax, "cached_bytes": s.Cached},
		"disk":           map[string]any{"read_bytes_per_sec": s.DiskRead, "write_bytes_per_sec": s.DiskWrite, "active_percent": s.DiskActive, "volumes": s.Volumes},
		"network":        map[string]any{"recv_bytes_per_sec": s.NetRecv, "sent_bytes_per_sec": s.NetSent, "interfaces": s.Interfaces},
		"gpus":           s.GPUs,
		"processes":      map[string]any{"count": s.ProcCount, "threads": s.Threads, "handles": s.Handles, "top_by_cpu": top},
		"uptime_seconds": s.Uptime,
	}
}

type processesOptions struct {
	sortBy string
	top    int
	name   string
}

func newProcessesCmd(app *App) *cobra.Command {
	var o processesOptions
	cmd := &cobra.Command{
		Use:     "processes",
		Aliases: []string{"ps"},
		Short:   "List running processes with CPU, memory and disk activity",
		GroupID: "analyze",
		Long:    "Measure running processes over one second and list them. Read-only: nothing is stopped or changed.",
		Example: "  " + buildinfo.Name + " processes\n" +
			"  " + buildinfo.Name + " processes --sort memory --top 10\n" +
			"  " + buildinfo.Name + " processes --name chrome --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProcesses(cmd.Context(), app, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.sortBy, "sort", "cpu", "sort by cpu, memory, io or name")
	f.IntVar(&o.top, "top", 25, "how many processes to list (0 = all)")
	f.StringVar(&o.name, "name", "", "only processes whose name contains this text")
	return cmd
}

func runProcesses(ctx context.Context, app *App, o processesOptions) error {
	switch o.sortBy {
	case "cpu", "memory", "io", "name":
	default:
		return withCode(ExitUsage, "--sort must be cpu, memory, io or name")
	}
	src := app.monitorSource()
	defer src.Close()
	s, err := firstSnapshot(ctx, &monitor.Sampler{Source: src}, time.Second)
	if err != nil {
		return err
	}
	var list []monitor.Process
	q := strings.ToLower(o.name)
	for _, p := range s.Processes {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) {
			list = append(list, p)
		}
	}
	monitor.SortProcesses(list, o.sortBy)
	total := len(list)
	if o.top > 0 && len(list) > o.top {
		list = list[:o.top]
	}
	if app.JSON {
		if list == nil {
			list = []monitor.Process{}
		}
		return app.printJSON(map[string]any{"schema": "oow.processes/v1", "time": s.Time, "sort": o.sortBy,
			"total": total, "processes": list})
	}
	width := min(ui.Width(), 120)
	nameW := max(16, width-52)
	app.printf("\n %s %s %s %s %s %s\n", ui.Bold.Render(ui.PadRight("NAME", nameW)), ui.Bold.Render(ui.PadLeft("PID", 7)),
		ui.Bold.Render(ui.PadLeft("CPU", 7)), ui.Bold.Render(ui.PadLeft("MEMORY", 10)), ui.Bold.Render(ui.PadLeft("I/O", 11)),
		ui.Bold.Render(ui.PadLeft("THREADS", 8)))
	for _, p := range list {
		app.printf(" %s %s %s %s %s %s\n", ui.PadRight(ui.TruncateMiddle(p.Name, nameW), nameW), ui.Muted.Render(ui.PadLeft(fmt.Sprint(p.PID), 7)),
			ui.PadLeft(fmt.Sprintf("%.1f%%", p.CPU), 7), ui.PadLeft(ui.Bytes(int64(p.Memory)), 10),
			ui.PadLeft(ui.Bytes(int64(p.IORate))+"/s", 11), ui.Muted.Render(ui.PadLeft(fmt.Sprint(p.Threads), 8)))
	}
	app.printf("\n %s\n\n", ui.Muted.Render(fmt.Sprintf("%s shown of %d %s sorted by %s %s memory is the private working set",
		ui.Count(len(list)), total, ui.SymDot, o.sortBy, ui.SymDot)))
	return nil
}
