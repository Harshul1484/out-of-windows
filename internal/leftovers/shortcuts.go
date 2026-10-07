package leftovers

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/startup"
	"github.com/Harshul1484/out-of-windows/internal/system"
)

// ShortcutFolder is a folder searched for broken shortcuts.
type ShortcutFolder struct {
	Path string
	// Location names the folder for people ("your Start Menu").
	Location string
	// Depth is how many folder levels are searched: 1 is only the files
	// directly inside.
	Depth int
	// Removable is set for the user's own folders: a broken shortcut there
	// may go to the Recycle Bin with the leftover folder it pointed into
	// (guard purpose PurposeShortcut, scope Path).
	Removable bool
	// Skip lists folders inside Path that are not searched: the Startup
	// folders, whose shortcuts are startup entries (`oow startup`).
	Skip []string
}

// Shortcut is a shortcut (.lnk) whose program is verifiably missing.
type Shortcut struct {
	Path     string `json:"path"`
	Target   string `json:"target"`
	Location string `json:"location"`
	// Removable reports whether it goes to the Recycle Bin together with
	// the leftover folder its program lived in: only shortcuts in the user's
	// own Start Menu and Desktop do.
	Removable bool `json:"removable"`
	// Note says why a shortcut stays.
	Note        string                 `json:"note,omitempty"`
	Scope       string                 `json:"-"`
	Fingerprint filesystem.Fingerprint `json:"-"`
}

// LinkResolver reads shortcuts and checks their targets.
type LinkResolver struct {
	// Read parses a shortcut file (startup.ReadLink, bounded).
	Read func(path string) (*startup.Link, error)
	// Expand expands %VARIABLES% in environment-variable targets.
	Expand func(string) string
	// Probe reports whether an absolute path exists (system.ProbePath:
	// "missing" only after a verified "not found" on a fixed drive).
	Probe func(string) (system.Presence, string)
}

// Bounds of a shortcut search.
const (
	maxShortcutDepth = 4
	maxShortcuts     = 5000
	maxShortcutSize  = 1 << 20
)

// FindBrokenShortcuts lists the shortcuts in folders whose program is
// verifiably missing: the target is an .exe named by an absolute path on a
// fixed drive that reports "not found". Shortcuts to network paths, shell
// items (Store apps, advertised installer shortcuts), other file types,
// unreadable shortcuts and programs that exist are left out. It changes
// nothing; links and cloud placeholders are neither followed nor opened.
func FindBrokenShortcuts(ctx context.Context, g *safety.Guard, folders []ShortcutFolder, lr LinkResolver) []Shortcut {
	if lr.Read == nil || lr.Probe == nil {
		return nil
	}
	var out []Shortcut
	seen, read := map[string]bool{}, 0
	for _, f := range folders {
		root, err := safety.Normalize(f.Path)
		if err != nil || ctx.Err() != nil {
			continue
		}
		var skip []string
		for _, s := range f.Skip {
			if n, err := safety.Normalize(s); err == nil {
				skip = append(skip, n)
			}
		}
		depth := min(max(f.Depth, 1), maxShortcutDepth)
		_ = filesystem.Walk(ctx, root, func(e filesystem.Entry) bool {
			if read >= maxShortcuts || e.Reparse || e.Cloud {
				return false
			}
			p, err := safety.Normalize(e.Path)
			if err != nil {
				return false
			}
			level := len(splitPath(p)) - len(splitPath(root))
			if e.IsDir() {
				for _, s := range skip {
					if safety.Key(s) == safety.Key(p) {
						return false
					}
				}
				return level < depth
			}
			if !strings.EqualFold(filepath.Ext(p), ".lnk") || e.Size() > maxShortcutSize || seen[safety.Key(p)] {
				return false
			}
			read++
			if sc, ok := brokenShortcut(g, f, root, p, e.Fingerprint, lr); ok {
				seen[safety.Key(p)] = true
				out = append(out, sc)
			}
			return false
		}, nil)
	}
	return out
}

// shortcutTarget returns the program a shortcut starts when it is
// verifiably missing.
func shortcutTarget(path string, lr LinkResolver) (string, bool) {
	link, err := lr.Read(path)
	if err != nil || link == nil || link.Network {
		return "", false
	}
	target := link.TargetPath(filepath.Dir(path), lr.Expand)
	if target == "" || !strings.EqualFold(filepath.Ext(target), ".exe") {
		return "", false
	}
	if st, _ := lr.Probe(target); st != system.Absent {
		return "", false
	}
	t, err := safety.Normalize(target)
	if err != nil || safety.IsUNC(t) {
		return "", false
	}
	return t, true
}

func brokenShortcut(g *safety.Guard, f ShortcutFolder, root, path string, fp filesystem.Fingerprint, lr LinkResolver) (Shortcut, bool) {
	target, ok := shortcutTarget(path, lr)
	if !ok {
		return Shortcut{}, false
	}
	sc := Shortcut{Path: path, Target: target, Location: f.Location, Fingerprint: fp}
	switch d := g.Check(safety.Request{Path: path, Purpose: safety.PurposeShortcut, Scope: root}); {
	case !f.Removable:
		sc.Note = "left alone: it is shared by all users"
	case !d.Allowed:
		sc.Note = "kept: " + d.Reason
	default:
		sc.Removable, sc.Scope = true, root
	}
	return sc, true
}

// layoutFolders hold an app's program inside its folder; the app folder is
// their parent.
var layoutFolders = map[string]bool{"bin": true, "bin32": true, "bin64": true, "x64": true, "x86": true,
	"amd64": true, "arm64": true, "win32": true, "win64": true, "program": true, "app": true}

// shortcutAppFolder returns the app folder a missing program lived in: the
// folder that held it, or its parent when that folder is a layout folder
// such as bin or x64. The folder must still exist in an install or data
// location, be allowed as a leftover by the guard, and be specific: its own
// name or (publisher\product) its parent's name must be distinctive, so a
// shared "Tools" or "Apps" folder never becomes evidence.
func shortcutAppFolder(g *safety.Guard, target string) (string, safety.LeftoverRoot, bool) {
	root, ok := g.LeftoverRootFor(target)
	if !ok {
		return "", root, false
	}
	rel := splitPath(target[len(root.Path):])
	if len(rel) < 2 { // the program must sit in an app folder
		return "", root, false
	}
	parts := rel[:len(rel)-1]
	if len(parts) > 1 && layoutFolders[strings.ToLower(parts[len(parts)-1])] {
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	specific := apps.IsDistinctive(apps.NormalizeName(last)) ||
		len(parts) > 1 && apps.IsDistinctive(apps.NormalizePublisher(parts[len(parts)-2]))
	if !specific {
		return "", root, false
	}
	dir := root.Path + `\` + strings.Join(parts, `\`)
	if d := g.Check(safety.Request{Path: dir, Purpose: safety.PurposeLeftover, Scope: root.Path}); !d.Allowed || !isDir(dir) {
		return "", root, false
	}
	return dir, root, true
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, `\`) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// EvidenceFromShortcuts turns broken shortcuts into evidence: the exact app
// folder their missing program lived in (see shortcutAppFolder). Names are
// never matched: the shortcut's name is shown, not searched for. Shortcuts
// into the same folder are merged. Such evidence is at most medium
// confidence on its own.
func EvidenceFromShortcuts(shortcuts []Shortcut, g *safety.Guard) []Evidence {
	byDir := map[string]*Evidence{}
	var order []string
	for _, sc := range shortcuts {
		dir, _, ok := shortcutAppFolder(g, sc.Target)
		if !ok {
			continue
		}
		k := safety.Key(dir)
		ev := byDir[k]
		if ev == nil {
			ev = &Evidence{Name: strings.TrimSuffix(filepath.Base(sc.Path), filepath.Ext(sc.Path)), InstallLocation: dir, Source: SourceShortcut}
			byDir[k] = ev
			order = append(order, k)
		}
		ev.Exes = appendUnique(ev.Exes, filepath.Base(sc.Target))
		ev.Shortcuts = appendUnique(ev.Shortcuts, sc.Path)
	}
	out := make([]Evidence, 0, len(order))
	for _, k := range order {
		out = append(out, *byDir[k])
	}
	return out
}

// attachShortcuts lists, on each candidate, the broken shortcuts whose
// program lived inside it.
func attachShortcuts(cands []Candidate, shortcuts []Shortcut) {
	for i := range cands {
		for _, sc := range shortcuts {
			if safety.IsStrictlyWithin(sc.Target, cands[i].Path) {
				cands[i].Shortcuts = append(cands[i].Shortcuts, sc)
			}
		}
	}
}

// recycleShortcut moves a broken shortcut to the Recycle Bin after checking
// that it still points to the same missing program, then verifying it
// through a handle (identity, link, fence) with PurposeShortcut.
func recycleShortcut(env *Env, sc Shortcut, r filesystem.Recycler) error {
	if !sc.Removable || sc.Scope == "" || env.Links.Read == nil || env.Links.Probe == nil {
		return errShortcutKept
	}
	target, ok := shortcutTarget(sc.Path, env.Links)
	if !ok || safety.Key(target) != safety.Key(sc.Target) {
		return filesystem.ErrChanged
	}
	check := func(final string) error {
		if d := env.Guard.Check(safety.Request{Path: final, Purpose: safety.PurposeShortcut, Scope: sc.Scope}); !d.Allowed {
			return errors.New(d.Reason)
		}
		return nil
	}
	return filesystem.RecycleVerified(sc.Path, sc.Fingerprint, check, r)
}

var errShortcutKept = errors.New("left alone")
