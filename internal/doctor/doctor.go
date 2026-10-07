// Package doctor diagnoses common Windows problems from facts gathered
// read-only. It never changes anything: each check reports a status, a
// one-line explanation and, for problems, a concrete next step.
package doctor

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/startup"
)

// Status is the outcome of one check.
type Status string

// Statuses. Warning and Problem count as issues; Info and Unknown do not.
const (
	OK      Status = "ok"
	Warning Status = "warning"
	Problem Status = "problem"
	Info    Status = "info"
	Unknown Status = "unknown"
)

// Check is one diagnosis.
type Check struct {
	ID       string         `json:"id"`
	Category string         `json:"category"`
	Title    string         `json:"title"`
	Status   Status         `json:"status"`
	Summary  string         `json:"summary"`
	Details  []string       `json:"details"`
	Next     string         `json:"next_step,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// Categories in display order.
var Categories = []string{"storage", "windows", "environment", "startup", "network", "permissions", "tools"}

// CategoryTitle is the heading of a category.
func CategoryTitle(c string) string {
	switch c {
	case "storage":
		return "Storage"
	case "windows":
		return "Windows"
	case "environment":
		return "PATH"
	case "startup":
		return "Startup"
	case "network":
		return "Network"
	case "permissions":
		return "Permissions"
	case "tools":
		return "Tools"
	}
	return c
}

// Drive is a fixed drive's capacity.
type Drive struct {
	Root  string `json:"root"`
	Total uint64 `json:"total_bytes"`
	Free  uint64 `json:"free_bytes"`
}

// Reboot holds the pending-restart signals Windows records.
type Reboot struct {
	// ComponentServicing: Component Based Servicing\RebootPending exists.
	ComponentServicing bool `json:"component_servicing"`
	// WindowsUpdate: WindowsUpdate\Auto Update\RebootRequired exists.
	WindowsUpdate bool `json:"windows_update"`
	// FileRenames: Session Manager\PendingFileRenameOperations is set
	// (files that are replaced at the next start, often by installers).
	FileRenames bool `json:"file_renames"`
	// Unreadable lists signals that could not be read.
	Unreadable []string `json:"unreadable,omitempty"`
}

// Update holds Windows Update settings that can be read reliably.
type Update struct {
	// ServiceStart is the wuauserv start type (2 automatic, 3 manual,
	// 4 disabled), or -1 when unknown.
	ServiceStart int `json:"service_start"`
	// NoAutoUpdate: Group Policy turns automatic updates off.
	NoAutoUpdate bool `json:"no_auto_update"`
	// Managed: updates come from an organization's server (WSUS policy).
	Managed bool `json:"managed"`
	// PausedUntil is set while updates are paused in Settings.
	PausedUntil time.Time `json:"paused_until,omitzero"`
}

// Adapter is a network adapter.
type Adapter struct {
	Name      string   `json:"name"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"`
	Gateways  []string `json:"gateways"`
	DNS       []string `json:"dns_servers"`
}

// Dir is a folder whose writability is checked.
type Dir struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	// Err is why files cannot be created there; "" when they can.
	Err string `json:"error,omitempty"`
}

// Reclaimable summarizes what `oow clean` would remove by default.
type Reclaimable struct {
	Bytes   int64 `json:"bytes"`
	Items   int   `json:"items"`
	Targets int   `json:"targets"`
	Partial bool  `json:"partial"`
}

// Facts is everything the checks look at. A nil or error field becomes an
// "unknown" check, never a guess.
type Facts struct {
	Drives    []Drive
	DrivesErr error

	Reboot    *Reboot
	RebootErr error

	Update    *Update
	UpdateErr error

	UserPath, MachinePath *envpath.Report

	Startup    []startup.Entry
	StartupErr error

	PackageManagers map[string]string

	Reclaimable *Reclaimable

	Adapters    []Adapter
	AdaptersErr error

	Dirs []Dir
}

// Probe gathers system facts. Every method is read-only.
type Probe interface {
	Drives() ([]Drive, error)
	Reboot() (Reboot, error)
	Update() (Update, error)
	Adapters() ([]Adapter, error)
	PackageManagers() map[string]string
	// Writable returns nil when files can be created in dir.
	Writable(dir string) error
}

// Collect reads the facts a Probe provides. PATH, startup entries and
// reclaimable space come from their own packages and are added by the caller.
func Collect(p Probe, dirs []Dir) Facts {
	var f Facts
	f.Drives, f.DrivesErr = p.Drives()
	if r, err := p.Reboot(); err == nil {
		f.Reboot = &r
	} else {
		f.RebootErr = err
	}
	if u, err := p.Update(); err == nil {
		f.Update = &u
	} else {
		f.UpdateErr = err
	}
	f.Adapters, f.AdaptersErr = p.Adapters()
	f.PackageManagers = p.PackageManagers()
	for _, d := range dirs {
		if err := p.Writable(d.Path); err != nil {
			d.Err = err.Error()
		}
		f.Dirs = append(f.Dirs, d)
	}
	return f
}

// Thresholds for free space.
const (
	gib             = 1 << 30
	diskProblemFree = 2 * gib
	diskWarnFree    = 10 * gib
	diskWarnPercent = 10
)

// Evaluate turns facts into checks, in display order.
func Evaluate(f Facts) []Check {
	var out []Check
	out = append(out, diskChecks(f)...)
	out = append(out, rebootCheck(f), updateCheck(f))
	if f.UserPath != nil {
		out = append(out, pathCheck(f.UserPath, f.MachinePath))
	}
	if f.MachinePath != nil {
		out = append(out, pathCheck(f.MachinePath, f.UserPath))
	}
	out = append(out, startupCheck(f))
	if f.Reclaimable != nil {
		out = append(out, reclaimCheck(f))
	}
	out = append(out, networkCheck(f))
	out = append(out, dirChecks(f)...)
	out = append(out, packageManagerCheck(f))
	order := map[string]int{}
	for i, c := range Categories {
		order[c] = i
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Category] < order[out[j].Category] })
	return out
}

// Summary counts checks by status.
type Summary struct {
	Checks   int `json:"checks"`
	Issues   int `json:"issues"`
	Problems int `json:"problems"`
	Warnings int `json:"warnings"`
	OK       int `json:"ok"`
	Info     int `json:"info"`
	Unknown  int `json:"unknown"`
}

// Summarize counts checks.
func Summarize(cs []Check) Summary {
	s := Summary{Checks: len(cs)}
	for _, c := range cs {
		switch c.Status {
		case Problem:
			s.Problems++
		case Warning:
			s.Warnings++
		case OK:
			s.OK++
		case Info:
			s.Info++
		default:
			s.Unknown++
		}
	}
	s.Issues = s.Problems + s.Warnings
	return s
}

func cmd(args string) string { return "`" + buildinfo.Name + " " + args + "`" }

func bytesGB(n uint64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(n)/(1<<40))
	case n >= gib:
		return fmt.Sprintf("%.1f GB", float64(n)/gib)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}

func diskChecks(f Facts) []Check {
	if f.DrivesErr != nil || len(f.Drives) == 0 {
		why := "no fixed drives were found"
		if f.DrivesErr != nil {
			why = f.DrivesErr.Error()
		}
		return []Check{{ID: "disk.free", Category: "storage", Title: "Free space", Status: Unknown,
			Summary: "Free space could not be read: " + why}}
	}
	var out []Check
	for _, d := range f.Drives {
		letter := strings.TrimRight(d.Root, `:\`)
		c := Check{ID: "disk.free." + strings.ToLower(letter), Category: "storage", Title: d.Root + " free space",
			Data: map[string]any{"root": d.Root, "free_bytes": d.Free, "total_bytes": d.Total}}
		pct := 0.0
		if d.Total > 0 {
			pct = float64(d.Free) / float64(d.Total) * 100
		}
		c.Summary = fmt.Sprintf("%s free of %s (%.0f%%)", bytesGB(d.Free), bytesGB(d.Total), pct)
		next := "Run " + cmd("clean") + " to remove temporary files and caches, or " + cmd("analyze "+d.Root) +
			" to see what uses the space."
		switch {
		case d.Free < diskProblemFree:
			c.Status, c.Next = Problem, next
			c.Details = []string{"Windows needs free space to install updates and for its page file; programs can fail when a drive is full."}
		case d.Free < diskWarnFree || pct < diskWarnPercent:
			c.Status, c.Next = Warning, next
			c.Details = []string{"Less than 10 GB or 10% free: updates and large downloads may fail soon."}
		default:
			c.Status = OK
		}
		out = append(out, c)
	}
	return out
}

func rebootCheck(f Facts) Check {
	c := Check{ID: "reboot.pending", Category: "windows", Title: "Pending restart"}
	if f.Reboot == nil {
		c.Status, c.Summary = Unknown, "Could not read the restart flags: "+errString(f.RebootErr)
		return c
	}
	r := f.Reboot
	c.Data = map[string]any{"component_servicing": r.ComponentServicing, "windows_update": r.WindowsUpdate, "file_renames": r.FileRenames}
	var why []string
	if r.WindowsUpdate {
		why = append(why, "Windows Update installed updates that finish at the next restart")
	}
	if r.ComponentServicing {
		why = append(why, "Windows components (servicing) are waiting for a restart")
	}
	if r.FileRenames {
		why = append(why, "files are scheduled to be replaced at the next start (often by an installer)")
	}
	switch {
	case r.WindowsUpdate || r.ComponentServicing:
		c.Status, c.Summary = Warning, "A restart is needed to finish installing updates"
		c.Details, c.Next = why, "Restart Windows when convenient; "+buildinfo.Name+" never restarts it for you."
	case r.FileRenames:
		c.Status, c.Summary = Info, "Some files will be replaced at the next restart"
		c.Details = why
	case len(r.Unreadable) > 0:
		c.Status, c.Summary = Unknown, "Some restart flags could not be read: "+strings.Join(r.Unreadable, ", ")
	default:
		c.Status, c.Summary = OK, "No restart is pending"
	}
	return c
}

func updateCheck(f Facts) Check {
	c := Check{ID: "windows-update", Category: "windows", Title: "Windows Update"}
	if f.Update == nil {
		c.Status, c.Summary = Unknown, "Could not read Windows Update settings: "+errString(f.UpdateErr)
		return c
	}
	u := f.Update
	c.Data = map[string]any{"service_start": u.ServiceStart, "no_auto_update": u.NoAutoUpdate, "managed": u.Managed}
	if !u.PausedUntil.IsZero() {
		c.Data["paused_until"] = u.PausedUntil.Format(time.RFC3339)
	}
	c.Details = append(c.Details, "When updates were last installed is not checked: Windows does not record it in a reliable place.")
	switch {
	case u.ServiceStart == 4:
		c.Status, c.Summary = Problem, "The Windows Update service is disabled"
		c.Next = "Set the Windows Update service back to Manual in services.msc, or ask your administrator."
	case u.NoAutoUpdate:
		c.Status, c.Summary = Warning, "Automatic updates are turned off by policy"
		c.Next = "Check for updates in Settings > Windows Update regularly, or ask your administrator."
	case !u.PausedUntil.IsZero() && u.PausedUntil.After(time.Now()):
		c.Status = Info
		c.Summary = "Updates are paused until " + u.PausedUntil.Local().Format("2 Jan 2006")
	case u.ServiceStart == -1:
		c.Status, c.Summary = Unknown, "The Windows Update service state could not be read"
	default:
		c.Status, c.Summary = OK, "The update service is enabled and updates are not paused"
	}
	if u.Managed {
		c.Details = append(c.Details, "Updates are managed by your organization (update server set by policy).")
	}
	return c
}

// Length limits. cmd.exe rejects command lines over 8,191 characters, so
// batch files that extend PATH ("set PATH=%PATH%;...") fail beyond it; setx
// truncates values to 1,024 characters.
const (
	cmdLineLimit = 8191
	setxLimit    = 1024
)

func pathCheck(r, other *envpath.Report) Check {
	scope := "User"
	if r.Scope == envpath.Machine {
		scope = "System"
	}
	c := Check{ID: "path." + string(r.Scope), Category: "environment", Title: scope + " PATH",
		Data: map[string]any{"entries": len(r.Entries), "missing": r.Missing, "duplicates": r.Duplicates,
			"empty": r.Empty, "unknown": r.Unknown, "length": r.Length, "expanded_length": r.ExpandedLength}}
	if r.Error != "" {
		c.Status, c.Summary = Unknown, "Could not be read: "+r.Error
		return c
	}
	if !r.Value.Exists {
		c.Status, c.Summary = OK, "Not set"
		return c
	}
	var parts []string
	if r.Missing > 0 {
		parts = append(parts, plural(r.Missing, "missing folder", "missing folders"))
	}
	if r.Duplicates > 0 {
		parts = append(parts, plural(r.Duplicates, "duplicate", "duplicates"))
	}
	if r.Empty > 0 {
		parts = append(parts, plural(r.Empty, "empty entry", "empty entries"))
	}
	for _, e := range r.Entries {
		switch e.Problem {
		case envpath.ProblemMissing:
			c.Details = append(c.Details, "missing: "+e.Raw)
		case envpath.ProblemDuplicate:
			c.Details = append(c.Details, "duplicate: "+e.Raw)
		}
	}
	combined := r.ExpandedLength
	if other != nil {
		combined = envpath.CombinedLength(*r, *other)
	}
	tooLong := combined > cmdLineLimit
	if tooLong {
		c.Details = append(c.Details, fmt.Sprintf("The combined PATH is %d characters: cmd.exe cannot handle lines over %d, so batch files that extend PATH fail.",
			combined, cmdLineLimit))
	}
	if r.Scope == envpath.User && r.Length > setxLimit {
		c.Details = append(c.Details, fmt.Sprintf("It is %d characters long: do not edit it with setx, which truncates values to %d.", r.Length, setxLimit))
	}
	if r.Unknown > 0 {
		c.Details = append(c.Details, plural(r.Unknown, "entry was", "entries were")+" not checked (network or removable drives, or undefined variables).")
	}
	switch {
	case len(parts) == 0 && !tooLong:
		c.Status, c.Summary = OK, fmt.Sprintf("%s, all present", plural(len(r.Entries), "entry", "entries"))
	case len(parts) == 0:
		c.Status, c.Summary = Warning, "Very long"
		c.Next = "Remove folders you no longer need from PATH."
	default:
		c.Status, c.Summary = Warning, strings.Join(parts, ", ")
		if r.Scope == envpath.User {
			c.Next = "Run " + cmd("repair") + " to remove them (the old value is backed up first)."
		} else {
			c.Next = "Edit the system PATH as administrator (System Properties > Environment Variables); " +
				buildinfo.Name + " repair never changes it."
		}
	}
	return c
}

func startupCheck(f Facts) Check {
	c := Check{ID: "startup.broken", Category: "startup", Title: "Startup programs"}
	if f.StartupErr != nil {
		c.Status, c.Summary = Unknown, "Could not be read: "+f.StartupErr.Error()
		return c
	}
	var broken, enabled int
	for _, e := range f.Startup {
		if e.State == startup.Enabled {
			enabled++
			if e.Broken() {
				broken++
				c.Details = append(c.Details, fmt.Sprintf("%s, missing program: %s", e.Name, e.Target))
			}
		}
	}
	c.Data = map[string]any{"entries": len(f.Startup), "enabled": enabled, "broken": broken}
	if broken > 0 {
		c.Status = Warning
		c.Summary = plural(broken, "entry starts", "entries start") + " a program that no longer exists"
		c.Next = "Run " + cmd("repair") + " or " + cmd("startup") + " to disable them (reversible)."
		return c
	}
	c.Status, c.Summary = OK, fmt.Sprintf("%d enabled, none broken", enabled)
	return c
}

func reclaimCheck(f Facts) Check {
	r := f.Reclaimable
	c := Check{ID: "cleanup.reclaimable", Category: "storage", Title: "Caches and temp files",
		Data: map[string]any{"bytes": r.Bytes, "items": r.Items, "targets": r.Targets}}
	if r.Bytes >= gib {
		c.Status = Info
		c.Summary = fmt.Sprintf("%s can be removed safely", bytesGB(uint64(r.Bytes)))
		c.Next = "Run " + cmd("clean --dry-run") + " to see what, then " + cmd("clean") + "."
	} else {
		c.Status = OK
		c.Summary = fmt.Sprintf("%s reclaimable", bytesGB(uint64(r.Bytes)))
	}
	if r.Partial {
		c.Details = append(c.Details, "The scan was incomplete.")
	}
	return c
}

// isPlaceholderDNS reports the site-local addresses Windows lists when no
// IPv6 DNS server is configured.
func isPlaceholderDNS(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	for _, p := range []string{"fec0:0:0:ffff::1", "fec0:0:0:ffff::2", "fec0:0:0:ffff::3"} {
		if ip.Equal(net.ParseIP(p)) {
			return true
		}
	}
	return false
}

func networkCheck(f Facts) Check {
	c := Check{ID: "network", Category: "network", Title: "Network settings"}
	if f.AdaptersErr != nil {
		c.Status, c.Summary = Unknown, "Could not read the network adapters: "+f.AdaptersErr.Error()
		return c
	}
	c.Details = append(c.Details, "Only the local configuration is checked; nothing is sent over the network.")
	var up, withGateway, withDNS []string
	for _, a := range f.Adapters {
		if !a.Up || len(a.Addresses) == 0 {
			continue
		}
		up = append(up, a.Name)
		var dns []string
		for _, d := range a.DNS {
			if !isPlaceholderDNS(d) {
				dns = append(dns, d)
			}
		}
		if len(a.Gateways) > 0 {
			withGateway = append(withGateway, a.Name)
			if len(dns) > 0 {
				withDNS = append(withDNS, a.Name)
				c.Details = append(c.Details, fmt.Sprintf("%s: gateway %s, DNS %s", a.Name, strings.Join(a.Gateways, ", "),
					strings.Join(dns[:min(len(dns), 2)], ", ")))
			}
		}
	}
	c.Data = map[string]any{"connected": up, "with_gateway": withGateway, "with_dns": withDNS}
	switch {
	case len(up) == 0:
		c.Status, c.Summary = Problem, "No network adapter is connected"
		c.Next = "Check the cable or Wi-Fi connection, and that the adapter is enabled in Settings > Network."
	case len(withGateway) == 0:
		c.Status, c.Summary = Warning, "Connected, but no adapter has a default gateway (local network only)"
		c.Next = "Reconnect to the network or check the adapter's IP settings (Settings > Network)."
	case len(withDNS) == 0:
		c.Status, c.Summary = Warning, "No DNS server is configured, so names cannot be resolved"
		c.Next = "Set DNS to automatic in the adapter's settings (Settings > Network)."
	default:
		c.Status, c.Summary = OK, strings.Join(withDNS, ", ")+" connected with a gateway and DNS"
	}
	return c
}

func dirChecks(f Facts) []Check {
	var out []Check
	for _, d := range f.Dirs {
		c := Check{ID: "permissions." + d.ID, Category: "permissions", Title: d.Label, Data: map[string]any{"path": d.Path}}
		if d.Err == "" {
			c.Status, c.Summary = OK, "Writable: "+d.Path
		} else {
			c.Status, c.Summary = Problem, "Files cannot be created in "+d.Path
			c.Details = []string{d.Err}
			c.Next = fmt.Sprintf("Check the folder's permissions: icacls \"%s\"", d.Path)
		}
		out = append(out, c)
	}
	return out
}

func packageManagerCheck(f Facts) Check {
	c := Check{ID: "package-managers", Category: "tools", Title: "Package managers"}
	var have, missing []string
	for _, pm := range []string{"winget", "scoop", "choco"} {
		if p := f.PackageManagers[pm]; p != "" {
			have = append(have, pm)
			c.Details = append(c.Details, pm+": "+p)
		} else {
			missing = append(missing, pm)
		}
	}
	c.Data = map[string]any{"available": nonNil(have), "missing": nonNil(missing)}
	switch {
	case len(have) == 0:
		c.Status, c.Summary = Info, "None found (winget, scoop, choco)"
		c.Next = "winget comes with App Installer from the Microsoft Store."
	case f.PackageManagers["winget"] == "":
		c.Status, c.Summary = Info, strings.Join(have, ", ")+" available; winget not found"
	default:
		c.Status, c.Summary = OK, strings.Join(have, ", ")+" available"
	}
	return c
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func errString(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
