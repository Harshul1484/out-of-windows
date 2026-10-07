package safety

import (
	"errors"
	"fmt"
	"path/filepath"
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
	ClassSensitive                // credentials, keys, wallets, VM disks, AI-tool state
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
	case ClassSensitive:
		return "sensitive"
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
	// PurposeLeftover is a folder left behind by an uninstalled app, reviewed
	// by the user. Scope must be one of LeftoverRoots; the folder must be at
	// most three levels below it and outside Windows-owned and shared
	// folders. Leftovers go to the Recycle Bin.
	PurposeLeftover
	// PurposeSelfRemove is the tool's own configuration or data directory,
	// moved to the Recycle Bin when the user removes the tool. Only exactly
	// one of Locations.SelfDirs is allowed (never a parent or a child), and
	// only when it is not critical, whitelisted, sensitive, in a system tree
	// or in user content.
	PurposeSelfRemove
	// PurposePurge is a rebuildable project artifact (node_modules, target,
	// .venv, ...) the user reviewed: deleted permanently when preselected, moved
	// to the Recycle Bin when the user added it from review. Scope must be
	// the artifact folder (see ValidatePurgeArtifact): a known build or
	// dependency folder name inside a project, outside system trees, AppData
	// and tool folders in the profile. The path must be that folder or lie
	// inside it, never inside a .git folder, and never be a sensitive file
	// type except inside an installed package (see IsPurgeSensitive).
	PurposePurge
)

// purgeArtifactNames are the only folder names (patterns) PurposePurge may
// use as a scope. The purge scanner adds per-ecosystem evidence on top.
var purgeArtifactNames = []string{
	"node_modules", ".next", ".nuxt", ".svelte-kit", ".turbo", ".parcel-cache", ".angular",
	"dist", "build", "out", "target", ".gradle", "bin", "obj",
	"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".venv", "venv",
	".dart_tool", "build-*", "cmake-build-*",
}

// purgePackageStores are artifacts whose contents come from a package
// registry. Below their top level, file names such as cacert.pem or key.pem
// are package content (CA bundles, test fixtures), not the user's keys.
var purgePackageStores = []string{"node_modules", ".venv", "venv", ".tox"}

// purgeProfileDeny lists folders directly inside the user profile (patterns,
// one per path component) that hold tool or package-manager state rather
// than projects: dot folders (.vscode\extensions, .cargo, .nuget, ...),
// AppData, Scoop apps, the Go module cache and Conda installations.
var purgeProfileDeny = [][]string{{".*"}, {"appdata"}, {"scoop"}, {"go", "pkg"}, {"anaconda3"}, {"miniconda3"}}

// IsPurgeArtifactName reports whether a folder name may be a purge scope.
func IsPurgeArtifactName(name string) bool {
	return matchAny(purgeArtifactNames, name)
}

// IsPurgePackageStore reports whether an artifact folder name is a package
// store (node_modules, virtual environments).
func IsPurgePackageStore(name string) bool {
	return matchAny(purgePackageStores, name)
}

// IsPurgeSensitive reports whether a file called name, depth levels inside
// an artifact folder called artifact (1 = directly inside), must never be
// purged: any sensitive file type in build output, and in package stores
// any sensitive file type placed directly in the store's top folder.
func IsPurgeSensitive(artifact string, depth int, name string) bool {
	if !IsSensitiveName(name) {
		return false
	}
	return !IsPurgePackageStore(artifact) || depth < 2
}

func matchAny(patterns []string, name string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, lower); ok {
			return true
		}
	}
	return false
}

// LeftoverRoot is a location where applications keep their files.
type LeftoverRoot struct {
	Path string
	Kind string // "program files", "app data", ...
	// Admin is true when changing it normally needs administrator rights.
	Admin bool
}

// leftoverDeny lists first-level folder names (patterns) under each kind of
// leftover root that are never treated as app leftovers: Windows and
// Microsoft components, shared runtimes, package managers and caches.
var leftoverDeny = map[string][]string{
	"program files": {"common files", "windows*", "microsoft*", "internet explorer", "modifiablewindowsapps",
		"reference assemblies", "msbuild", "dotnet", "packagemanagement", "uninstall information",
		"windowsapps", "windows defender*", "windowspowershell", "windows nt", "iis*", "dotnet*"},
	"program data": {"microsoft*", "windows*", "packages", "package cache", "regid.*", "ssh", "usoshared",
		"usoprivate", "chocolatey", "scoop", "docker*", "softwaredistribution", "applicationdata",
		"start menu", "desktop", "documents", "templates", "favorites", "oow"},
	"app data": {"microsoft*", "windows*", "npm", "npm-cache", "nuget", "code", "cursor", "oow"},
	"local app data": {"microsoft*", "windows*", "packages", "temp", "programs", "publishers",
		"connecteddevicesplatform", "d3dscache", "crashdumps", "comms", "placeholdertilelogofolder",
		"virtualstore", "elevateddiagnostics", "pip", "npm-cache", "go-build", "nuget", "yarn",
		"history", "inetcache", "oow", "docker"},
	"local low":     {"microsoft*", "windows*"},
	"user programs": {"common", "microsoft*", "windows*"},
}

// Request asks whether one path may be deleted.
type Request struct {
	Path    string
	Purpose Purpose
	// Scope, when set, is a root returned by ValidateRoot. The path must lie
	// strictly inside it.
	Scope string
	// Dir says the path is a directory, as verified by the caller through a
	// handle. Only PurposePurge uses it: sensitive file-type names apply to
	// files, not to package folders such as node_modules\history.
	Dir bool
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
	self        []location // this tool's own directories only
	whitelist   []location // the user's whitelist only
	sensitive   []location // credentials, keys, wallets, VM and AI-tool state
	leftover    []LeftoverRoot
	windowsDir  string
	appData     []location // AppData roots: application state, never projects
	profile     string     // normalized user profile, "" when unknown
}

// LeftoverRoots returns where app leftovers may be found, most specific
// first (so LocalAppData\Programs wins over LocalAppData).
func (g *Guard) LeftoverRoots() []LeftoverRoot { return append([]LeftoverRoot(nil), g.leftover...) }

// LeftoverRootFor returns the most specific leftover root strictly
// containing p.
func (g *Guard) LeftoverRootFor(p string) (LeftoverRoot, bool) {
	n, err := Normalize(p)
	if err != nil {
		return LeftoverRoot{}, false
	}
	for _, r := range g.leftover {
		if IsStrictlyWithin(n, r.Path) {
			return r, true
		}
	}
	return LeftoverRoot{}, false
}

func (g *Guard) checkLeftover(p, scope string) Decision {
	s, err := Normalize(scope)
	if err != nil || scope == "" {
		return deny(ClassOrdinary, "leftover removal requires a known install or data location")
	}
	var root *LeftoverRoot
	for i := range g.leftover {
		if Key(g.leftover[i].Path) == Key(s) {
			root = &g.leftover[i]
			break
		}
	}
	if root == nil {
		return deny(ClassOrdinary, "%s is not an install or data location", s)
	}
	if !IsStrictlyWithin(p, s) {
		return deny(ClassOrdinary, "%s is outside %s", p, s)
	}
	// A more specific root (LocalAppData\Programs inside LocalAppData) owns it.
	if best, ok := g.LeftoverRootFor(p); ok && Key(best.Path) != Key(s) {
		return deny(ClassOrdinary, "%s belongs to %s", p, best.Path)
	}
	rel := strings.Split(p[len(s):], `\`)
	parts := rel[:0]
	for _, r := range rel {
		if r != "" {
			parts = append(parts, r)
		}
	}
	if len(parts) == 0 || len(parts) > 3 {
		return deny(ClassOrdinary, "%s is not a top-level application folder", p)
	}
	first := strings.ToLower(parts[0])
	for _, pat := range leftoverDeny[root.Kind] {
		if ok, _ := filepath.Match(pat, first); ok {
			return deny(ClassSystem, "%s is inside a Windows or shared folder (%s)", p, parts[0])
		}
	}
	if u, ok := g.containing(g.userContent, p); ok {
		return deny(ClassUserContent, "%s is inside your %s", p, u.label)
	}
	if strings.HasPrefix(Key(p), Key(g.windowsDir)+`\`) || Key(p) == Key(g.windowsDir) {
		return deny(ClassSystem, "%s is inside the Windows directory", p)
	}
	class := ClassOrdinary
	if _, ok := g.containing(g.system, p); ok {
		class = ClassSystem
	}
	return Decision{Allowed: true, Class: class}
}

// ValidatePurgeArtifact checks that path may serve as the scope of
// PurposePurge and returns its normalized form. A valid artifact folder has a
// known build or dependency folder name; its parent (the project) is not a
// drive root and not itself a never-remove location (profile, Documents,
// ...); it neither is nor contains a protected, system or sensitive location;
// and it lies outside system trees, AppData and tool folders in the profile.
// User content (Documents\GitHub, Desktop) is allowed: projects live there.
func (g *Guard) ValidatePurgeArtifact(path string) (string, error) {
	a, err := Normalize(path)
	if err != nil {
		return "", fmt.Errorf("unrecognized path (%v)", err)
	}
	if IsUNC(a) {
		return "", fmt.Errorf("%s is a network path; project folders are purged on local drives only", a)
	}
	if IsVolumeRoot(a) {
		return "", fmt.Errorf("%s is a drive root", a)
	}
	parts := splitNonEmpty(a[3:])
	if !IsPurgeArtifactName(parts[len(parts)-1]) {
		return "", fmt.Errorf("%s is not a known build or dependency folder", a)
	}
	parent := a[:3] + strings.Join(parts[:len(parts)-1], `\`)
	if IsVolumeRoot(parent) {
		return "", fmt.Errorf("%s is at the top of a drive; projects there are not purged", a)
	}
	for _, list := range [][]location{g.critical, g.system, g.protected, g.sensitive} {
		for _, c := range list {
			if IsWithin(c.path, a) {
				return "", fmt.Errorf("%s contains the %s (%s)", a, c.label, c.path)
			}
		}
	}
	for _, c := range g.critical {
		if Key(c.path) == Key(parent) {
			return "", fmt.Errorf("%s is directly inside the %s, which is not a project folder", a, c.label)
		}
	}
	if s, ok := g.containing(g.system, a); ok {
		return "", fmt.Errorf("%s is inside the %s", a, s.label)
	}
	if w, ok := g.containing(g.protected, a); ok {
		return "", fmt.Errorf("protected: %s is %s", w.path, w.label)
	}
	if s, ok := g.containing(g.sensitive, a); ok {
		return "", fmt.Errorf("sensitive: %s holds %s", s.path, s.label)
	}
	if d, ok := g.containing(g.appData, a); ok {
		return "", fmt.Errorf("%s is inside %s, which holds application state, not projects", a, d.label)
	}
	if g.profile != "" && IsStrictlyWithin(a, g.profile) {
		rel := splitNonEmpty(a)[len(splitNonEmpty(g.profile)):]
		for _, pat := range purgeProfileDeny {
			if len(rel) < len(pat) {
				continue
			}
			match := true
			for i, p := range pat {
				if ok, _ := filepath.Match(p, strings.ToLower(rel[i])); !ok {
					match = false
					break
				}
			}
			if match {
				return "", fmt.Errorf("%s is inside %s\\%s, which holds tool or package-manager state, not projects",
					a, g.profile, strings.Join(rel[:len(pat)], `\`))
			}
		}
	}
	return a, nil
}

func (g *Guard) checkPurge(p, scope string, dir bool) Decision {
	if strings.TrimSpace(scope) == "" {
		return deny(ClassOrdinary, "purging requires the build or dependency folder the path belongs to")
	}
	s, err := g.ValidatePurgeArtifact(scope)
	if err != nil {
		return deny(ClassOrdinary, "%v", err)
	}
	if !IsWithin(p, s) {
		return deny(ClassOrdinary, "%s is outside %s", p, s)
	}
	rel := splitNonEmpty(p)[len(splitNonEmpty(s)):]
	for _, r := range rel {
		if strings.EqualFold(r, ".git") {
			return deny(ClassProtected, "%s is inside a Git repository folder", p)
		}
	}
	if !dir && len(rel) > 0 && IsPurgeSensitive(baseName(s), len(rel), rel[len(rel)-1]) {
		return deny(ClassSensitive, "sensitive file type: %s is never purged", rel[len(rel)-1])
	}
	if sys, ok := g.containing(g.system, p); ok {
		return deny(ClassSystem, "%s is inside the %s", p, sys.label)
	}
	if _, ok := g.containing(g.userContent, p); ok {
		return Decision{Allowed: true, Class: ClassUserContent}
	}
	return Decision{Allowed: true, Class: ClassOrdinary}
}

// sensitiveNames are file name patterns that automatic cleanup never removes,
// wherever they are: virtual disks (WSL, Docker, Hyper-V), password databases,
// mail stores, private keys, certificates, wallets and browser credential
// databases. A user may still delete such a file explicitly.
var sensitiveNames = []string{
	"*.vhd", "*.vhdx", "*.avhdx", "*.vmdk", "*.vdi", "*.qcow2",
	"*.kdbx", "*.kdb", "*.1pux", "*.opvault",
	"*.pst", "*.ost",
	"*.pfx", "*.p12", "*.pem", "*.key", "*.ppk", "*.gpg", "*.jks", "*.keystore",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", "*.ovpn",
	"*.wallet", "wallet.dat",
	"login data", "login data for account", "cookies", "web data", "history",
	"bookmarks", "local state", "key4.db", "logins.json", "cert9.db",
	"places.sqlite", "cookies.sqlite", "formhistory.sqlite",
}

// IsSensitiveName reports whether a file name is never removed by automatic
// cleanup.
func IsSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range sensitiveNames {
		if ok, _ := filepath.Match(p, lower); ok {
			return true
		}
	}
	return false
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

	join := func(base string, rel ...string) string {
		if base == "" {
			return ""
		}
		return base + `\` + strings.Join(rel, `\`)
	}

	// Narrow, individually reviewed system subtrees that cleanup rules may
	// clean inside. Adding an entry here requires a matching safety review.
	g.exemptions = appendLoc(g.exemptions, locs.WindowsTemp, "Windows Temp directory", false)
	// Windows Error Reporting copies for all users (Disk Cleanup removes them).
	g.exemptions = appendLoc(g.exemptions, join(locs.ProgramData, "Microsoft", "Windows", "WER", "ReportArchive"), "system error reports", false)
	g.exemptions = appendLoc(g.exemptions, join(locs.ProgramData, "Microsoft", "Windows", "WER", "ReportQueue"), "queued system error reports", false)
	// Kernel minidumps written after a crash (Disk Cleanup removes them).
	g.exemptions = appendLoc(g.exemptions, join(locs.Windows, "Minidump"), "crash minidumps", false)

	// Credentials, keys, wallets, VM disks and AI-tool state: never deleted,
	// whatever the purpose, and no rule may be rooted in or above them.
	for _, s := range []struct{ path, label string }{
		{join(locs.UserProfile, ".ssh"), "SSH keys"},
		{join(locs.UserProfile, ".gnupg"), "GnuPG keys"},
		{join(locs.UserProfile, ".aws"), "AWS credentials"},
		{join(locs.UserProfile, ".azure"), "Azure credentials"},
		{join(locs.UserProfile, ".kube"), "Kubernetes credentials"},
		{join(locs.UserProfile, ".docker"), "Docker credentials"},
		{join(locs.UserProfile, ".config", "gcloud"), "Google Cloud credentials"},
		{join(locs.UserProfile, ".claude"), "Claude Code state"},
		{join(locs.UserProfile, ".codex"), "Codex state"},
		{join(locs.UserProfile, ".cursor"), "Cursor state"},
		{join(locs.UserProfile, ".ollama"), "Ollama models"},
		{join(locs.UserProfile, ".lmstudio"), "LM Studio models"},
		{join(locs.UserProfile, ".cargo", "bin"), "installed Cargo binaries"},
		{join(locs.UserProfile, ".rustup"), "Rust toolchains"},
		{join(locs.RoamingAppData, "Microsoft", "Protect"), "Windows DPAPI master keys"},
		{join(locs.RoamingAppData, "Microsoft", "Credentials"), "Windows Credential Manager"},
		{join(locs.RoamingAppData, "Microsoft", "Crypto"), "Windows key store"},
		{join(locs.RoamingAppData, "Microsoft", "SystemCertificates"), "certificate store"},
		{join(locs.LocalAppData, "Microsoft", "Credentials"), "Windows Credential Manager"},
		{join(locs.LocalAppData, "Microsoft", "Vault"), "Windows Vault"},
		{join(locs.RoamingAppData, "gnupg"), "GnuPG keys"},
		{join(locs.RoamingAppData, "Bitwarden"), "Bitwarden data"},
		{join(locs.LocalAppData, "1Password"), "1Password data"},
		{join(locs.RoamingAppData, "KeePass"), "KeePass data"},
		{join(locs.LocalAppData, "KeePassXC"), "KeePassXC data"},
		{join(locs.RoamingAppData, "Electrum"), "Electrum wallets"},
		{join(locs.RoamingAppData, "Bitcoin"), "Bitcoin wallets"},
		{join(locs.RoamingAppData, "Ethereum"), "Ethereum keystore"},
		{join(locs.RoamingAppData, "Exodus"), "Exodus wallet"},
		{join(locs.RoamingAppData, "Ledger Live"), "Ledger Live data"},
		{join(locs.UserProfile, "OpenVPN"), "OpenVPN profiles"},
		{join(locs.RoamingAppData, "Claude"), "Claude desktop data"},
		{join(locs.LocalAppData, "AnthropicClaude"), "Claude desktop app"},
		{join(locs.RoamingAppData, "Code", "User"), "VS Code settings and state"},
		{join(locs.RoamingAppData, "Cursor", "User"), "Cursor settings and state"},
		{join(locs.LocalAppData, "Docker"), "Docker Desktop data"},
	} {
		g.sensitive = appendLoc(g.sensitive, s.path, s.label, false)
	}

	// Where app leftovers may be removed from, most specific first.
	g.windowsDir = sysDrive + "Windows"
	if n, err := Normalize(locs.Windows); err == nil && locs.Windows != "" {
		g.windowsDir = n
	}
	for _, r := range []LeftoverRoot{
		{join(locs.LocalAppData, "Programs"), "user programs", false},
		{locs.ProgramFiles, "program files", true},
		{locs.ProgramFilesX86, "program files", true},
		{locs.ProgramData, "program data", true},
		{locs.RoamingAppData, "app data", false},
		{locs.LocalLow, "local low", false},
		{locs.LocalAppData, "local app data", false},
	} {
		if n, err := Normalize(r.Path); err == nil && r.Path != "" && !IsVolumeRoot(n) {
			dup := false
			for _, e := range g.leftover {
				dup = dup || Key(e.Path) == Key(n)
			}
			if !dup {
				r.Path = n
				g.leftover = append(g.leftover, r)
			}
		}
	}

	if locs.UserProfile != "" {
		g.appData = appendLoc(g.appData, locs.UserProfile+`\AppData`, "AppData", false)
		if n, err := Normalize(locs.UserProfile); err == nil && !IsVolumeRoot(n) {
			g.profile = n
		}
	}
	g.appData = appendLoc(g.appData, locs.RoamingAppData, "Roaming AppData", false)
	g.appData = appendLoc(g.appData, locs.LocalAppData, "Local AppData", false)
	g.appData = appendLoc(g.appData, locs.LocalLow, "LocalLow AppData", false)

	for _, p := range locs.SelfDirs {
		g.protected = appendLoc(g.protected, p, "used by "+toolDirLabel, false)
		g.self = appendLoc(g.self, p, "used by "+toolDirLabel, false)
	}
	for _, p := range userProtected {
		g.protected = appendLoc(g.protected, p, "in your whitelist", false)
		g.whitelist = appendLoc(g.whitelist, p, "in your whitelist", false)
	}
	return g
}

// checkSelfRemove allows exactly one of the tool's own directories and
// nothing else. Critical locations were already refused by Check.
func (g *Guard) checkSelfRemove(p string) Decision {
	own := false
	for _, s := range g.self {
		own = own || Key(s.path) == Key(p)
	}
	if !own {
		return deny(ClassOrdinary, "%s is not one of %s's own folders", p, toolDirLabel)
	}
	for _, w := range g.whitelist {
		if IsWithin(p, w.path) || IsWithin(w.path, p) {
			return deny(ClassProtected, "protected: %s is %s", w.path, w.label)
		}
	}
	for _, s := range g.sensitive {
		if IsWithin(p, s.path) || IsWithin(s.path, p) {
			return deny(ClassSensitive, "sensitive: %s holds %s", s.path, s.label)
		}
	}
	if s, ok := g.containing(g.system, p); ok {
		return deny(ClassSystem, "%s is inside the %s", p, s.label)
	}
	if u, ok := g.containing(g.userContent, p); ok {
		return deny(ClassUserContent, "%s is inside your %s (%s)", p, u.label, u.path)
	}
	return Decision{Allowed: true, Class: ClassProtected}
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
	if req.Purpose == PurposeSelfRemove {
		return g.checkSelfRemove(p)
	}
	for _, w := range g.protected {
		if IsWithin(p, w.path) || IsWithin(w.path, p) {
			return deny(ClassProtected, "protected: %s is %s", w.path, w.label)
		}
	}
	for _, s := range g.sensitive {
		if IsWithin(p, s.path) || IsWithin(s.path, p) {
			return deny(ClassSensitive, "sensitive: %s holds %s", s.path, s.label)
		}
	}
	if req.Purpose == PurposeCleanup && IsSensitiveName(baseName(p)) {
		return deny(ClassSensitive, "sensitive file type: %s is never removed by automatic cleanup", baseName(p))
	}
	if req.Purpose == PurposeLeftover {
		return g.checkLeftover(p, req.Scope)
	}
	if req.Purpose == PurposePurge {
		return g.checkPurge(p, req.Scope, req.Dir)
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
	for _, list := range [][]location{g.critical, g.system, g.userContent, g.protected, g.sensitive} {
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
	for _, s := range g.sensitive {
		if IsWithin(r, s.path) {
			return "", fmt.Errorf("%w: %s holds %s", ErrProtectedRoot, s.path, s.label)
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
	if _, ok := g.containing(g.sensitive, n); ok {
		return ClassSensitive
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
	add(g.sensitive, "sensitive")
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

// InSystemTree reports whether p is, or lies inside, a Windows-owned system
// location (Windows, Program Files, ProgramData, ...). Classify answers
// ClassCritical for the top folders themselves, so callers that refuse
// system trees ask this instead.
func (g *Guard) InSystemTree(p string) bool { return g.within(g.system, p) }

// InAppData reports whether p is, or lies inside, an AppData folder
// (application state, never projects or downloads).
func (g *Guard) InAppData(p string) bool { return g.within(g.appData, p) }

func (g *Guard) within(list []location, p string) bool {
	n, err := Normalize(p)
	if err != nil {
		return false
	}
	_, ok := g.containing(list, n)
	return ok
}

func (g *Guard) containing(list []location, p string) (location, bool) {
	for _, l := range list {
		if IsWithin(p, l.path) {
			return l, true
		}
	}
	return location{}, false
}

// baseName returns the last element of a normalized Windows path.
func baseName(normalized string) string {
	if i := strings.LastIndexByte(normalized, '\\'); i >= 0 {
		return normalized[i+1:]
	}
	return normalized
}

func deny(c Class, format string, args ...any) Decision {
	return Decision{Allowed: false, Class: c, Reason: fmt.Sprintf(format, args...)}
}
