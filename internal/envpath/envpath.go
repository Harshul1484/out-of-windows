// Package envpath analyzes the user and machine PATH environment variables
// (missing folders, duplicates, length) and rewrites the user PATH for
// `oow repair`. The machine PATH is only ever read.
package envpath

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/Harshul1484/out-of-windows/internal/system"
)

// Scope is which PATH variable.
type Scope string

// Scopes.
const (
	User    Scope = "user"
	Machine Scope = "machine"
)

// Value is a PATH variable as stored in the registry.
type Value struct {
	Raw string `json:"raw"`
	// Expand is true for REG_EXPAND_SZ values, whose %VARIABLES% Windows
	// expands when building the environment.
	Expand bool `json:"expandable"`
	// Exists is false when the variable is not defined at all.
	Exists bool `json:"exists"`
}

// Problem is what is wrong with one PATH entry.
type Problem string

// Problems.
const (
	ProblemNone      Problem = ""
	ProblemMissing   Problem = "missing"   // the folder does not exist
	ProblemDuplicate Problem = "duplicate" // an earlier entry names the same folder
	ProblemEmpty     Problem = "empty"     // an empty entry (";;")
)

// Entry is one element of a PATH value.
type Entry struct {
	Index    int             `json:"index"`
	Raw      string          `json:"raw"`
	Expanded string          `json:"expanded"`
	State    system.Presence `json:"state"`
	Problem  Problem         `json:"problem,omitempty"`
	// DuplicateOf is the index of the earlier entry this one repeats.
	DuplicateOf int `json:"duplicate_of,omitempty"`
	// Note explains an unknown state or why a fix needs review.
	Note string `json:"note,omitempty"`
	// Review is set for missing folders inside the user profile: tools
	// often add their bin folder to PATH before creating it (go\bin,
	// .dotnet\tools), so removing it could break a later install.
	Review bool `json:"review,omitempty"`
}

// Report is the analysis of one PATH variable.
type Report struct {
	Scope   Scope   `json:"scope"`
	Value   Value   `json:"value"`
	Entries []Entry `json:"entries"`
	// Length is the stored value's length in UTF-16 characters, and
	// ExpandedLength the length after expanding %VARIABLES%.
	Length         int `json:"length"`
	ExpandedLength int `json:"expanded_length"`
	Missing        int `json:"missing"`
	Duplicates     int `json:"duplicates"`
	Empty          int `json:"empty"`
	Unknown        int `json:"unknown"`
	// Quoted is set when an entry contains double quotes; such values are
	// analyzed but never rewritten, because quotes may hide a ";".
	Quoted bool `json:"quoted"`
	// Error is set when the value could not be read.
	Error string `json:"error,omitempty"`
}

// Issues is the number of entries with a problem.
func (r *Report) Issues() int { return r.Missing + r.Duplicates + r.Empty }

// Options configure Analyze.
type Options struct {
	// Expand expands %VARIABLES%; ok is false when a variable is undefined.
	Expand func(string) (string, bool)
	// Probe reports whether a folder exists (system.ProbePath).
	Probe func(string) (system.Presence, string)
	// Profile is the user profile folder (missing entries inside it need
	// review rather than removal).
	Profile string
}

// Split splits a PATH value into entries exactly as stored.
func Split(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ";")
}

// Analyze inspects every entry of v.
func Analyze(scope Scope, v Value, o Options) Report {
	r := Report{Scope: scope, Value: v, Entries: []Entry{}, Length: utf16Len(v.Raw)}
	seen := map[string]int{}
	var expandedParts []string
	for i, raw := range Split(v.Raw) {
		e := Entry{Index: i, Raw: raw, State: system.Unknown}
		clean := strings.TrimSpace(raw)
		if strings.Contains(clean, `"`) {
			r.Quoted = true
			clean = strings.ReplaceAll(clean, `"`, "")
		}
		if clean == "" {
			// A trailing ";" is ubiquitous and harmless; only empty entries
			// between others are worth reporting.
			if i == len(Split(v.Raw))-1 && i > 0 {
				continue
			}
			e.Problem, e.State = ProblemEmpty, system.Unknown
			r.Empty++
			r.Entries = append(r.Entries, e)
			continue
		}
		exp, ok := clean, true
		if strings.Contains(clean, "%") && o.Expand != nil {
			exp, ok = o.Expand(clean)
		}
		e.Expanded = exp
		expandedParts = append(expandedParts, exp)
		if !ok {
			e.Note = "uses an environment variable that is not set"
		} else {
			e.State, e.Note = o.Probe(exp)
		}
		key := normalize(exp)
		if first, dup := seen[key]; dup && ok {
			e.Problem, e.DuplicateOf = ProblemDuplicate, first
			r.Duplicates++
		} else {
			if ok {
				seen[key] = i
			}
			switch e.State {
			case system.Absent:
				e.Problem = ProblemMissing
				r.Missing++
				if o.Profile != "" && within(exp, o.Profile) {
					e.Review = true
					e.Note = "inside your profile: tools often add a folder here before creating it"
				}
			case system.Unknown:
				r.Unknown++
			}
		}
		r.Entries = append(r.Entries, e)
	}
	r.ExpandedLength = utf16Len(strings.Join(expandedParts, ";"))
	return r
}

// CombinedLength is the length of the PATH a new process receives: the
// expanded machine PATH, a separator and the expanded user PATH.
func CombinedLength(machine, user Report) int {
	n := machine.ExpandedLength + user.ExpandedLength
	if machine.ExpandedLength > 0 && user.ExpandedLength > 0 {
		n++
	}
	return n
}

func normalize(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "/", `\`))
	if len(p) > 3 {
		p = strings.TrimRight(p, `\`)
	}
	if filepath.IsAbs(p) {
		p = filepath.Clean(p)
	}
	return strings.ToLower(p)
}

func within(p, dir string) bool {
	p, dir = normalize(p), normalize(dir)
	return dir != "" && strings.HasPrefix(p, dir+`\`)
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// Remove returns v without the entries at the given indexes. Everything else
// (order, spelling, unexpanded variables, the value type) is kept exactly.
func Remove(v Value, indexes []int) Value {
	drop := map[int]bool{}
	for _, i := range indexes {
		drop[i] = true
	}
	var keep []string
	for i, part := range Split(v.Raw) {
		if !drop[i] {
			keep = append(keep, part)
		}
	}
	return Value{Raw: strings.Join(keep, ";"), Expand: v.Expand, Exists: v.Exists}
}

// ErrChanged is returned when the PATH changed after it was read.
var ErrChanged = errors.New("PATH was changed by another program after it was read")

// Store reads both PATH variables and writes the user PATH.
type Store interface {
	Read(scope Scope) (Value, error)
	// WriteUser replaces the user PATH with updated, but only if it still
	// equals expected (otherwise ErrChanged), and reads it back.
	WriteUser(expected, updated Value) error
	// Broadcast tells running programs (Explorer) that the environment
	// changed, so newly started programs see the new PATH.
	Broadcast() error
	// Expand expands %VARIABLES% in s; ok is false if any is undefined.
	Expand(s string) (string, bool)
}
