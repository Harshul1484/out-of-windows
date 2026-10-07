package sandbox

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/doctor"
	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/optimize"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/tasks"
)

// The simulated registry and system state used by startup, doctor, optimize
// and repair lives in JSON files under <root>\registry, next to apps.json;
// tasks.json holds the simulated Task Scheduler library.

func stateFile(root, name string) string { return filepath.Join(root, "registry", name) }

func loadState(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func saveState(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// simVars are the environment variables of the simulated system.
func simVars(root string) map[string]string {
	l := Locations(root)
	return map[string]string{
		"systemroot":        l.Windows,
		"windir":            l.Windows,
		"programfiles":      l.ProgramFiles,
		"programfiles(x86)": l.ProgramFilesX86,
		"programdata":       l.ProgramData,
		"userprofile":       l.UserProfile,
		"localappdata":      l.LocalAppData,
		"appdata":           l.RoamingAppData,
		"temp":              l.Temp,
		"tmp":               l.Temp,
	}
}

var envRef = regexp.MustCompile(`%([^%]+)%`)

// Expand expands %VARIABLES% of the simulated system; ok is false when one
// is not defined there.
func Expand(root, s string) (string, bool) {
	vars := simVars(root)
	ok := true
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, found := vars[strings.ToLower(m[1:len(m)-1])]; found {
			return v
		}
		ok = false
		return m
	})
	return out, ok
}

// StartupFolder is the simulated user (or common) Startup folder.
func StartupFolder(root string, common bool) string {
	l := Locations(root)
	if common {
		return filepath.Join(l.ProgramData, "Microsoft", "Windows", "Start Menu", "Programs", "StartUp")
	}
	return filepath.Join(l.RoamingAppData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

// Startup simulates Run/RunOnce registry values and StartupApproved values
// (<root>\registry\startup.json) over real Startup folders in the sandbox.
type Startup struct{ Root string }

type simStartup struct {
	Run []simRun `json:"run"`
	// Approved maps a StartupApproved key to value names and hex data.
	Approved map[string]map[string]string `json:"approved"`
}

type simRun struct {
	Source  startup.Source `json:"source"`
	Name    string         `json:"name"`
	Command string         `json:"command"`
}

func (s Startup) file() string { return stateFile(s.Root, "startup.json") }

func (s Startup) load() (simStartup, error) {
	st := simStartup{Approved: map[string]map[string]string{}}
	err := loadState(s.file(), &st)
	if st.Approved == nil {
		st.Approved = map[string]map[string]string{}
	}
	return st, err
}

// Resolver resolves commands against the simulated system.
func (s Startup) Resolver() startup.Resolver {
	l := Locations(s.Root)
	return startup.Resolver{
		Expand: func(v string) string { x, _ := Expand(s.Root, v); return x },
		Probe:  system.ProbePath,
		Search: []string{filepath.Join(l.Windows, "System32"), l.Windows},
	}
}

func (st simStartup) approval(src startup.Source, name string) []byte {
	key := src.ApprovalKey()
	if key == "" {
		return nil
	}
	h, ok := st.Approved[key][name]
	if !ok {
		return nil
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return []byte{}
	}
	return b
}

// List returns the simulated startup entries.
func (s Startup) List(ctx context.Context) ([]startup.Entry, []string, error) {
	st, err := s.load()
	if err != nil {
		return nil, nil, err
	}
	var raws []startup.Raw
	for _, r := range st.Run {
		raws = append(raws, startup.Raw{Source: r.Source, Name: r.Name, Command: r.Command, Approval: st.approval(r.Source, r.Name)})
	}
	for _, src := range []startup.Source{startup.SourceUserFolder, startup.SourceCommonFolder} {
		dir := StartupFolder(s.Root, src == startup.SourceCommonFolder)
		r, err := startup.ReadFolder(src, dir, nil, func(name string) []byte { return st.approval(src, name) })
		if err != nil {
			return nil, nil, err
		}
		raws = append(raws, r...)
	}
	tr, warnings := startup.TaskRaws(ctx, Tasks{Root: s.Root})
	return startup.Build(append(raws, tr...), s.Resolver()), warnings, nil
}

// SetTaskEnabled switches a simulated scheduled task's Enabled flag.
func (s Startup) SetTaskEnabled(e startup.Entry, enabled bool) error {
	if e.Task == nil {
		return fmt.Errorf("%s is not a scheduled task", e.Name)
	}
	return startup.TaskWriteError(Tasks{Root: s.Root}.SetEnabled(e.Task.Path, enabled))
}

// Tasks simulates the Task Scheduler library (<root>\registry\tasks.json)
// as seen by the simulated user.
type Tasks struct{ Root string }

// TaskAccount is the simulated user tasks are compared with.
var TaskAccount = tasks.Account{SID: "S-1-5-21-1000-2000-3000-1001", Name: UserName, Domain: "SANDBOX"}

func (s Tasks) file() string { return stateFile(s.Root, "tasks.json") }

// List returns the simulated tasks.
func (s Tasks) List(ctx context.Context) ([]tasks.Task, []string, error) {
	var list []tasks.Task
	if err := loadState(s.file(), &list); err != nil {
		return nil, nil, err
	}
	if list == nil {
		list = []tasks.Task{}
	}
	return list, nil, nil
}

// SetEnabled switches a simulated task's Enabled flag; nothing else changes.
func (s Tasks) SetEnabled(path string, enabled bool) error {
	var list []tasks.Task
	if err := loadState(s.file(), &list); err != nil {
		return err
	}
	for i := range list {
		if strings.EqualFold(list[i].Path, path) {
			list[i].Enabled = enabled
			return saveState(s.file(), list)
		}
	}
	return tasks.ErrNotFound
}

// Account returns the simulated user.
func (s Tasks) Account() tasks.Account { return TaskAccount }

// SetApproval writes a simulated StartupApproved value.
func (s Startup) SetApproval(e startup.Entry, data []byte) error {
	key := e.Source.ApprovalKey()
	if key == "" {
		return fmt.Errorf("%s entries cannot be enabled or disabled", e.Source.Label())
	}
	st, err := s.load()
	if err != nil {
		return err
	}
	present := false
	if e.Source.Folder() {
		_, err := os.Lstat(e.Location)
		present = err == nil
	} else {
		for _, r := range st.Run {
			if r.Source == e.Source && r.Name == e.Name {
				present = true
			}
		}
	}
	if !present {
		return startup.ErrEntryGone
	}
	if st.Approved[key] == nil {
		st.Approved[key] = map[string]string{}
	}
	st.Approved[key][e.Name] = hex.EncodeToString(data)
	return saveState(s.file(), st)
}

// Paths simulates the user and machine PATH (<root>\registry\environment.json).
type Paths struct{ Root string }

type simEnvironment struct {
	User       envpath.Value `json:"user"`
	Machine    envpath.Value `json:"machine"`
	Broadcasts int           `json:"broadcasts"`
}

func (p Paths) file() string { return stateFile(p.Root, "environment.json") }

func (p Paths) load() (simEnvironment, error) {
	var e simEnvironment
	return e, loadState(p.file(), &e)
}

// Read returns a simulated PATH.
func (p Paths) Read(scope envpath.Scope) (envpath.Value, error) {
	e, err := p.load()
	if scope == envpath.Machine {
		return e.Machine, err
	}
	return e.User, err
}

// WriteUser replaces the simulated user PATH if it still equals expected.
func (p Paths) WriteUser(expected, updated envpath.Value) error {
	e, err := p.load()
	if err != nil {
		return err
	}
	if e.User != expected {
		return envpath.ErrChanged
	}
	e.User = updated
	return saveState(p.file(), e)
}

// Broadcast counts simulated WM_SETTINGCHANGE broadcasts.
func (p Paths) Broadcast() error {
	e, err := p.load()
	if err != nil {
		return err
	}
	e.Broadcasts++
	return saveState(p.file(), e)
}

// Expand expands variables of the simulated system.
func (p Paths) Expand(s string) (string, bool) { return Expand(p.Root, s) }

// Doctor serves deterministic system facts from <root>\registry\system.json;
// folder permissions are checked for real (read-only) inside the sandbox.
type Doctor struct{ Root string }

type simSystem struct {
	Drives          []doctor.Drive    `json:"drives"`
	Reboot          doctor.Reboot     `json:"reboot"`
	Update          doctor.Update     `json:"update"`
	Adapters        []doctor.Adapter  `json:"adapters"`
	PackageManagers map[string]string `json:"package_managers"`
}

func (d Doctor) load() (simSystem, error) {
	var s simSystem
	return s, loadState(stateFile(d.Root, "system.json"), &s)
}

// Drives returns simulated drives.
func (d Doctor) Drives() ([]doctor.Drive, error) {
	s, err := d.load()
	return s.Drives, err
}

// Reboot returns simulated restart flags.
func (d Doctor) Reboot() (doctor.Reboot, error) {
	s, err := d.load()
	return s.Reboot, err
}

// Update returns simulated Windows Update settings.
func (d Doctor) Update() (doctor.Update, error) {
	s, err := d.load()
	return s.Update, err
}

// Adapters returns simulated network adapters.
func (d Doctor) Adapters() ([]doctor.Adapter, error) {
	s, err := d.load()
	return s.Adapters, err
}

// PackageManagers returns simulated package managers.
func (d Doctor) PackageManagers() map[string]string {
	s, _ := d.load()
	return s.PackageManagers
}

// Writable checks a sandbox folder for real; it creates nothing.
func (d Doctor) Writable(dir string) error { return system.CanCreateIn(dir) }

// Optimizer simulates the maintenance tasks (<root>\registry\optimize.json)
// with a real Delivery Optimization cache folder inside the sandbox.
type Optimizer struct{ Root string }

type simOptimize struct {
	DNSFlushes  int               `json:"dns_flushes"`
	Retrimmed   []string          `json:"retrimmed"`
	SSDs        []optimize.Volume `json:"ssd_volumes"`
	DOAvailable bool              `json:"do_available"`
}

func (o Optimizer) file() string { return stateFile(o.Root, "optimize.json") }

func (o Optimizer) load() (simOptimize, error) {
	var s simOptimize
	return s, loadState(o.file(), &s)
}

// DOCacheDir is the simulated Delivery Optimization cache.
func DOCacheDir(root string) string {
	return filepath.Join(Locations(root).Windows, "ServiceProfiles", "NetworkService", "AppData", "Local",
		"Microsoft", "Windows", "DeliveryOptimization", "Cache")
}

// FlushDNS counts simulated flushes.
func (o Optimizer) FlushDNS(ctx context.Context) error {
	s, err := o.load()
	if err != nil {
		return err
	}
	s.DNSFlushes++
	return saveState(o.file(), s)
}

func (o Optimizer) doItems(ctx context.Context) (files, dirs []filesystem.Entry, err error) {
	err = filesystem.Walk(ctx, DOCacheDir(o.Root), func(e filesystem.Entry) bool {
		if e.Reparse {
			return false
		}
		if e.IsDir() {
			dirs = append(dirs, e)
			return true
		}
		files = append(files, e)
		return false
	}, nil)
	if errors.Is(err, filesystem.ErrGone) {
		err = nil
	}
	return files, dirs, err
}

// DeliveryOptimization measures the simulated cache.
func (o Optimizer) DeliveryOptimization(ctx context.Context, elevated bool) optimize.DOCache {
	s, _ := o.load()
	c := optimize.DOCache{Available: s.DOAvailable, Bytes: -1, Path: DOCacheDir(o.Root)}
	if !elevated {
		return c
	}
	files, _, err := o.doItems(ctx)
	if err != nil {
		return c
	}
	c.Bytes = 0
	for _, f := range files {
		c.Bytes += f.Size()
	}
	return c
}

// ClearDeliveryOptimization empties the simulated cache through the verified
// deletion sink, confined to the cache folder (and the sandbox fence).
func (o Optimizer) ClearDeliveryOptimization(ctx context.Context) error {
	files, dirs, err := o.doItems(ctx)
	if err != nil {
		return err
	}
	root, err := safety.Normalize(DOCacheDir(o.Root))
	if err != nil {
		return err
	}
	inside := func(final string) error {
		if !safety.IsStrictlyWithin(final, root) {
			return fmt.Errorf("%s is outside the simulated cache", final)
		}
		return nil
	}
	for _, f := range files {
		if err := filesystem.RemoveVerified(f.Path, f.Fingerprint, inside); err != nil {
			return err
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].Path) > len(dirs[j].Path) })
	for _, d := range dirs {
		_ = filesystem.RemoveVerified(d.Path, d.Fingerprint, inside)
	}
	return nil
}

// SSDVolumes returns the simulated SSD volumes.
func (o Optimizer) SSDVolumes(ctx context.Context) ([]optimize.Volume, error) {
	s, err := o.load()
	return s.SSDs, err
}

// ReTrim records a simulated retrim.
func (o Optimizer) ReTrim(ctx context.Context, v optimize.Volume) error {
	s, err := o.load()
	if err != nil {
		return err
	}
	s.Retrimmed = append(s.Retrimmed, v.Root)
	return saveState(o.file(), s)
}

// seedTasks is the simulated Task Scheduler library: a sign-in task of the
// simulated user whose program is gone (broken; the user may switch it off),
// a sign-in task that runs as SYSTEM (all users: needs administrator rights),
// and a Windows task that the startup list leaves out.
func seedTasks(l safety.Locations) []tasks.Task {
	logon := []tasks.Trigger{tasks.TriggerLogon}
	return []tasks.Task{
		{Path: `\Tailspin Sync`, Enabled: true, Triggers: logon, UserID: TaskAccount.SID, LogonUser: TaskAccount.Domain + `\` + UserName,
			Actions: []tasks.Action{{Command: `%LOCALAPPDATA%\Programs\Tailspin Sync\tailspin.exe`, Arguments: "--background"}}},
		{Path: `\Wingtip Toys\Wingtip Logon Check`, Enabled: true, Triggers: logon, UserID: "S-1-5-18", HighestPrivileges: true,
			Actions: []tasks.Action{{Command: `%ProgramFiles(x86)%\Wingtip Toys\wingtip.exe`, Arguments: "/check"}}},
		{Path: `\Microsoft\Windows\Shell\FamilySafetyMonitor`, Enabled: true, Triggers: logon, GroupID: "S-1-5-32-545",
			Actions: []tasks.Action{{Command: `%windir%\System32\wpcmon.exe`}}},
	}
}

// seedSystem writes the simulated startup entries, PATH values, system facts
// and maintenance state, with a deterministic set of problems for doctor and
// repair: a full D: drive, a pending restart, missing and duplicate PATH
// entries, and startup entries whose programs are gone.
func seedSystem(root string, l safety.Locations) error {
	const day = 24 * time.Hour
	now := time.Now()
	j := filepath.Join
	pf, local := l.ProgramFiles, l.LocalAppData
	progs := j(local, "Programs")
	userStartup, commonStartup := StartupFolder(root, false), StartupFolder(root, true)

	for _, f := range []struct {
		path string
		size int
	}{
		{j(l.Windows, "System32", "ctfmon.exe"), 20000},
		{j(local, "Microsoft", "WindowsApps", "winget.exe"), 0},
		{j(userStartup, "backup.cmd"), 120},
		{j(userStartup, "desktop.ini"), 80},
		{j(DOCacheDir(root), "a1b2c3", "update.cab"), 3_000_000},
		{j(DOCacheDir(root), "d4e5f6", "store.appx"), 2_000_000},
	} {
		if err := WriteFile(f.path, f.size, now.Add(-10*day)); err != nil {
			return err
		}
	}
	for path, link := range map[string][]byte{
		j(userStartup, "Fabrikam Player.lnk"):         startup.BuildLink(j(progs, "Fabrikam Player", "player.exe"), "--minimized"),
		j(userStartup, "Old Notes.lnk"):               startup.BuildLink(j(pf, "OldEditor", "oldeditor.exe"), ""),
		j(commonStartup, "Contoso Studio Helper.lnk"): startup.BuildLink(j(pf, "Contoso", "Studio", "studio.exe"), "--helper"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, link, 0o644); err != nil {
			return err
		}
	}

	disabledAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	st := simStartup{
		Run: []simRun{
			{startup.SourceHKCURun, "Contoso Agent", `"` + j(pf, "Contoso", "Agent", "agent.exe") + `" --tray`},
			{startup.SourceHKCURun, "Fabrikam Updater", `"` + j(progs, "Fabrikam Player", "updater.exe") + `" /check`},
			{startup.SourceHKCURun, "Old Sync Helper", j(pf, "Northwind", "Sync", "helper.exe") + " -background"},
			{startup.SourceHKCURun, "Text Services", "ctfmon.exe"},
			{startup.SourceHKCURun, "Cloud Drive", `\\fileserver\tools\drive.exe /autostart`},
			{startup.SourceHKCURunOnce, "Setup Cleanup", `"` + j(local, "Temp", "setup-123", "cleanup.exe") + `" /quiet`},
			{startup.SourceHKLMRun, "Wingtip Updater", `"%ProgramFiles(x86)%\Wingtip Toys\wingtip.exe" /background`},
			{startup.SourceHKLMRun32, "Litware Tray", j(pf, "Litware Tool", "litware.exe") + " /tray"},
		},
		Approved: map[string]map[string]string{
			startup.SourceHKCURun.ApprovalKey(): {
				"Contoso Agent":   hex.EncodeToString(startup.ApprovalData(true, now)),
				"Old Sync Helper": hex.EncodeToString(startup.ApprovalData(false, disabledAt)),
			},
			startup.SourceUserFolder.ApprovalKey(): {"backup.cmd": "070000000000000000000000"},
		},
	}
	if err := saveState(stateFile(root, "startup.json"), st); err != nil {
		return err
	}
	if err := saveState(stateFile(root, "tasks.json"), seedTasks(l)); err != nil {
		return err
	}

	env := simEnvironment{
		User: envpath.Value{Exists: true, Expand: true, Raw: strings.Join([]string{
			`%LOCALAPPDATA%\Microsoft\WindowsApps`,
			j(progs, "Fabrikam Player"),
			`%USERPROFILE%\go\bin`,
			j(pf, "OldEditor", "bin"),
			"",
			`%LocalAppData%\Microsoft\WindowsApps\`,
			`%TOOLS_HOME%\bin`,
		}, ";") + ";"},
		Machine: envpath.Value{Exists: true, Expand: true, Raw: strings.Join([]string{
			`%SystemRoot%\system32`,
			`%SystemRoot%`,
			j(pf, "Litware Tool", "bin"),
			`%SYSTEMROOT%\System32`,
		}, ";")},
	}
	if err := saveState(stateFile(root, "environment.json"), env); err != nil {
		return err
	}

	sys := simSystem{
		Drives: []doctor.Drive{
			{Root: `C:\`, Total: 512 << 30, Free: 180 << 30},
			{Root: `D:\`, Total: 256 << 30, Free: 6 << 30},
		},
		Reboot: doctor.Reboot{WindowsUpdate: true, FileRenames: true},
		Update: doctor.Update{ServiceStart: 3},
		Adapters: []doctor.Adapter{
			{Name: "Ethernet", Up: true, Addresses: []string{"192.168.1.20"}, Gateways: []string{"192.168.1.1"},
				DNS: []string{"192.168.1.1", "fec0:0:0:ffff::1"}},
			{Name: "vEthernet (Default Switch)", Up: true, Addresses: []string{"172.20.0.1"}, Gateways: []string{},
				DNS: []string{"fec0:0:0:ffff::1"}},
			{Name: "Wi-Fi", Up: false, Addresses: []string{}, Gateways: []string{}, DNS: []string{}},
		},
		PackageManagers: map[string]string{"winget": j(local, "Microsoft", "WindowsApps", "winget.exe")},
	}
	if err := saveState(stateFile(root, "system.json"), sys); err != nil {
		return err
	}
	return saveState(stateFile(root, "optimize.json"), simOptimize{
		Retrimmed:   []string{},
		SSDs:        []optimize.Volume{{Root: `C:\`, FileSystem: "NTFS"}},
		DOAvailable: true,
	})
}
