// Package leftovers finds folders left behind by applications that are no
// longer installed, and moves the ones the user confirms to the Recycle Bin.
//
// A folder is only offered with evidence that an app which is gone owned it:
// the install folder an uninstall entry recorded, an exact (normalized) name
// match under a data root, the publisher's folder, or the app's executable
// inside. Similar-looking names are not evidence. Folders still claimed by an
// installed app, a running process, a service or a startup entry are kept,
// and so is anything the safety guard refuses. Every candidate carries a
// confidence level; only high-confidence candidates are preselected.
package leftovers

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// Evidence identifies an application that is no longer installed.
type Evidence struct {
	Name            string   `json:"name"`
	Publisher       string   `json:"publisher,omitempty"`
	InstallLocation string   `json:"install_location,omitempty"`
	Exes            []string `json:"exes,omitempty"`
	// Source is "uninstalled" (just now), "history" (uninstalled earlier
	// by oow), "broken-entry" (registry entry whose app is gone) or
	// "usage-trace" (Windows recorded running an executable that is gone).
	Source string `json:"source"`
	// When is when the app was uninstalled, if known.
	When time.Time `json:"when,omitempty"`
}

// Describe explains in words why this app is believed to be gone.
func (e Evidence) Describe() string {
	switch e.Source {
	case SourceUninstalled:
		return "just uninstalled"
	case SourceHistory:
		if !e.When.IsZero() {
			return "uninstalled with oow on " + e.When.Local().Format("2 Jan 2006")
		}
		return "uninstalled with oow"
	case SourceBroken:
		return "still listed as installed, but its uninstaller and program are gone"
	case SourceTrace:
		return "Windows remembers running it, but its program is gone"
	}
	return e.Source
}

// Evidence sources.
const (
	SourceUninstalled = "uninstalled"
	SourceHistory     = "history"
	SourceBroken      = "broken-entry"
	SourceTrace       = "usage-trace"
)

// FromApp turns an app record into evidence.
func FromApp(a apps.App, source string) Evidence {
	ev := Evidence{Name: a.Name, Publisher: a.Publisher, InstallLocation: a.InstallLocation, Source: source}
	if exe := apps.ExeName(a.DisplayIcon); exe != "" {
		ev.Exes = append(ev.Exes, exe)
	}
	return ev
}

// keys are the distinctive normalized names identifying the app.
func (e Evidence) keys() []string {
	var out []string
	add := func(s string) {
		n := apps.NormalizeName(s)
		if !apps.IsDistinctive(n) {
			return
		}
		for _, k := range out {
			if k == n {
				return
			}
		}
		out = append(out, n)
	}
	add(e.Name)
	if e.InstallLocation != "" {
		add(filepath.Base(strings.TrimRight(e.InstallLocation, `\/`)))
	}
	for _, exe := range e.Exes {
		add(strings.TrimSuffix(exe, filepath.Ext(exe)))
	}
	return out
}

func (e Evidence) publisherKey() string {
	p := apps.NormalizePublisher(e.Publisher)
	if apps.IsDistinctive(p) {
		return p
	}
	return ""
}

// Trace is a program Windows recorded as having run.
type Trace struct {
	Exe     string `json:"exe"`
	Name    string `json:"name,omitempty"`
	Company string `json:"company,omitempty"`
}

// EvidenceFromTraces turns traces of executables that no longer exist into
// evidence. Only executables that lived in an install or data root count
// (not Temp, Downloads, ...). Traces in the same top-level folder are merged.
func EvidenceFromTraces(traces []Trace, g *safety.Guard, exists func(string) bool) []Evidence {
	byDir := map[string]*Evidence{}
	var order []string
	for _, t := range traces {
		exe, err := safety.Normalize(t.Exe)
		if err != nil || !strings.EqualFold(filepath.Ext(exe), ".exe") || exists(exe) {
			continue
		}
		root, ok := g.LeftoverRootFor(exe)
		if !ok {
			continue
		}
		rel := strings.Split(strings.TrimPrefix(exe[len(root.Path):], `\`), `\`)
		if len(rel) < 2 { // the executable must live in an app folder
			continue
		}
		dir := root.Path + `\` + rel[0]
		pub := apps.NormalizePublisher(t.Company)
		if len(rel) > 2 && apps.IsDistinctive(pub) && apps.NormalizePublisher(rel[0]) == pub {
			dir += `\` + rel[1] // publisher\app
		}
		// Programs run from Temp, package caches and Windows folders are not
		// app installations.
		if d := g.Check(safety.Request{Path: dir, Purpose: safety.PurposeLeftover, Scope: root.Path}); !d.Allowed {
			continue
		}
		name := t.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
		}
		ev := byDir[safety.Key(dir)]
		if ev == nil {
			ev = &Evidence{Name: name, Publisher: t.Company, InstallLocation: dir, Source: SourceTrace}
			byDir[safety.Key(dir)] = ev
			order = append(order, safety.Key(dir))
		}
		ev.Exes = appendUnique(ev.Exes, filepath.Base(exe))
	}
	out := make([]Evidence, 0, len(order))
	for _, k := range order {
		out = append(out, *byDir[k])
	}
	return out
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return list
		}
	}
	return append(list, s)
}

// ClaimPath is a location something on the system still uses.
type ClaimPath struct {
	Path  string
	Owner string
}

// Claims records what installed software still owns.
type Claims struct {
	names map[string]string // normalized name -> owner
	paths []ClaimPath       // normalized
}

// NewClaims builds claims from installed apps (except skip, e.g. the app
// being uninstalled) and extra system paths (processes, services, startup).
// Claims that would cover a whole leftover root are ignored, since they
// would come from a malformed registry entry.
func NewClaims(inv *apps.Inventory, skip func(apps.App) bool, extra []ClaimPath, g *safety.Guard) *Claims {
	c := &Claims{names: map[string]string{}}
	addPath := func(p, owner string) {
		n, err := safety.Normalize(p)
		if err != nil || safety.IsVolumeRoot(n) {
			return
		}
		for _, r := range g.LeftoverRoots() {
			if safety.IsWithin(r.Path, n) {
				return // covers a whole root: not meaningful
			}
		}
		c.paths = append(c.paths, ClaimPath{Path: n, Owner: owner})
	}
	if inv != nil {
		for _, a := range inv.Apps {
			if skip != nil && skip(a) {
				continue
			}
			for _, k := range a.Keys() {
				if _, ok := c.names[k]; !ok {
					c.names[k] = a.Name
				}
			}
			if a.InstallLocation != "" {
				addPath(a.InstallLocation, a.Name)
			}
			if icon := strings.Trim(strings.Split(a.DisplayIcon, ",")[0], `" `); strings.ContainsAny(icon, `\/`) {
				addPath(filepath.Dir(icon), a.Name)
			}
		}
	}
	for _, e := range extra {
		addPath(e.Path, e.Owner)
	}
	return c
}

// Owner reports who still claims path (whose normalized base name is norm).
func (c *Claims) Owner(path, norm string) (string, bool) {
	if owner, ok := c.names[norm]; ok {
		return owner, true
	}
	n, err := safety.Normalize(path)
	if err != nil {
		return "unknown", true
	}
	for _, cp := range c.paths {
		if safety.IsWithin(n, cp.Path) || safety.IsWithin(cp.Path, n) {
			return cp.Owner, true
		}
	}
	return "", false
}
