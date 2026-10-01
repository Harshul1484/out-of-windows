package safety

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Class describes what kind of location a path lives in.
type Class int

const (
	ClassOrdinary    Class = iota // nothing special known about it
	ClassUserContent              // Documents, Desktop, Pictures, OneDrive, ...
	ClassSystem                   // Windows, Program Files, ProgramData, ...
	ClassCritical                 // a location that must never be removed itself
	ClassProtected                // protected by the user's whitelist or owned by this tool
)

func (c Class) String() string {
	switch c {
	case ClassUserContent:
		return "user-content"
	case ClassSystem:
		return "system"
	case ClassCritical:
		return "critical"
	case ClassProtected:
		return "protected"
	}
	return "ordinary"
}

// Purpose says why a deletion is being requested. Rule-driven cleanup is held
// to a much stricter standard than a path the user picked by hand.
type Purpose int

const (
	// PurposeCleanup is automatic, rule-driven cleanup of caches and
	// temporary data. It may never touch user content, and may touch
	// system-owned trees only inside an explicit exemption.
	PurposeCleanup Purpose = iota
	// PurposeUserSelected is a path the user chose explicitly (for example in
	// the disk analyzer). It may touch user content but never system trees.
	PurposeUserSelected
)

// Request asks whether one path may be deleted.
type Request struct {
	Path    string
	Purpose Purpose
	// Scope, when set, is a root returned by ValidateRoot. The path must lie
	// strictly inside it.
	Scope string
}

// Decision is the guard's answer. Reason is user-facing when not allowed.
type Decision struct {
	Allowed bool
	Class   Class
	Reason  string
}

// Root validation errors.
var (
	ErrProtectedRoot = errors.New("cleanup root is not allowed")
)

type location struct {
	path  string // normalized
	label string
	// contentsCleanable marks a critical location whose contents (never the
	// location itself) may be cleaned by a rule, e.g. the Temp directories.
	contentsCleanable bool
}

// Guard classifies paths and decides whether they may be deleted. A Guard is
// immutable after construction and safe for concurrent use.
type Guard struct {
	critical    []location
	system      []location
	userContent []location
	exemptions  []location // system subtrees that rules may clean inside
	protected   []location // whitelist + this tool's own directories
}

// NewGuard builds a guard from discovered locations plus a hard-coded
// baseline that does not depend on discovery succeeding. userProtected are
// whitelisted paths from the user's configuration.
func NewGuard(locs Locations, userProtected []string) *Guard {
	g := &Guard{}

	sysDrive := "C:\\"
	if n, err := Normalize(locs.SystemDrive); err == nil && IsVolumeRoot(n) && !IsUNC(n) {
		sysDrive = n
	}
	drives := map[string]bool{sysDrive: true}
	for _, d := range locs.FixedDrives {
		if n, err := Normalize(d); err == nil && IsVolumeRoot(n) && !IsUNC(n) {
			drives[n] = true
		}
	}

	// System-owned trees. Discovered values first, then literal fallbacks on
	// the system drive in case discovery returned something unexpected.
	addSystem := func(p, label string) {
		g.system = appendLoc(g.system, p, label, false)
		g.critical = appendLoc(g.critical, p, label, false)
	}
	addSystem(locs.Windows, "Windows directory")
	addSystem(locs.ProgramFiles, "Program Files")
	addSystem(locs.ProgramFilesX86, "Program Files (x86)")
	addSystem(locs.ProgramData, "ProgramData")
	for _, p := range locs.CommonFilesExtra {
		addSystem(p, "Common Files")
	}
	for _, rel := range []string{
		"Windows", "Program Files", "Program Files (x86)", "ProgramData",
		"Windows.old", "PerfLogs", "Config.Msi", "MSOCache", "Boot", "EFI",
	} {
		addSystem(sysDrive+rel, rel)
	}
	for d := range drives {
		for _, rel := range []string{
			"System Volume Information", "$Recycle.Bin", "Recovery",
			"$WinREAgent", "$Windows.~BT", "$Windows.~WS", "$SysReset",
			"pagefile.sys", "hiberfil.sys", "swapfile.sys", "DumpStack.log.tmp",
			"bootmgr", "BOOTNXT",
		} {
			addSystem(d+rel, rel)
		}
	}

	// Profile and AppData roots: never removable themselves, and no cleanup
	// rule may be rooted at (or above) them.
	g.critical = appendLoc(g.critical, locs.UsersRoot, "Users directory", false)
	g.critical = appendLoc(g.critical, sysDrive+"Users", "Users directory", false)
	g.critical = appendLoc(g.critical, locs.PublicProfile, "Public profile", false)
	g.critical = appendLoc(g.critical, locs.UserProfile, "user profile", false)
	if locs.UserProfile != "" {
		g.critical = appendLoc(g.critical, locs.UserProfile+`\AppData`, "AppData", false)
	}
	g.critical = appendLoc(g.critical, locs.RoamingAppData, "Roaming AppData", false)
	g.critical = appendLoc(g.critical, locs.LocalAppData, "Local AppData", false)
	g.critical = appendLoc(g.critical, locs.LocalLow, "LocalLow AppData", false)
	g.critical = appendLoc(g.critical, locs.Temp, "user Temp directory", true)
	g.critical = appendLoc(g.critical, locs.WindowsTemp, "Windows Temp directory", true)
	for _, p := range locs.CriticalExtra {
		g.critical = appendLoc(g.critical, p, "Windows shell folder", false)
	}

	for _, p := range locs.UserContent {
		g.userContent = appendLoc(g.userContent, p, "user files", false)
		g.critical = appendLoc(g.critical, p, "user files folder", false)
	}

	// Narrow, individually reviewed system subtrees that cleanup rules may
	// clean inside. Adding an entry here requires a matching safety review.
	g.exemptions = appendLoc(g.exemptions, locs.WindowsTemp, "Windows Temp directory", false)

	for _, p := range locs.SelfDirs {
		g.protected = appendLoc(g.protected, p, "used by "+toolDirLabel, false)
	}
	for _, p := range userProtected {
		g.protected = appendLoc(g.protected, p, "in your whitelist", false)
	}
	return g
}

const toolDirLabel = "this tool"

func appendLoc(list []location, p, label string, cleanable bool) []location {
	if strings.TrimSpace(p) == "" {
		return list
	}
	n, err := Normalize(p)
	if err != nil {
		return list
	}
	for i, existing := range list {
		if Key(existing.path) == Key(n) {
			list[i].contentsCleanable = existing.contentsCleanable || cleanable
			return list
		}
	}
	return append(list, location{path: n, label: label, contentsCleanable: cleanable})
}

// Check decides whether req.Path may be deleted. It never returns Allowed for
// a path it cannot parse.
func (g *Guard) Check(req Request) Decision {
	p, err := Normalize(req.Path)
	if err != nil {
		return deny(ClassOrdinary, "unrecognized path (%v)", err)
	}
	if IsUNC(p) {
		return deny(ClassOrdinary, "network paths are never deleted")
	}
	if IsVolumeRoot(p) {
		return deny(ClassCritical, "%s is a drive root", p)
	}
	for _, c := range g.critical {
		if IsWithin(c.path, p) {
			if Key(c.path) == Key(p) {
				return deny(ClassCritical, "%s is the %s", p, c.label)
			}
			return deny(ClassCritical, "%s contains the %s (%s)", p, c.label, c.path)
		}
	}
	for _, w := range g.protected {
		if IsWithin(p, w.path) || IsWithin(w.path, p) {
			return deny(ClassProtected, "protected: %s is %s", w.path, w.label)
		}
	}

	if req.Purpose == PurposeCleanup && strings.TrimSpace(req.Scope) == "" {
		return deny(ClassOrdinary, "automatic cleanup requires a validated cleanup location")
	}
	var scope string
	if req.Scope != "" {
		scope, err = Normalize(req.Scope)
		if err != nil {
			return deny(ClassOrdinary, "invalid cleanup scope (%v)", err)
		}
		if !IsStrictlyWithin(p, scope) {
			return deny(ClassOrdinary, "%s is outside the cleanup location %s", p, scope)
		}
	}

	if s, ok := g.containing(g.system, p); ok {
		if req.Purpose == PurposeCleanup && scope != "" && g.isExempt(scope) {
			return Decision{Allowed: true, Class: ClassSystem}
		}
		return deny(ClassSystem, "%s is inside the %s", p, s.label)
	}
	if u, ok := g.containing(g.userContent, p); ok {
		if req.Purpose == PurposeCleanup {
			return deny(ClassUserContent, "%s is inside your %s (%s)", p, u.label, u.path)
		}
		return Decision{Allowed: true, Class: ClassUserContent}
	}
	return Decision{Allowed: true, Class: ClassOrdinary}
}

// ValidateRoot checks that root may serve as the scope of a cleanup rule and
// returns its normalized form. A valid root is never a drive root, never an
// ancestor of a protected location, never a protected location itself (except
// for containers like Temp whose contents are cleanable), never inside user
// content, and inside a system tree only within an explicit exemption.
func (g *Guard) ValidateRoot(root string) (string, error) {
	r, err := Normalize(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrProtectedRoot, err)
	}
	if IsUNC(r) {
		return "", fmt.Errorf("%w: %s is a network path", ErrProtectedRoot, r)
	}
	if IsVolumeRoot(r) {
		return "", fmt.Errorf("%w: %s is a drive root", ErrProtectedRoot, r)
	}
	for _, list := range [][]location{g.critical, g.system, g.userContent, g.protected} {
		for _, c := range list {
			if IsStrictlyWithin(c.path, r) {
				return "", fmt.Errorf("%w: %s contains the %s (%s)", ErrProtectedRoot, r, c.label, c.path)
			}
		}
	}
	for _, c := range g.critical {
		if Key(c.path) == Key(r) && !c.contentsCleanable {
			return "", fmt.Errorf("%w: %s is the %s", ErrProtectedRoot, r, c.label)
		}
	}
	for _, w := range g.protected {
		if IsWithin(r, w.path) {
			return "", fmt.Errorf("%w: %s is %s", ErrProtectedRoot, w.path, w.label)
		}
	}
	if u, ok := g.containing(g.userContent, r); ok {
		return "", fmt.Errorf("%w: %s is inside your %s", ErrProtectedRoot, r, u.label)
	}
	if s, ok := g.containing(g.system, r); ok && !g.isExempt(r) {
		return "", fmt.Errorf("%w: %s is inside the %s", ErrProtectedRoot, r, s.label)
	}
	return r, nil
}

// Classify reports the class of a path without making a deletion decision.
func (g *Guard) Classify(p string) Class {
	n, err := Normalize(p)
	if err != nil {
		return ClassOrdinary
	}
	if IsVolumeRoot(n) {
		return ClassCritical
	}
	for _, c := range g.critical {
		if Key(c.path) == Key(n) {
			return ClassCritical
		}
	}
	if _, ok := g.containing(g.protected, n); ok {
		return ClassProtected
	}
	if _, ok := g.containing(g.system, n); ok {
		return ClassSystem
	}
	if _, ok := g.containing(g.userContent, n); ok {
		return ClassUserContent
	}
	return ClassOrdinary
}

// ProtectedLocation is a location the guard protects, for display.
type ProtectedLocation struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// ProtectedLocations lists everything the guard protects, sorted by path.
func (g *Guard) ProtectedLocations() []ProtectedLocation {
	seen := map[string]bool{}
	var out []ProtectedLocation
	add := func(list []location, kind string) {
		for _, l := range list {
			k := Key(l.path) + "|" + kind
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, ProtectedLocation{Path: l.path, Label: l.label, Kind: kind})
		}
	}
	add(g.system, "system-tree")
	add(g.userContent, "user-content")
	add(g.protected, "protected")
	add(g.critical, "never-remove")
	sort.Slice(out, func(i, j int) bool {
		if Key(out[i].Path) != Key(out[j].Path) {
			return Key(out[i].Path) < Key(out[j].Path)
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func (g *Guard) isExempt(scope string) bool {
	for _, e := range g.exemptions {
		if IsWithin(scope, e.path) {
			return true
		}
	}
	return false
}

func (g *Guard) containing(list []location, p string) (location, bool) {
	for _, l := range list {
		if IsWithin(p, l.path) {
			return l, true
		}
	}
	return location{}, false
}

func deny(c Class, format string, args ...any) Decision {
	return Decision{Allowed: false, Class: c, Reason: fmt.Sprintf(format, args...)}
}
