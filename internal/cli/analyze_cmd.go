package cli

import (
	"context"
	"errors"
	"fmt"

	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/analyzer"
	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type analyzeOptions struct {
	large   bool
	minSize string
	top     int
	depth   int
}

func newAnalyzeCmd(app *App) *cobra.Command {
	var o analyzeOptions
	cmd := &cobra.Command{
		Use:     "analyze [path]",
		Aliases: []string{"analyse"},
		Short:   "Explore disk usage interactively and find large files",
		GroupID: "analyze",
		Long: "Measure a drive or folder and explore it: folders by size, drill down, sort, filter and\n" +
			"search, and move what you no longer need to the Recycle Bin. Links and junctions are not\n" +
			"followed. Without a terminal, prints a summary (or JSON with --json).",
		Example: "  " + buildinfo.Name + " analyze\n" +
			"  " + buildinfo.Name + " analyze D:\\\n" +
			"  " + buildinfo.Name + " analyze --large --min-size 1GB\n" +
			"  " + buildinfo.Name + " analyze %USERPROFILE% --json --depth 2",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			return runAnalyze(cmd.Context(), app, o, path)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.large, "large", false, "list the largest files")
	f.StringVar(&o.minSize, "min-size", "", "only files at least this big, e.g. 500MB or 1GB (with --large)")
	f.IntVar(&o.top, "top", 50, "how many large files to list")
	f.IntVar(&o.depth, "depth", 1, "folder levels to include in JSON output")
	return cmd
}

// ParseSize parses sizes such as 1GB, 500MB, 1.5 GB or 4096 (bytes), using
// the same binary units as File Explorer.
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		m      int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.m
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q (use e.g. 500MB or 1GB)", s)
	}
	return int64(v * float64(mult)), nil
}

func (a *App) defaultAnalyzeRoot() string {
	if a.Sandbox != "" {
		return filepath.Join(a.Sandbox, "C")
	}
	return system.SystemDrive()
}

func runAnalyze(ctx context.Context, app *App, o analyzeOptions, path string) error {
	minSize, err := ParseSize(o.minSize)
	if err != nil {
		return withCode(ExitUsage, "%v", err)
	}
	if path == "" {
		path = app.defaultAnalyzeRoot()
		if app.interactive() && app.Sandbox == "" {
			chosen, err := pickDrive(app)
			if err != nil || chosen == "" {
				return err
			}
			path = chosen
		}
	}
	// Expand Windows-style %VARIABLES% (PowerShell passes them literally).
	// Unix-style $VARS are not expanded: "$" is common in Windows paths.
	if x, err := registry.ExpandString(path); err == nil {
		path = x
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return withCode(ExitUsage, "%v", err)
	}

	var prog analyzer.Progress
	spin := ui.StartSpinner(app.Err, app.tty(), func() string {
		return fmt.Sprintf("Scanning %s %s %s files %s %s", abs, ui.SymDot, ui.Count(int(prog.Files.Load())),
			ui.SymDot, ui.Bytes(prog.Bytes.Load()))
	})
	top := o.top
	if top < 200 {
		top = 200 // keep enough for the interactive view
	}
	res, err := analyzer.Scan(ctx, abs, analyzer.Options{TopFiles: top, MinFileSize: minSize}, &prog)
	spin.Stop()
	if err != nil {
		return withCode(ExitError, "cannot analyze %s: %v", abs, err)
	}
	if res.Cancelled {
		return errCancelled
	}
	var diskTotal int64
	if d, err := system.DiskUsage(filesystem.VolumeOf(abs)); err == nil {
		diskTotal = int64(d.Total)
	}
	largest := res.Largest
	if len(largest) > o.top && o.top > 0 {
		largest = largest[:o.top]
	}

	switch {
	case app.JSON && o.large:
		return app.printJSON(map[string]any{"schema": "oow.large/v1", "root": abs, "min_size": minSize,
			"files": nonNilFiles(largest), "scan_errors": res.Errors, "duration_ms": res.Duration.Milliseconds()})
	case app.JSON:
		res.Largest = largest
		return app.printJSON(analyzeJSON(res, diskTotal, o.depth))
	case app.interactive():
		return ui.RunExplorer(ui.ExplorerOptions{
			Title:      "Analyze",
			Root:       res.Root,
			Largest:    res.Largest,
			DiskTotal:  diskTotal,
			StartLarge: o.large,
			Notes:      scanNotes(res),
			Recycle:    func(paths []string) []error { return app.recyclePaths(paths) },
			Reveal:     revealInExplorer,
			Rescan: func(p string) (*analyzer.Node, error) {
				r, err := analyzer.Scan(ctx, p, analyzer.Options{TopFiles: 1}, nil)
				if err != nil {
					return nil, err
				}
				return r.Root, nil
			},
		})
	}
	printAnalyzeSummary(app, res, diskTotal, largest, o.large)
	return nil
}

func scanNotes(res *analyzer.Result) []string {
	var notes []string
	if res.Errors > 0 {
		notes = append(notes, fmt.Sprintf("%s could not be read (permission denied); run elevated to include them.",
			ui.Plural(int(res.Errors), "folder", "folders")))
	}
	if res.Links > 0 {
		notes = append(notes, fmt.Sprintf("%s not followed.", ui.Plural(int(res.Links), "link or junction", "links and junctions")))
	}
	return notes
}

func pickDrive(app *App) (string, error) {
	var items []ui.PickItem
	var roots []string
	for _, d := range app.Locations.FixedDrives {
		du, err := system.DiskUsage(d)
		if err != nil {
			continue
		}
		items = append(items, ui.PickItem{
			Title:    d,
			Subtitle: fmt.Sprintf("%s used of %s (%.0f%%)", ui.Bytes(int64(du.Used)), ui.Bytes(int64(du.Total)), du.UsedPercent),
			Right:    ui.Bytes(int64(du.Free)) + " free",
			Size:     int64(du.Used),
		})
		roots = append(roots, d)
	}
	if len(items) <= 1 {
		if len(roots) == 1 {
			return roots[0], nil
		}
		return app.defaultAnalyzeRoot(), nil
	}
	res, err := ui.RunPicker(ui.PickerOptions{Title: "Choose a drive to analyze", ConfirmVerb: "analyze", Noun: "drive"}, items)
	if err != nil || !res.Confirmed {
		return "", err
	}
	return roots[res.Selected[0]], nil
}

// recyclePaths moves user-chosen paths to the Recycle Bin. Each must pass the
// guard for explicit user selection (never system trees, critical,
// sensitive or protected locations) and the verified recycle sink.
func (a *App) recyclePaths(paths []string) []error {
	errs := make([]error, len(paths))
	var moved int
	var bytes int64
	for i, p := range paths {
		d := a.Guard.Check(safety.Request{Path: p, Purpose: safety.PurposeUserSelected})
		if !d.Allowed {
			errs[i] = errors.New(d.Reason)
			continue
		}
		e, err := filesystem.Lstat(p)
		if err != nil {
			errs[i] = err
			continue
		}
		var size int64
		if e.IsDir() {
			size, _, _ = filesystem.SizeOf(context.Background(), p)
		} else {
			size = e.Size()
		}
		check := func(final string) error {
			if d := a.Guard.Check(safety.Request{Path: final, Purpose: safety.PurposeUserSelected}); !d.Allowed {
				return errors.New(d.Reason)
			}
			return nil
		}
		if err := filesystem.RecycleVerified(p, e.Fingerprint, check, a.recycler()); err != nil {
			errs[i] = err
			continue
		}
		moved++
		bytes += size
	}
	if moved > 0 {
		a.record(history.Record{Time: time.Now(), Command: "analyze", Sandbox: a.Sandbox != "",
			Removed: moved, Recycled: bytes, Skipped: len(paths) - moved})
	}
	return errs
}

func revealInExplorer(path string) {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	_ = cmd.Start()
}

func printAnalyzeSummary(app *App, res *analyzer.Result, diskTotal int64, largest []analyzer.File, largeOnly bool) {
	width := min(ui.Width(), 110)
	r := res.Root
	app.header("Analyze "+r.Path, false)
	stats := fmt.Sprintf("%s in %s and %s", ui.Bytes(r.Size), ui.Plural(int(r.Files), "file", "files"),
		ui.Plural(int(r.Dirs()), "folder", "folders"))
	if diskTotal > 0 {
		stats += fmt.Sprintf(" · %.1f%% of the disk", float64(r.Size)/float64(diskTotal)*100)
	}
	app.printf(" %s\n", ui.Bold.Render(stats))
	for _, n := range scanNotes(res) {
		app.printf(" %s\n", ui.Muted.Render(n))
	}
	app.println()
	if !largeOnly {
		nameW := max(20, width-46)
		for i, c := range r.SortedChildren() {
			if i == 20 {
				app.printf("   %s\n", ui.Muted.Render(fmt.Sprintf("… %d more", len(r.Children)-20)))
				break
			}
			pct := 0.0
			if r.Size > 0 {
				pct = float64(c.Size) / float64(r.Size) * 100
			}
			name := c.Name
			if c.Reparse {
				name += " (link)"
			}
			app.printf("   %s %s %s %s\n", ui.PadRight(ui.TruncateMiddle(name, nameW), nameW), ui.PadLeft(ui.Bytes(c.Size), 9),
				ui.Bar(pct, 14), ui.PadLeft(fmt.Sprintf("%.1f%%", pct), 6))
		}
		if r.Own > 0 {
			app.printf("   %s %s\n", ui.PadRight(ui.Muted.Render("(files directly here)"), nameW), ui.PadLeft(ui.Bytes(r.Own), 9))
		}
		app.println()
		if len(largest) > 10 {
			largest = largest[:10]
		}
	}
	if len(largest) > 0 {
		app.printf(" %s\n", ui.Bold.Render("Largest files"))
		for _, f := range largest {
			app.printf("   %s  %s\n", ui.PadLeft(ui.Bytes(f.Size), 9), ui.TruncateMiddle(f.Path, width-16))
		}
		app.println()
	}
	app.printf(" %s\n\n", ui.Muted.Render("Scanned in "+ui.Duration(res.Duration)+". Run in a terminal to explore interactively."))
}

type analyzeNodeJSON struct {
	Name     string            `json:"name"`
	Path     string            `json:"path"`
	Size     int64             `json:"size"`
	Files    int64             `json:"files"`
	Percent  float64           `json:"percent_of_parent"`
	Link     bool              `json:"link,omitempty"`
	Error    string            `json:"error,omitempty"`
	Children []analyzeNodeJSON `json:"children,omitempty"`
}

func analyzeJSON(res *analyzer.Result, diskTotal int64, depth int) map[string]any {
	var conv func(n *analyzer.Node, parent int64, level int) analyzeNodeJSON
	conv = func(n *analyzer.Node, parent int64, level int) analyzeNodeJSON {
		j := analyzeNodeJSON{Name: n.Name, Path: n.Path, Size: n.Size, Files: n.Files, Link: n.Reparse}
		if parent > 0 {
			j.Percent = float64(n.Size) / float64(parent) * 100
		}
		if n.Err != nil {
			j.Error = n.Err.Error()
		}
		if level < depth {
			for _, c := range n.SortedChildren() {
				j.Children = append(j.Children, conv(c, n.Size, level+1))
			}
		}
		return j
	}
	status := "complete"
	if res.Errors > 0 {
		status = "partial"
	}
	return map[string]any{
		"schema":      "oow.analyze/v1",
		"root":        conv(res.Root, 0, 0),
		"disk_total":  diskTotal,
		"scan_status": status,
		"scan_errors": res.Errors,
		"links":       res.Links,
		"duration_ms": res.Duration.Milliseconds(),
		"largest":     nonNilFiles(res.Largest),
	}
}

func nonNilFiles(f []analyzer.File) []analyzer.File {
	if f == nil {
		return []analyzer.File{}
	}
	return f
}
