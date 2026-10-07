// Package purge finds rebuildable project artifacts (dependency folders such
// as node_modules and .venv, build output such as target and bin/obj, and
// tool caches) below the user's project folders, and deletes the ones the
// user confirms.
//
// An artifact is only recognized next to its project's marker file
// (package.json next to node_modules, Cargo.toml next to target, ...). A
// found artifact is never searched for more projects. Artifacts holding
// Git-tracked files, a nested repository, links, cloud-only files or
// sensitive files are kept; when Git cannot answer, the folder is kept.
// Artifacts changed in the last 7 days, and folders whose names are also used
// for hand-made content (dist, build, out) without Git evidence, are listed
// but not selected by default.
package purge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// RecentActivity is how recently an artifact may have changed and still be
// preselected.
const RecentActivity = 7 * 24 * time.Hour

// Env carries everything machine-specific, so the scanner runs unchanged
// against the real system, the sandbox or a test fixture.
type Env struct {
	Guard     *safety.Guard
	Locations safety.Locations
	// Git answers tracked/ignored questions; nil means ExecGit{}.
	Git Git
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Recent overrides RecentActivity (tests).
	Recent time.Duration
	// MaxDepth is how many folder levels below a root are searched for
	// projects (default 8). Artifacts themselves are measured completely.
	MaxDepth int
	// Workers bounds concurrent measurements and deletions (default
	// min(8, CPUs)).
	Workers int
	// Ceiling, when set, is the highest folder searched for an enclosing
	// Git repository (the sandbox root, which simulates a whole machine).
	Ceiling string
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Env) git() Git {
	if e.Git != nil {
		return e.Git
	}
	return ExecGit{}
}

func (e *Env) recent() time.Duration {
	if e.Recent > 0 {
		return e.Recent
	}
	return RecentActivity
}

func (e *Env) maxDepth() int {
	if e.MaxDepth > 0 {
		return e.MaxDepth
	}
	return 8
}

func (e *Env) workers() int {
	if e.Workers > 0 {
		return e.Workers
	}
	return min(8, max(2, runtime.NumCPU()))
}

// Status of an artifact.
type Status string

const (
	// StatusReady is offered and selected by default.
	StatusReady Status = "ready"
	// StatusReview is offered but not selected by default (recent activity
	// or weaker evidence); see Reasons.
	StatusReview Status = "review"
	// StatusKept is never offered; see Reasons.
	StatusKept Status = "kept"
)

// Artifact is one rebuildable folder inside a project.
type Artifact struct {
	Path      string          `json:"path"`
	Kind      string          `json:"kind"`
	Label     string          `json:"label"`
	Ecosystem string          `json:"ecosystem"`
	Bytes     int64           `json:"bytes"`
	Files     int             `json:"files"`
	Newest    time.Time       `json:"newest_change"`
	Status    Status          `json:"status"`
	Selected  bool            `json:"selected"`
	Reasons   []string        `json:"reasons"`
	Rebuild   string          `json:"rebuild"`
	Result    *ArtifactResult `json:"result,omitempty"`

	kind        *Kind
	project     *Project
	repo        string // folder holding .git, "" when not in a repository
	fingerprint filesystem.Fingerprint
}

// Project is a folder with a marker file and the artifacts found in it.
type Project struct {
	Path       string      `json:"path"`
	Markers    []string    `json:"markers"`
	Repository string      `json:"repository,omitempty"`
	Artifacts  []*Artifact `json:"artifacts"`
}

// Root is a folder that was (or could not be) scanned for projects.
type Root struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Root sources and statuses.
const (
	SourceArgument = "argument"
	SourceConfig   = "config"
	SourceDefault  = "default"

	RootOK      = "ok"
	RootMissing = "missing"
	RootRefused = "refused"
)

// Result is the outcome of Find.
type Result struct {
	Roots      []Root
	Projects   []*Project
	ScanErrors int // folders that could not be read while looking for projects
	Cancelled  bool
	Started    time.Time
	Duration   time.Duration
}

// Artifacts returns every artifact, by project.
func (r *Result) Artifacts() []*Artifact {
	var out []*Artifact
	for _, p := range r.Projects {
		out = append(out, p.Artifacts...)
	}
	return out
}

// Progress is updated during Find and Remove; safe for concurrent use.
type Progress struct {
	Projects  atomic.Int64
	Artifacts atomic.Int64
	Files     atomic.Int64
	Bytes     atomic.Int64
	current   atomic.Pointer[string]
}

// Current returns the folder being looked at.
func (p *Progress) Current() string {
	if p == nil {
		return ""
	}
	if s := p.current.Load(); s != nil {
		return *s
	}
	return ""
}

func (p *Progress) set(s string) {
	if p != nil {
		p.current.Store(&s)
	}
}

// DefaultRootNames are folders below the user profile where projects
// usually live. Only the ones that exist are scanned.
var DefaultRootNames = []string{
	`source\repos`, "Projects", "projects", "dev", "code", "src", "workspace", "repos", "GitHub", `Documents\GitHub`,
}

// Roots resolves the folders to scan: the explicit paths when given, else
// the configured ones, else the defaults that exist.
func Roots(env *Env, args, configured []string) []Root {
	var cands []Root
	switch {
	case len(args) > 0:
		for _, a := range args {
			cands = append(cands, Root{Path: a, Source: SourceArgument})
		}
	case len(configured) > 0:
		for _, c := range configured {
			cands = append(cands, Root{Path: c, Source: SourceConfig})
		}
	default:
		if env.Locations.UserProfile != "" {
			for _, n := range DefaultRootNames {
				cands = append(cands, Root{Path: filepath.Join(env.Locations.UserProfile, n), Source: SourceDefault})
			}
		}
	}
	var out []Root
	seen := map[string]bool{}
	for _, r := range cands {
		r = resolveRoot(env, r)
		if r.Status == RootMissing && r.Source == SourceDefault {
			continue
		}
		if r.Status == RootOK {
			k := safety.Key(r.Path)
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, r)
	}
	// A root inside another root is scanned as part of it.
	for i := range out {
		for j := range out {
			if i != j && out[i].Status == RootOK && out[j].Status == RootOK && safety.IsStrictlyWithin(out[i].Path, out[j].Path) {
				out[i].Status, out[i].Reason = RootRefused, "inside "+out[j].Path+", which is scanned too"
			}
		}
	}
	return out
}

func resolveRoot(env *Env, r Root) Root {
	n, err := safety.Normalize(safety.LongPath(r.Path))
	if err != nil || !filepath.IsAbs(r.Path) {
		r.Status, r.Reason = RootRefused, "not an absolute folder path"
		return r
	}
	r.Path = n
	e, err := filesystem.Lstat(n)
	switch {
	case errors.Is(err, filesystem.ErrGone):
		r.Status, r.Reason = RootMissing, "does not exist"
		return r
	case err != nil:
		r.Status, r.Reason = RootRefused, err.Error()
		return r
	case e.Reparse:
		target, _ := filesystem.FinalPath(n)
		r.Status, r.Reason = RootRefused, "is a link or junction (not followed); add its target "+target+" instead"
		return r
	case !e.IsDir():
		r.Status, r.Reason = RootRefused, "is not a folder"
		return r
	}
	if final, err := filesystem.FinalPath(n); err != nil || safety.Key(final) != safety.Key(n) {
		r.Status, r.Reason = RootRefused, "resolves to "+final+" through a link or substituted drive; add that path instead"
		return r
	}
	if why := refuseRoot(env, n); why != "" {
		r.Status, r.Reason = RootRefused, why
		return r
	}
	r.Status = RootOK
	return r
}

// refuseRoot says why a folder may not be scanned for projects, or "".
func refuseRoot(env *Env, n string) string {
	l := env.Locations
	if safety.IsUNC(n) {
		return "network folders are not scanned"
	}
	if sys := systemDrive(l); sys != "" && safety.Key(n) == safety.Key(sys) {
		return "the system drive is too broad; name a projects folder"
	}
	switch env.Guard.Classify(n) {
	case safety.ClassSystem:
		return "is a system folder"
	case safety.ClassProtected:
		return "is protected (whitelisted or used by oow)"
	case safety.ClassSensitive:
		return "holds sensitive data"
	}
	for _, d := range []string{l.UserProfile + `\AppData`, l.RoamingAppData, l.LocalAppData, l.LocalLow} {
		if dn, err := safety.Normalize(d); err == nil && d != `\AppData` && d != "" && safety.IsWithin(n, dn) {
			return "is application data, not a projects folder"
		}
	}
	for _, broad := range []string{l.UsersRoot, l.PublicProfile} {
		b, err := safety.Normalize(broad)
		switch {
		case err != nil || broad == "" || !safety.IsWithin(n, b) || insideProfile(l, n):
		case safety.Key(n) == safety.Key(b):
			return "is too broad; name a projects folder"
		default:
			return "belongs to another user or the Public profile; only your own folders are scanned"
		}
	}
	if p, err := safety.Normalize(l.UserProfile); err == nil && l.UserProfile != "" && safety.IsStrictlyWithin(p, n) {
		return "contains the whole user profile; name a projects folder"
	}
	return ""
}

func insideProfile(l safety.Locations, n string) bool {
	p, err := safety.Normalize(l.UserProfile)
	return err == nil && l.UserProfile != "" && safety.IsWithin(n, p)
}

func systemDrive(l safety.Locations) string {
	if l.SystemDrive != "" {
		return l.SystemDrive
	}
	if len(l.Windows) >= 3 && l.Windows[1] == ':' {
		return l.Windows[:3]
	}
	return ""
}

// skipNames are folders never searched for projects: dependency stores of
// other ecosystems (vendored code is not regenerated by a build), version
// control and system folders. Names starting with "." are skipped too.
var skipNames = map[string]bool{
	"node_modules": true, "vendor": true, "third_party": true, "third-party": true, "thirdparty": true,
	"external": true, "externals": true, "extern": true, "deps": true, "bower_components": true,
	"jspm_packages": true, "pods": true, "site-packages": true, "package cache": true,
	"$recycle.bin": true, "system volume information": true, "appdata": true,
}

// Find scans the roots for projects and their artifacts. It never changes
// anything. When cancelled it returns what it has with Cancelled set; such a
// result must not be used for deletion.
func Find(ctx context.Context, env *Env, roots []Root, prog *Progress) *Result {
	s := &scanner{env: env, ctx: ctx, prog: prog, projects: map[string]*Project{},
		seen: map[string]bool{}, repos: map[string]string{}}
	res := &Result{Roots: roots, Started: env.now()}
	start := time.Now()
	for _, r := range roots {
		if r.Status != RootOK {
			continue
		}
		s.visit(r.Path, 0, nil)
	}
	s.measureAll()
	if ctx.Err() == nil {
		s.checkGit()
	}
	res.ScanErrors = int(s.errors.Load())
	res.Cancelled = ctx.Err() != nil
	for _, a := range s.cands {
		s.evaluate(a)
	}
	for _, p := range s.projects {
		var keep []*Artifact
		for _, a := range p.Artifacts {
			if a.Files > 0 || a.Status == StatusKept {
				keep = append(keep, a)
			}
		}
		if len(keep) == 0 {
			continue
		}
		sort.Slice(keep, func(i, j int) bool { return safety.Key(keep[i].Path) < safety.Key(keep[j].Path) })
		p.Artifacts = keep
		res.Projects = append(res.Projects, p)
	}
	sort.Slice(res.Projects, func(i, j int) bool {
		return safety.Key(res.Projects[i].Path) < safety.Key(res.Projects[j].Path)
	})
	res.Duration = time.Since(start)
	return res
}

type scanner struct {
	env      *Env
	ctx      context.Context
	prog     *Progress
	projects map[string]*Project
	cands    []*Artifact
	seen     map[string]bool
	repos    map[string]string
	errors   atomic.Int64
	measures sync.Map // *Artifact → *measure
	gitErr   map[*Artifact]string
	tracked  map[*Artifact]bool
	ignored  map[*Artifact]bool
}

// visit looks for projects in dir. py is the nearest enclosing Python
// project, which owns __pycache__ folders anywhere below it.
func (s *scanner) visit(dir string, depth int, py *Project) {
	if s.ctx.Err() != nil {
		return
	}
	s.prog.set(dir)
	entries, err := os.ReadDir(dir)
	if err != nil && len(entries) == 0 {
		s.errors.Add(1)
		return
	}
	files := map[string]bool{}
	var dirs []filesystem.Entry
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := filesystem.EntryFromInfo(filepath.Join(dir, de.Name()), info)
		if e.IsDir() && !e.Reparse {
			dirs = append(dirs, e)
		} else if !e.IsDir() {
			files[strings.ToLower(de.Name())] = true
		}
	}
	var proj *Project
	if markers := markersIn(files); len(markers) > 0 {
		sort.Strings(markers)
		proj = s.project(dir, markers)
		if anyMatch(pythonMarkers, files) {
			py = proj
		}
	}
	for _, d := range dirs {
		if s.ctx.Err() != nil {
			return
		}
		if k := s.match(proj, files, py, d); k != nil {
			owner := proj
			if k.Anywhere {
				owner = py
			}
			s.add(owner, k, d)
			continue
		}
		name := strings.ToLower(d.Name)
		if strings.HasPrefix(name, ".") || skipNames[name] || depth+1 > s.env.maxDepth() {
			continue
		}
		switch s.env.Guard.Classify(d.Path) {
		case safety.ClassSystem, safety.ClassProtected, safety.ClassSensitive:
			continue
		}
		s.visit(d.Path, depth+1, py)
	}
}

// match returns the artifact kind of folder d, or nil.
func (s *scanner) match(proj *Project, files map[string]bool, py *Project, d filesystem.Entry) *Kind {
	for _, k := range Kinds {
		if !k.matchesName(d.Name) {
			continue
		}
		if k.Anywhere {
			if py == nil {
				continue
			}
		} else if proj == nil || !k.hasMarker(files) {
			continue
		}
		if len(k.Inside) > 0 && !hasFile(d.Path, k.Inside) {
			continue
		}
		if looksLikeProject(d.Path) {
			return nil // a project, not an artifact: searched like any folder
		}
		return k
	}
	return nil
}

func hasFile(dir string, names []string) bool {
	for _, n := range names {
		if e, err := filesystem.Lstat(filepath.Join(dir, n)); err == nil && !e.IsDir() && !e.Reparse {
			return true
		}
	}
	return false
}

func looksLikeProject(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, de := range entries {
		if !de.IsDir() && matchAny(projectMarkers, de.Name()) {
			return true
		}
	}
	return false
}

func (s *scanner) project(dir string, markers []string) *Project {
	k := safety.Key(dir)
	if p, ok := s.projects[k]; ok {
		return p
	}
	p := &Project{Path: dir, Markers: markers, Repository: s.repoOf(dir)}
	s.projects[k] = p
	s.prog.addProject()
	return p
}

func (p *Progress) addProject() {
	if p != nil {
		p.Projects.Add(1)
	}
}

func (s *scanner) add(proj *Project, k *Kind, d filesystem.Entry) {
	key := safety.Key(d.Path)
	if s.seen[key] || proj == nil {
		return
	}
	s.seen[key] = true
	a := &Artifact{Path: d.Path, Kind: k.ID, Label: k.Label, Ecosystem: k.Ecosystem, Rebuild: k.Rebuild,
		Reasons: []string{}, kind: k, project: proj, repo: proj.Repository, fingerprint: d.Fingerprint}
	proj.Artifacts = append(proj.Artifacts, a)
	s.cands = append(s.cands, a)
	if s.prog != nil {
		s.prog.Artifacts.Add(1)
	}
}

// repoOf returns the nearest folder at or above dir holding .git (a folder,
// or a file for worktrees and submodules), or "".
func (s *scanner) repoOf(dir string) string {
	var walked []string
	repo := ""
	for d := dir; ; {
		k := safety.Key(d)
		if r, ok := s.repos[k]; ok {
			repo = r
			break
		}
		walked = append(walked, k)
		if _, err := filesystem.Lstat(filepath.Join(d, ".git")); err == nil {
			repo = d
			break
		}
		parent := filepath.Dir(d)
		if parent == d || (s.env.Ceiling != "" && k == safety.Key(filepath.Clean(s.env.Ceiling))) {
			break
		}
		d = parent
	}
	for _, k := range walked {
		s.repos[k] = repo
	}
	return repo
}

// measure is what a full walk of an artifact found.
type measure struct {
	bytes      int64
	files      int
	newest     time.Time
	hasGit     bool
	links      int
	cloud      int
	sensitive  []string
	unreadable int
	foreign    int      // files an "only" kind may not contain
	top        []string // direct children, lower case, folders with a trailing \
	cancelled  bool
	entries    []filesystem.Entry // collected only when requested (deletion)
}

// walkArtifact walks an artifact completely without following links.
func walkArtifact(ctx context.Context, path string, k *Kind, collect bool, prog *Progress) *measure {
	m := &measure{}
	name := filepath.Base(path)
	prefix := len(path) + 1
	err := filesystem.Walk(ctx, path, func(e filesystem.Entry) bool {
		rel := e.Path[min(prefix, len(e.Path)):]
		depth := strings.Count(rel, `\`) + 1
		lower := strings.ToLower(e.Name)
		if depth == 1 {
			if e.IsDir() {
				m.top = append(m.top, lower+`\`)
			} else {
				m.top = append(m.top, lower)
			}
		}
		if lower == ".git" {
			m.hasGit = true
			return false
		}
		if e.Reparse {
			m.links++
			return false
		}
		if e.Cloud {
			m.cloud++
		}
		if collect {
			m.entries = append(m.entries, e)
		}
		if e.IsDir() {
			return true
		}
		m.files++
		m.bytes += e.Size()
		if t := e.Fingerprint.Newest(); t.After(m.newest) {
			m.newest = t
		}
		if safety.IsPurgeSensitive(name, depth, e.Name) && len(m.sensitive) < 3 {
			m.sensitive = append(m.sensitive, rel)
		}
		if len(k.only) > 0 && !matchAny(k.only, e.Name) {
			m.foreign++
		}
		if prog != nil {
			prog.Files.Add(1)
			prog.Bytes.Add(e.Size())
		}
		return true
	}, func(string, error) { m.unreadable++ })
	if err != nil {
		if ctx.Err() != nil {
			m.cancelled = true
		} else {
			m.unreadable++
		}
	}
	return m
}

func (s *scanner) measureAll() {
	work := make(chan *Artifact)
	var wg sync.WaitGroup
	for i := 0; i < s.env.workers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range work {
				s.prog.set(a.Path)
				s.measures.Store(a, walkArtifact(s.ctx, a.Path, a.kind, false, s.prog))
			}
		}()
	}
	for _, a := range s.cands {
		if s.ctx.Err() != nil {
			break
		}
		work <- a
	}
	close(work)
	wg.Wait()
}

// checkGit asks each repository which artifacts hold tracked files and
// which ambiguous ones it ignores. Any failure keeps the artifacts.
func (s *scanner) checkGit() {
	s.gitErr = map[*Artifact]string{}
	s.tracked = map[*Artifact]bool{}
	s.ignored = map[*Artifact]bool{}
	byRepo := map[string][]*Artifact{}
	var order []string
	for _, a := range s.cands {
		if a.repo == "" {
			continue
		}
		if _, ok := byRepo[a.repo]; !ok {
			order = append(order, a.repo)
		}
		byRepo[a.repo] = append(byRepo[a.repo], a)
	}
	git := s.env.git()
	for _, repo := range order {
		arts := byRepo[repo]
		s.prog.set(repo)
		rels := map[string]*Artifact{}
		var list, ambiguous []string
		for _, a := range arts {
			r, err := relSlash(repo, a.Path)
			if err != nil {
				s.gitErr[a] = err.Error()
				continue
			}
			rels[r] = a
			list = append(list, r)
			if a.kind.Ambiguous {
				ambiguous = append(ambiguous, r)
			}
		}
		tracked, err := git.Tracked(s.ctx, repo, list)
		if err != nil {
			for _, r := range list {
				s.gitErr[rels[r]] = gitFailure(err)
			}
			continue
		}
		for r := range tracked {
			if a := rels[r]; a != nil {
				s.tracked[a] = true
			}
		}
		if len(ambiguous) > 0 {
			if ign, err := git.Ignored(s.ctx, repo, ambiguous); err == nil {
				for r := range ign {
					if a := rels[r]; a != nil {
						s.ignored[a] = true
					}
				}
			}
		}
	}
}

func gitFailure(err error) string {
	if errors.Is(err, ErrGitMissing) {
		return "it is in a Git repository and Git is not installed or not on PATH, so tracked files cannot be ruled out"
	}
	return "Git could not check it for tracked files (" + err.Error() + ")"
}

// evaluate decides an artifact's status from its measurement and Git.
func (s *scanner) evaluate(a *Artifact) {
	v, ok := s.measures.Load(a)
	if !ok {
		a.Status, a.Reasons = StatusKept, append(a.Reasons, "not measured (the scan was stopped)")
		return
	}
	m := v.(*measure)
	a.Bytes, a.Files, a.Newest = m.bytes, m.files, m.newest
	if a.Newest.IsZero() {
		a.Newest = a.fingerprint.CreationTime()
	}
	for _, r := range keepReasons(s.env, a, m) {
		a.Reasons = append(a.Reasons, r)
	}
	if a.repo != "" {
		switch {
		case s.gitErr[a] != "":
			a.Reasons = append(a.Reasons, s.gitErr[a])
		case s.tracked == nil:
			a.Reasons = append(a.Reasons, "Git was not consulted (the scan was stopped)")
		case s.tracked[a]:
			a.Reasons = append(a.Reasons, "contains files tracked by Git")
		}
	}
	if len(a.Reasons) > 0 {
		a.Status = StatusKept
		return
	}
	if a.kind.Ambiguous {
		switch {
		case a.repo == "":
			a.Reasons = append(a.Reasons, "not in a Git repository, so it may be hand-made: review it")
		case !s.ignored[a]:
			a.Reasons = append(a.Reasons, "not ignored by Git, so it may be hand-made: review it")
		}
	}
	if a.kind.shape != nil {
		if ok, why := a.kind.shape(m.top); !ok {
			a.Reasons = append(a.Reasons, why)
		}
	}
	if age := s.env.now().Sub(a.Newest); age < s.env.recent() {
		a.Reasons = append(a.Reasons, "changed "+ago(age)+" (recent activity)")
	}
	if len(a.Reasons) > 0 {
		a.Status = StatusReview
		return
	}
	a.Status, a.Selected = StatusReady, true
}

// keepReasons are the findings that keep an artifact whatever the user
// selects. They are re-checked right before deletion.
func keepReasons(env *Env, a *Artifact, m *measure) []string {
	var out []string
	if d := env.Guard.Check(safety.Request{Path: a.Path, Purpose: safety.PurposePurge, Scope: a.Path, Dir: true}); !d.Allowed {
		out = append(out, d.Reason)
	}
	if m.cancelled {
		out = append(out, "not measured completely (the scan was stopped)")
	}
	if m.unreadable > 0 {
		out = append(out, "some folders inside could not be read, so it could not be checked completely")
	}
	if m.hasGit {
		out = append(out, "contains a Git repository (.git), which may hold unpushed work")
	}
	if m.links > 0 {
		out = append(out, "contains links or junctions (workspace or pnpm links), which are never deleted; "+
			"remove it with its package manager")
	}
	if m.cloud > 0 {
		out = append(out, "some files are stored only in the cloud (online-only placeholders)")
	}
	if len(m.sensitive) > 0 {
		out = append(out, "contains sensitive files such as keys or certificates ("+strings.Join(m.sensitive, ", ")+")")
	}
	if m.foreign > 0 {
		out = append(out, "contains files other than "+strings.Join(a.kind.only, " or "))
	}
	return out
}

func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "less than an hour ago"
	case d < 48*time.Hour:
		return itoa(int(d/time.Hour)) + " hours ago"
	}
	return itoa(int(d/(24*time.Hour))) + " days ago"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
