// Package cleanup finds and removes reclaimable files using declarative rules.
//
// A Rule describes one cleanup target: where it lives (path templates
// resolved through safety.Locations), which items qualify (age, name
// patterns), and the user-facing explanation of what it is, why removing it is
// safe, and what happens afterwards. Adding a cleaner means adding a Rule; the
// engine handles scanning, safety checks, verification and reporting.
package cleanup

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Category groups rules in the UI.
type Category string

const (
	CategoryTemp      Category = "temp"
	CategoryBrowser   Category = "browser"
	CategoryApps      Category = "apps"
	CategoryWindows   Category = "windows"
	CategoryDeveloper Category = "developer"
	CategoryLogs      Category = "logs"
)

// Title returns the display title of a category.
func (c Category) Title() string {
	switch c {
	case CategoryTemp:
		return "Temporary files"
	case CategoryBrowser:
		return "Browser caches"
	case CategoryApps:
		return "Application caches"
	case CategoryWindows:
		return "Windows caches"
	case CategoryDeveloper:
		return "Developer caches"
	case CategoryLogs:
		return "Logs and crash reports"
	}
	return string(c)
}

// CategoryOrder is the display order of categories.
var CategoryOrder = []Category{
	CategoryTemp, CategoryWindows, CategoryBrowser, CategoryApps, CategoryDeveloper, CategoryLogs,
}

// Rule is one declarative cleanup target.
type Rule struct {
	// ID is stable and dotted, e.g. "temp.user". Whitelist entries and JSON
	// output refer to rules by ID, so IDs must never be reused or renamed.
	ID       string
	Name     string
	Category Category

	// App is the display name of the owning application ("Google Chrome"),
	// used in messages such as "close Google Chrome to clean this".
	App string

	// Roots are path templates such as `{LocalAppData}\D3DSCache`. Only the
	// contents of a root are cleaned, never the root itself.
	//
	// A root may contain one `{profile}` component, which expands to every
	// real directory at that position that contains ProfileMarker (a file or
	// folder name), e.g. `...\User Data\{profile}\Cache` with marker
	// "Preferences" matches Default, Profile 1, ... but not unrelated folders.
	Roots []string
	// ProfileMarker identifies real profile directories for `{profile}`.
	ProfileMarker string
	// ProfileExclude lists directory names `{profile}` never expands to.
	ProfileExclude []string

	// Special names a non-file target handled by Env.Specials (for example
	// the Recycle Bin, which is emptied through the Shell API). Special rules
	// have no Roots.
	Special string

	// Include, when non-empty, limits candidates to files whose name matches
	// one of these patterns (filepath.Match syntax, case-insensitive).
	Include []string
	// Exclude skips files and directories whose name matches.
	Exclude []string
	// MaxDepth limits how deep below the root files are collected
	// (0 = unlimited, 1 = direct children only).
	MaxDepth int

	// MinAge only selects items whose creation AND last-write time are at
	// least this old.
	MinAge time.Duration

	// User-facing explanation. All three are required.
	What    string // what is removed
	WhySafe string // why removing it is safe
	Impact  string // what happens afterwards

	RequiresAdmin   bool
	DefaultSelected bool

	// AppProcesses lists executable names (e.g. "Discord.exe") whose data
	// this rule touches. When one is running the rule is skipped, because the
	// application may be using its cache.
	AppProcesses []string
	// DetectPaths are templates; the rule only applies if at least one
	// exists. Used to avoid offering cleaners for software that is absent.
	DetectPaths []string
}

var ruleID = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)+$`)

const profileToken = "{profile}"

// Validate checks a rule definition for mistakes that could make it unsafe
// or confusing. The test suite runs it over every built-in rule.
func (r *Rule) Validate() error {
	if !ruleID.MatchString(r.ID) {
		return fmt.Errorf("rule %q: ID must be lowercase dotted (e.g. temp.user)", r.ID)
	}
	if r.Name == "" || r.What == "" || r.WhySafe == "" || r.Impact == "" {
		return fmt.Errorf("rule %s: Name, What, WhySafe and Impact are required", r.ID)
	}
	if r.Special != "" {
		if len(r.Roots) > 0 {
			return fmt.Errorf("rule %s: special rules have no roots", r.ID)
		}
		return nil
	}
	if len(r.Roots) == 0 {
		return fmt.Errorf("rule %s: at least one root is required", r.ID)
	}
	for _, root := range r.Roots {
		if !strings.HasPrefix(root, "{") || strings.HasPrefix(root, profileToken) {
			return fmt.Errorf("rule %s: root %q must start with a location token like {LocalAppData}", r.ID, root)
		}
		if strings.ContainsAny(root, "*?") || strings.Contains(root, "..") {
			return fmt.Errorf("rule %s: root %q must not contain wildcards or '..'", r.ID, root)
		}
		rest := root[strings.Index(root, "}")+1:]
		if strings.Trim(rest, `\`) == "" && root != "{Temp}" && root != "{WindowsTemp}" {
			return fmt.Errorf("rule %s: root %q is a bare location; target a specific subfolder", r.ID, root)
		}
		switch n := strings.Count(root, profileToken); {
		case n > 1:
			return fmt.Errorf("rule %s: root %q has more than one {profile}", r.ID, root)
		case n == 1 && r.ProfileMarker == "":
			return fmt.Errorf("rule %s: root %q uses {profile} without a ProfileMarker", r.ID, root)
		case n == 1 && !strings.Contains(root, `\`+profileToken+`\`):
			return fmt.Errorf("rule %s: {profile} must be a whole middle path component in %q", r.ID, root)
		}
	}
	for _, pat := range append(append([]string{}, r.Include...), r.Exclude...) {
		if _, err := filepath.Match(pat, "x"); err != nil || strings.ContainsAny(pat, `\/`) {
			return fmt.Errorf("rule %s: invalid name pattern %q", r.ID, pat)
		}
	}
	return nil
}

func matchAny(patterns []string, name string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := filepath.Match(strings.ToLower(p), lower); ok {
			return true
		}
	}
	return false
}

// MatchesID reports whether a whitelist entry refers to this rule: either the
// exact ID or a dotted prefix of it ("browser" matches "browser.chrome.cache").
func (r *Rule) MatchesID(entry string) bool {
	entry = strings.ToLower(strings.TrimSpace(entry))
	return entry != "" && (r.ID == entry || strings.HasPrefix(r.ID, entry+"."))
}
