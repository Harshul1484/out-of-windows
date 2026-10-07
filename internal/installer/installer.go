// Package installer finds installer packages the user may no longer need
// (MSI, MSIX/APPX, setup programs, archives and disc images holding an
// installer) in Downloads, Desktop and Documents, and moves the ones the
// user confirms to the Recycle Bin.
//
// Files are identified by content, never by name: an MSI must be a compound
// file with the Windows Installer class ID, an MSIX a ZIP with a package
// manifest, a setup program a PE file with setup-engine data or a version
// resource that says so. Only packages whose product is installed (exact
// normalized match) and that are older than 7 days are preselected.
package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// RecentActivity: packages downloaded or changed more recently than this are
// not preselected.
const RecentActivity = 7 * 24 * time.Hour

// candidateExts pre-filter which files are opened. Each candidate must then
// prove by content that it is an installer.
var candidateExts = map[string]bool{
	".msi": true, ".msp": true, ".msix": true, ".appx": true, ".msixbundle": true, ".appxbundle": true,
	".exe": true, ".zip": true, ".iso": true,
}

// Env carries everything machine-specific.
type Env struct {
	Guard     *safety.Guard
	Locations safety.Locations
	// Inventory is the list of installed apps; nil means unknown.
	Inventory *apps.Inventory
	Now       func() time.Time
	// MaxDepth is how many folder levels below each folder are searched
	// (default 3).
	MaxDepth int
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Status of a package.
type Status string

const (
	StatusReady  Status = "ready"  // selected by default
	StatusReview Status = "review" // listed, not selected by default
)

// Installed says whether the package's product appears to be installed.
type Installed struct {
	// Status is "installed", "not-found" (no installed app matches) or
	// "unknown" (nothing to match on, or the app list is unavailable).
	Status  string `json:"status"`
	App     string `json:"app,omitempty"`
	AppID   string `json:"app_id,omitempty"`
	Version string `json:"version,omitempty"`
	// Match is how it matched: "product code", "package identity",
	// "product name".
	Match string `json:"match,omitempty"`
}

// Package is one installer package found.
type Package struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Type        Type      `json:"type"`
	Format      string    `json:"format"`
	Engine      string    `json:"engine,omitempty"`
	Product     string    `json:"product,omitempty"`
	Version     string    `json:"version,omitempty"`
	Publisher   string    `json:"publisher,omitempty"`
	ProductCode string    `json:"product_code,omitempty"`
	Identity    string    `json:"package_identity,omitempty"`
	Evidence    []string  `json:"evidence"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modified"`
	AgeDays     int       `json:"age_days"`
	Installed   Installed `json:"installed"`
	Status      Status    `json:"status"`
	Selected    bool      `json:"selected"`
	Reasons     []string  `json:"reasons"`

	fingerprint filesystem.Fingerprint
}

// Folder is a folder that was (or could not be) searched.
type Folder struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "ok", "missing", "refused"
	Reason string `json:"reason,omitempty"`
}

// Result is the outcome of Find.
type Result struct {
	Folders    []Folder
	Packages   []*Package
	ScanErrors int
	Cancelled  bool
	Duration   time.Duration
}

// Progress is updated during Find.
type Progress struct {
	Files   atomic.Int64
	Found   atomic.Int64
	current atomic.Pointer[string]
}

// Current returns the folder being searched.
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

// ResolveFolders validates the folders to search.
func ResolveFolders(env *Env, paths []string) []Folder {
	var out []Folder
	seen := map[string]bool{}
	for _, p := range paths {
		f := Folder{Path: p}
		n, err := safety.Normalize(safety.LongPath(p))
		switch {
		case err != nil || !filepath.IsAbs(p):
			f.Status, f.Reason = "refused", "not an absolute folder path"
		default:
			f.Path = n
			f.Status, f.Reason = checkFolder(env, n)
		}
		if f.Status == "ok" {
			if seen[safety.Key(f.Path)] {
				continue
			}
			seen[safety.Key(f.Path)] = true
		}
		out = append(out, f)
	}
	for i := range out {
		for j := range out {
			if i != j && out[i].Status == "ok" && out[j].Status == "ok" && safety.IsStrictlyWithin(out[i].Path, out[j].Path) {
				out[i].Status, out[i].Reason = "refused", "inside "+out[j].Path+", which is searched too"
			}
		}
	}
	return out
}

func checkFolder(env *Env, n string) (string, string) {
	e, err := filesystem.Lstat(n)
	switch {
	case errors.Is(err, filesystem.ErrGone):
		return "missing", "does not exist"
	case err != nil:
		return "refused", err.Error()
	case e.Reparse:
		return "refused", "is a link or junction (not followed)"
	case !e.IsDir():
		return "refused", "is not a folder"
	}
	if safety.IsUNC(n) {
		return "refused", "network folders are not searched (they have no Recycle Bin)"
	}
	l := env.Locations
	if l.SystemDrive != "" && safety.Key(n) == safety.Key(safety.MustNormalize(l.SystemDrive)) {
		return "refused", "the system drive is too broad; name a folder"
	}
	switch env.Guard.Classify(n) {
	case safety.ClassSystem:
		return "refused", "is a system folder (installer caches there are needed for repair and uninstall)"
	case safety.ClassProtected:
		return "refused", "is protected (whitelisted or used by oow)"
	case safety.ClassSensitive:
		return "refused", "holds sensitive data"
	}
	for _, d := range []string{l.RoamingAppData, l.LocalAppData, l.LocalLow} {
		if dn, err := safety.Normalize(d); err == nil && d != "" && safety.IsWithin(n, dn) {
			return "refused", "is application data (installer caches there are needed for repair and uninstall)"
		}
	}
	return "ok", ""
}

// skipDirs are never searched: repositories and dependency folders hold
// project files, and Package Cache folders hold installers Windows needs.
var skipDirs = map[string]bool{"node_modules": true, "package cache": true, "$recycle.bin": true}

// Find searches the folders for installer packages. It never changes
// anything.
func Find(ctx context.Context, env *Env, folders []Folder, prog *Progress) *Result {
	start := time.Now()
	res := &Result{Folders: folders}
	depthLimit := env.MaxDepth
	if depthLimit <= 0 {
		depthLimit = 3
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if ctx.Err() != nil {
			return
		}
		prog.set(dir)
		entries, err := os.ReadDir(dir)
		if err != nil && len(entries) == 0 {
			res.ScanErrors++
			return
		}
		if depth > 0 {
			for _, de := range entries {
				if strings.EqualFold(de.Name(), ".git") {
					return // a repository: its files belong to a project
				}
			}
		}
		for _, de := range entries {
			if ctx.Err() != nil {
				return
			}
			info, err := de.Info()
			if err != nil {
				continue
			}
			e := filesystem.EntryFromInfo(filepath.Join(dir, de.Name()), info)
			if e.Reparse {
				continue // links are never followed
			}
			if e.IsDir() {
				name := strings.ToLower(e.Name)
				if depth+1 > depthLimit || strings.HasPrefix(name, ".") || skipDirs[name] {
					continue
				}
				switch env.Guard.Classify(e.Path) {
				case safety.ClassSystem, safety.ClassProtected, safety.ClassSensitive:
					continue
				}
				walk(e.Path, depth+1)
				continue
			}
			if !candidateExts[strings.ToLower(filepath.Ext(e.Name))] || e.Cloud {
				continue // cloud placeholders are never opened (that would download them)
			}
			if prog != nil {
				prog.Files.Add(1)
			}
			if d := env.Guard.Check(safety.Request{Path: e.Path, Purpose: safety.PurposeUserSelected}); !d.Allowed {
				continue
			}
			det, err := Inspect(e.Path)
			if err != nil || det == nil {
				continue
			}
			res.Packages = append(res.Packages, newPackage(env, e, det))
			if prog != nil {
				prog.Found.Add(1)
			}
		}
	}
	for _, f := range folders {
		if f.Status == "ok" {
			walk(f.Path, 0)
		}
	}
	res.Cancelled = ctx.Err() != nil
	sort.SliceStable(res.Packages, func(i, j int) bool {
		return safety.Key(res.Packages[i].Path) < safety.Key(res.Packages[j].Path)
	})
	res.Duration = time.Since(start)
	return res
}

func newPackage(env *Env, e filesystem.Entry, d *Details) *Package {
	p := &Package{
		Path: e.Path, Name: e.Name, Type: d.Type, Format: d.Format, Engine: d.Engine,
		Product: d.Product, Version: d.Version, Publisher: d.Publisher, ProductCode: d.ProductCode,
		Identity: d.Identity, Evidence: d.Evidence, Size: e.Size(), Modified: e.Fingerprint.Newest(),
		Reasons: []string{}, fingerprint: e.Fingerprint,
	}
	if p.Evidence == nil {
		p.Evidence = []string{}
	}
	age := env.now().Sub(p.Modified)
	p.AgeDays = max(0, int(age/(24*time.Hour)))
	p.Installed = matchInstalled(d, env.Inventory)

	if d.Review != "" {
		p.Reasons = append(p.Reasons, d.Review)
	}
	switch p.Installed.Status {
	case "installed":
		if newerThan(d.Version, p.Installed.Version) {
			p.Reasons = append(p.Reasons, "newer than the installed version "+p.Installed.Version+": it may not be installed yet")
		}
	case "not-found":
		p.Reasons = append(p.Reasons, "no installed app matches it: you may still need it")
	default:
		p.Reasons = append(p.Reasons, "whether it is installed cannot be told: review it")
	}
	if age < RecentActivity {
		p.Reasons = append(p.Reasons, "downloaded or changed "+agoText(age)+" (recent)")
	}
	if len(p.Reasons) == 0 {
		p.Status, p.Selected = StatusReady, true
	} else {
		p.Status = StatusReview
	}
	return p
}

// matchInstalled compares a package with the installed apps, exactly: the
// MSI ProductCode, the MSIX package identity, or the normalized product name
// (also without its publisher prefix). Similar names do not count.
func matchInstalled(d *Details, inv *apps.Inventory) Installed {
	// Archives and disc images carry no product identity (a volume label
	// is not a product name).
	if inv == nil || d.Type == TypeZIP || d.Type == TypeISO || d.Type == TypeMSP {
		return Installed{Status: "unknown"}
	}
	found := func(a apps.App, how string) Installed {
		return Installed{Status: "installed", App: a.Name, AppID: a.ID, Version: a.Version, Match: how}
	}
	if d.ProductCode != "" {
		for _, a := range inv.Apps {
			if strings.EqualFold(a.ProductCode, d.ProductCode) || strings.HasSuffix(strings.ToUpper(a.ID), ":"+strings.ToUpper(d.ProductCode)) {
				return found(a, "product code")
			}
		}
	}
	if d.Identity != "" {
		for _, a := range inv.Apps {
			if a.Source == apps.SourceAppX && strings.EqualFold(strings.SplitN(a.PackageName, "_", 2)[0], d.Identity) {
				return found(a, "package identity")
			}
		}
	}
	keys := productKeys(d.Product, d.Publisher)
	if d.Type == TypeEXE {
		keys = append(keys, productKeys(stripSetupWords(d.Description), d.Publisher)...)
	}
	if len(keys) == 0 {
		return Installed{Status: "unknown"}
	}
	for _, a := range inv.Apps {
		for _, ak := range productKeys(a.Name, a.Publisher) {
			for _, k := range keys {
				if ak == k {
					return found(a, "product name")
				}
			}
		}
	}
	return Installed{Status: "not-found"}
}

// productKeys returns the distinctive normalized forms of a product name:
// the name itself and the name without a leading publisher name
// ("Microsoft Visual Studio Code" → "visualstudiocode").
func productKeys(name, publisher string) []string {
	n := apps.NormalizeName(name)
	var out []string
	if apps.IsDistinctive(n) {
		out = append(out, n)
	}
	if pub := apps.NormalizePublisher(publisher); len(pub) >= 3 && strings.HasPrefix(n, pub) {
		if rest := strings.TrimPrefix(n, pub); apps.IsDistinctive(rest) {
			out = append(out, rest)
		}
	}
	return out
}

func stripSetupWords(s string) string {
	return strings.TrimSpace(setupWords.ReplaceAllString(s, " "))
}

// newerThan reports whether version a is greater than b (dotted numbers;
// false when either is unknown).
func newerThan(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func agoText(d time.Duration) string {
	switch {
	case d < 24*time.Hour:
		return "today"
	case d < 48*time.Hour:
		return "yesterday"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + " days ago"
}

// Skipped is a package that was not moved, with the reason.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Recycled is one package moved to the Recycle Bin.
type Recycled struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Outcome is what Recycle did.
type Outcome struct {
	Recycled  []Recycled `json:"recycled"`
	Bytes     int64      `json:"bytes"`
	Skipped   []Skipped  `json:"skipped"`
	Errors    int        `json:"errors"`
	Cancelled bool       `json:"cancelled,omitempty"`
}

// Recycle moves the packages to the Recycle Bin. Each one is re-verified
// through a handle first (same file as scanned, not a link or cloud
// placeholder, final path approved by the guard for an explicit user
// choice).
func Recycle(ctx context.Context, g *safety.Guard, pkgs []*Package, r filesystem.Recycler) *Outcome {
	out := &Outcome{Recycled: []Recycled{}, Skipped: []Skipped{}}
	check := func(final string) error {
		if d := g.Check(safety.Request{Path: final, Purpose: safety.PurposeUserSelected}); !d.Allowed {
			return errors.New(d.Reason)
		}
		return nil
	}
	for _, p := range pkgs {
		if ctx.Err() != nil {
			out.Cancelled = true
			out.Skipped = append(out.Skipped, Skipped{p.Path, "cancelled"})
			continue
		}
		err := filesystem.RecycleVerified(p.Path, p.fingerprint, check, r)
		switch {
		case err == nil:
			out.Recycled = append(out.Recycled, Recycled{p.Path, p.Size})
			out.Bytes += p.Size
		case errors.Is(err, filesystem.ErrGone):
			out.Skipped = append(out.Skipped, Skipped{p.Path, "already removed"})
		case errors.Is(err, filesystem.ErrChanged):
			out.Skipped = append(out.Skipped, Skipped{p.Path, "changed since it was scanned"})
		case errors.Is(err, filesystem.ErrInUse):
			out.Skipped = append(out.Skipped, Skipped{p.Path, "in use by another program"})
		case errors.Is(err, filesystem.ErrAccessDenied):
			out.Skipped = append(out.Skipped, Skipped{p.Path, "permission denied"})
		case errors.Is(err, filesystem.ErrNoRecycleBin):
			out.Skipped = append(out.Skipped, Skipped{p.Path, "the drive has no Recycle Bin"})
		case errors.Is(err, filesystem.ErrReparsePoint), errors.Is(err, filesystem.ErrCloudFile),
			errors.Is(err, filesystem.ErrPolicy), errors.Is(err, filesystem.ErrOutsideFence):
			out.Skipped = append(out.Skipped, Skipped{p.Path, "refused by safety policy"})
		default:
			out.Skipped = append(out.Skipped, Skipped{p.Path, err.Error()})
			out.Errors++
		}
	}
	return out
}
