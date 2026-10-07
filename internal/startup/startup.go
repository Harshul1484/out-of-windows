// Package startup lists the programs Windows starts when the user signs in
// (Run and RunOnce registry values, the user and common Startup folders) and
// enables or disables them the way Task Manager does: by writing the
// Explorer\StartupApproved value that belongs to the entry. Disabling is
// reversible and never deletes or edits the entry itself.
//
// # StartupApproved values
//
// Explorer keeps one REG_BINARY value per entry, named like the Run value or
// the file in the Startup folder, under
//
//	HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run            HKCU Run
//	HKCU\...\StartupApproved\StartupFolder                                                 user Startup folder
//	HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run            HKLM Run (64-bit view)
//	HKLM\...\StartupApproved\Run32                                                          HKLM Run (32-bit view)
//	HKLM\...\StartupApproved\StartupFolder                                                 common Startup folder
//
// The value is 12 bytes: a 4-byte little-endian flag followed by a FILETIME.
// Task Manager writes 02 00 00 00 and a zero FILETIME to enable an entry, and
// 03 00 00 00 with the time of the change to disable it. Other writers use 06
// (enabled) and 07 or 01 (disabled): Explorer treats an odd first byte as
// disabled. A missing value means enabled. RunOnce entries have no approval
// value; Windows deletes them after running them once.
package startup

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/system"
)

// Source is where an entry is registered.
type Source string

// Sources.
const (
	SourceHKCURun       Source = "hkcu-run"
	SourceHKCURunOnce   Source = "hkcu-runonce"
	SourceHKLMRun       Source = "hklm-run"
	SourceHKLMRun32     Source = "hklm-run32"
	SourceHKLMRunOnce   Source = "hklm-runonce"
	SourceHKLMRunOnce32 Source = "hklm-runonce32"
	SourceUserFolder    Source = "startup-folder"
	SourceCommonFolder  Source = "common-startup-folder"
)

// SourceOrder lists every source in display order.
var SourceOrder = []Source{
	SourceHKCURun, SourceUserFolder, SourceHKCURunOnce,
	SourceHKLMRun, SourceHKLMRun32, SourceCommonFolder, SourceHKLMRunOnce, SourceHKLMRunOnce32,
}

const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	runOnceKey  = `Software\Microsoft\Windows\CurrentVersion\RunOnce`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved`
)

type sourceInfo struct {
	label    string // short human label
	location string // registry key or folder kind, for display
	machine  bool   // applies to every user; changes need administrator rights
	approval string // StartupApproved subkey; "" when the source has none
}

var sources = map[Source]sourceInfo{
	SourceHKCURun:       {"Registry (this user)", `HKCU\` + runKey, false, "Run"},
	SourceHKCURunOnce:   {"Registry, once (this user)", `HKCU\` + runOnceKey, false, ""},
	SourceHKLMRun:       {"Registry (all users)", `HKLM\` + runKey, true, "Run"},
	SourceHKLMRun32:     {"Registry, 32-bit (all users)", `HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Run`, true, "Run32"},
	SourceHKLMRunOnce:   {"Registry, once (all users)", `HKLM\` + runOnceKey, true, ""},
	SourceHKLMRunOnce32: {"Registry, once, 32-bit (all users)", `HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\RunOnce`, true, ""},
	SourceUserFolder:    {"Startup folder (this user)", "", false, "StartupFolder"},
	SourceCommonFolder:  {"Startup folder (all users)", "", true, "StartupFolder"},
}

// Label is a short description of the source.
func (s Source) Label() string { return sources[s].label }

// Machine reports whether the source applies to all users.
func (s Source) Machine() bool { return sources[s].machine }

// Folder reports whether the source is a Startup folder.
func (s Source) Folder() bool { return s == SourceUserFolder || s == SourceCommonFolder }

// ApprovalKey returns the StartupApproved key (as HKCU\... or HKLM\...) that
// holds the enabled/disabled state of entries from s, or "" when the source
// has no such state (RunOnce).
func (s Source) ApprovalKey() string {
	info, ok := sources[s]
	if !ok || info.approval == "" {
		return ""
	}
	root := `HKCU\`
	if info.machine {
		root = `HKLM\`
	}
	return root + approvedKey + `\` + info.approval
}

// State is whether an entry runs at sign-in.
type State string

// States.
const (
	Enabled  State = "enabled"
	Disabled State = "disabled"
	RunsOnce State = "runs-once" // RunOnce: runs at the next sign-in, then Windows removes it
)

// Approval is the StartupApproved value behind an entry's state.
type Approval struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Data is the value's bytes in hex, or "" when the value does not exist
	// (which means enabled).
	Data string `json:"data"`
}

// Entry is one startup program.
type Entry struct {
	// ID is stable for the entry: "<source>:<name>".
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source Source `json:"source"`
	// SourceLabel describes Source for people.
	SourceLabel string `json:"source_label"`
	Scope       string `json:"scope"` // "user" or "machine"
	// Location is the registry key holding the entry, or the file in the
	// Startup folder.
	Location string `json:"location"`
	// Command is what runs: the Run value, or the shortcut's target and
	// arguments (or the file itself).
	Command string `json:"command"`
	// Target is the program file the command starts, when it could be
	// determined, and TargetState whether it exists.
	Target      string          `json:"target"`
	TargetState system.Presence `json:"target_state"`
	TargetNote  string          `json:"target_note,omitempty"`
	State       State           `json:"state"`
	DisabledAt  *time.Time      `json:"disabled_at,omitempty"`
	Approval    *Approval       `json:"approval,omitempty"`
	// Toggleable reports whether oow can enable or disable the entry.
	Toggleable bool `json:"toggleable"`
	// NeedsAdmin reports whether changing it needs administrator rights.
	NeedsAdmin bool `json:"needs_admin"`
}

// Broken reports whether the program an entry starts is missing. RunOnce
// entries are never reported: they cannot be disabled and run only once.
func (e Entry) Broken() bool {
	return e.TargetState == system.Absent && e.State != RunsOnce
}

// ID returns the stable ID of an entry.
func ID(s Source, name string) string { return string(s) + ":" + name }

// ParseApproval interprets a StartupApproved value: an odd first byte means
// disabled, and a disabled value carries the time it was disabled in bytes
// 4-11. Known reports whether the first byte is one of the values Windows is
// known to write (01, 02, 03, 06, 07); unknown values are still interpreted by
// the odd/even rule. An empty value counts as enabled.
func ParseApproval(b []byte) (enabled bool, disabledAt time.Time, known bool) {
	if len(b) == 0 {
		return true, time.Time{}, false
	}
	switch b[0] {
	case 0x01, 0x02, 0x03, 0x06, 0x07:
		known = true
	}
	enabled = b[0]&1 == 0
	if !enabled && len(b) >= 12 {
		if ft := binary.LittleEndian.Uint64(b[4:12]); ft != 0 {
			disabledAt = FiletimeToTime(ft)
		}
	}
	return enabled, disabledAt, known
}

// ApprovalData returns the value Task Manager writes to enable an entry
// (02 00 00 00 and a zero FILETIME) or to disable it (03 00 00 00 and the
// FILETIME of now).
func ApprovalData(enabled bool, now time.Time) []byte {
	b := make([]byte, 12)
	if enabled {
		b[0] = 0x02
		return b
	}
	b[0] = 0x03
	binary.LittleEndian.PutUint64(b[4:], TimeToFiletime(now))
	return b
}

// filetimeEpochDelta is the number of 100 ns intervals between 1601-01-01
// (the FILETIME epoch) and 1970-01-01.
const filetimeEpochDelta = 116444736000000000

// TimeToFiletime converts t to a Windows FILETIME value.
func TimeToFiletime(t time.Time) uint64 {
	return uint64(t.UnixNano()/100) + filetimeEpochDelta
}

// FiletimeToTime converts a Windows FILETIME value to UTC time.
func FiletimeToTime(ft uint64) time.Time {
	if ft < filetimeEpochDelta {
		return time.Time{}
	}
	return time.Unix(0, int64(ft-filetimeEpochDelta)*100).UTC()
}

// Raw is an entry as read from the registry or a Startup folder, before
// interpretation. Stores produce it; Build turns it into an Entry.
type Raw struct {
	Source Source
	Name   string // Run value name, or file name in the Startup folder
	// Command is the Run value (registry sources).
	Command string
	// File is the full path of the file (folder sources); Link is the parsed
	// shortcut when the file is a .lnk, LinkErr why it could not be parsed.
	File    string
	Link    *Link
	LinkErr error
	// Approval is the StartupApproved value, nil when there is none.
	Approval []byte
}

// Resolver finds the program a command starts and whether it exists.
type Resolver struct {
	// Expand expands %VARIABLES%.
	Expand func(string) string
	// Probe reports whether an absolute path exists (system.ProbePath).
	Probe func(string) (system.Presence, string)
	// Search are the folders Windows searches for a bare program name before
	// PATH (the System32 and Windows folders).
	Search []string
}

func (r Resolver) expand(s string) string {
	if r.Expand == nil {
		return s
	}
	return r.Expand(s)
}

// CommandTarget returns the program a command line starts and whether it
// exists. It never reports Absent unless an absolute path was named and is
// verifiably missing: bare names that are not in the System32 or Windows
// folder are found through PATH or App Paths at run time, so they are Unknown.
func (r Resolver) CommandTarget(cmdline string) (string, system.Presence, string) {
	cmdline = strings.TrimSpace(r.expand(strings.TrimSpace(cmdline)))
	cmdline = strings.TrimPrefix(cmdline, `\??\`)
	if cmdline == "" {
		return "", system.Unknown, "empty command"
	}
	exists := func(p string) bool {
		if !filepath.IsAbs(p) {
			return false
		}
		st, _ := r.Probe(p)
		return st == system.Present
	}
	cmd, err := apps.ParseCommandLine(cmdline, exists)
	if err != nil {
		return "", system.Unknown, err.Error()
	}
	exe := strings.TrimSpace(cmd.Exe)
	if !strings.ContainsAny(exe, `\/`) {
		for _, dir := range r.Search {
			for _, name := range []string{exe, exe + ".exe"} {
				p := filepath.Join(dir, name)
				if st, _ := r.Probe(p); st == system.Present {
					return r.hosted(p, cmd.Args)
				}
			}
		}
		return exe, system.Unknown, "found through PATH when it runs (not checked)"
	}
	if !filepath.IsAbs(exe) {
		return exe, system.Unknown, "relative path (not checked)"
	}
	st, note := r.Probe(exe)
	if st == system.Absent && !strings.HasPrefix(cmdline, `"`) {
		// An unquoted path with spaces whose program is gone: name the
		// whole program path rather than its first word.
		if g := guessExe(cmdline); g != "" && filepath.IsAbs(g) {
			exe = g
			st, note = r.Probe(exe)
		}
	}
	if st != system.Present {
		return exe, st, note
	}
	return r.hosted(exe, cmd.Args)
}

// guessExe returns the command line up to the first ".exe" that ends a word.
func guessExe(cmdline string) string {
	lower := strings.ToLower(cmdline)
	for i := 0; ; {
		j := strings.Index(lower[i:], ".exe")
		if j < 0 {
			return ""
		}
		end := i + j + 4
		if end == len(lower) || lower[end] == ' ' || lower[end] == '\t' {
			return cmdline[:end]
		}
		i = end
	}
}

// hosted looks through rundll32, which starts a DLL named in its first
// argument: when the DLL is gone the entry is broken even though rundll32
// exists.
func (r Resolver) hosted(exe string, args []string) (string, system.Presence, string) {
	if !strings.EqualFold(filepath.Base(exe), "rundll32.exe") || len(args) == 0 {
		return exe, system.Present, ""
	}
	dll, _, _ := strings.Cut(strings.Join(args, " "), ",")
	dll = strings.Trim(strings.TrimSpace(dll), `"`)
	if !filepath.IsAbs(dll) {
		return exe, system.Present, ""
	}
	st, note := r.Probe(dll)
	return dll, st, note
}

// Build interprets raw entries: state from the approval value, the program
// each one starts and whether it exists.
func Build(raws []Raw, r Resolver) []Entry {
	out := make([]Entry, 0, len(raws))
	for _, raw := range raws {
		info := sources[raw.Source]
		e := Entry{
			ID:          ID(raw.Source, raw.Name),
			Name:        raw.Name,
			Source:      raw.Source,
			SourceLabel: info.label,
			Scope:       "user",
			Location:    info.location,
			NeedsAdmin:  info.machine,
		}
		if info.machine {
			e.Scope = "machine"
		}
		if raw.Source.Folder() {
			e.Location = raw.File
			if e.Name == "" {
				e.Name = filepath.Base(raw.File)
			}
		}

		// State.
		if key := raw.Source.ApprovalKey(); key != "" {
			e.Toggleable = true
			e.State = Enabled
			e.Approval = &Approval{Key: key, Value: raw.Name}
			if raw.Approval != nil {
				e.Approval.Data = hex.EncodeToString(raw.Approval)
				enabled, at, _ := ParseApproval(raw.Approval)
				if !enabled {
					e.State = Disabled
					if !at.IsZero() {
						e.DisabledAt = &at
					}
				}
			}
		} else {
			e.State = RunsOnce
		}

		// Command and target.
		switch {
		case !raw.Source.Folder():
			e.Command = raw.Command
			e.Target, e.TargetState, e.TargetNote = r.CommandTarget(raw.Command)
		case raw.Link != nil:
			target := raw.Link.TargetPath(filepath.Dir(raw.File), r.expand)
			e.Command = strings.TrimSpace(quoteIfSpaced(target) + " " + raw.Link.Arguments)
			switch {
			case target == "":
				e.TargetState, e.TargetNote = system.Unknown, "the shortcut points to a shell item, not a file"
			case raw.Link.Network:
				e.Target, e.TargetState, e.TargetNote = target, system.Unknown, "network path (not checked)"
			default:
				e.Target, e.TargetState, e.TargetNote = r.CommandTarget(quoteIfSpaced(target) + " " + raw.Link.Arguments)
			}
		case raw.LinkErr != nil:
			e.Command = raw.File
			e.TargetState, e.TargetNote = system.Unknown, "the shortcut could not be read: "+raw.LinkErr.Error()
		default:
			// Any other file in the Startup folder is opened as it is.
			e.Command, e.Target, e.TargetState = raw.File, raw.File, system.Present
		}
		out = append(out, e)
	}
	order := map[Source]int{}
	for i, s := range SourceOrder {
		order[s] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return order[out[i].Source] < order[out[j].Source]
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func quoteIfSpaced(p string) string {
	if strings.ContainsAny(p, " \t") && !strings.HasPrefix(p, `"`) {
		return `"` + p + `"`
	}
	return p
}

// Find returns the entries a user's query names: an exact ID, else an exact
// name (case-insensitive), else every entry whose name contains the query.
func Find(entries []Entry, query string) []Entry {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	for _, e := range entries {
		if strings.EqualFold(e.ID, q) {
			return []Entry{e}
		}
	}
	var exact, partial []Entry
	lq := strings.ToLower(q)
	for _, e := range entries {
		switch {
		case strings.EqualFold(e.Name, q):
			exact = append(exact, e)
		case strings.Contains(strings.ToLower(e.Name), lq):
			partial = append(partial, e)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

// Store reads startup entries and writes their StartupApproved values.
type Store interface {
	// List returns every entry. It changes nothing. Warnings describe
	// sources that could not be read.
	List(ctx context.Context) (entries []Entry, warnings []string, err error)
	// SetApproval writes the StartupApproved value for e, after checking that
	// the entry still exists. It never changes the entry itself.
	SetApproval(e Entry, data []byte) error
}

// ErrEntryGone is returned when an entry disappeared after it was listed.
var ErrEntryGone = errors.New("the entry no longer exists")

// Result is the outcome of enabling or disabling one entry.
type Result struct {
	Entry  Entry  `json:"entry"`
	Status string `json:"status"` // changed, unchanged, skipped, failed
	Reason string `json:"reason,omitempty"`
	// Before and After are the approval values (hex) around the change.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Result statuses.
const (
	StatusChanged   = "changed"
	StatusUnchanged = "unchanged"
	StatusSkipped   = "skipped"
	StatusFailed    = "failed"
	StatusPlanned   = "planned" // dry run: would change
)

// ReasonNeedsAdmin explains admin-only entries in a non-elevated process.
const ReasonNeedsAdmin = "requires administrator: it starts for all users; run oow from an elevated terminal"

// Plan reports what SetEnabled would do for each target, without changing
// anything (statuses planned, unchanged or skipped).
func Plan(targets []Entry, enabled, elevated bool) []Result {
	out := make([]Result, 0, len(targets))
	for _, e := range targets {
		out = append(out, plan1(e, enabled, elevated))
	}
	return out
}

func plan1(e Entry, enabled, elevated bool) Result {
	r := Result{Entry: e, Status: StatusPlanned}
	if e.Approval != nil {
		r.Before = e.Approval.Data
	}
	want := Disabled
	if enabled {
		want = Enabled
	}
	switch {
	case !e.Toggleable:
		r.Status, r.Reason = StatusSkipped, "runs once at the next sign-in and cannot be disabled; Windows removes it after it runs"
	case e.State == want:
		r.Status, r.Reason = StatusUnchanged, "already "+string(want)
	case e.NeedsAdmin && !elevated:
		r.Status, r.Reason = StatusSkipped, ReasonNeedsAdmin
	}
	return r
}

// SetEnabled enables or disables targets through their StartupApproved
// values and verifies each change by listing the entries again.
func SetEnabled(ctx context.Context, s Store, targets []Entry, enabled, elevated bool, now time.Time) []Result {
	results := Plan(targets, enabled, elevated)
	data := ApprovalData(enabled, now)
	changed := false
	for i := range results {
		r := &results[i]
		if r.Status != StatusPlanned {
			continue
		}
		if ctx.Err() != nil {
			r.Status, r.Reason = StatusSkipped, "cancelled"
			continue
		}
		if err := s.SetApproval(r.Entry, data); err != nil {
			r.Status, r.Reason = StatusFailed, err.Error()
			continue
		}
		r.Status, r.After = StatusChanged, hex.EncodeToString(data)
		changed = true
	}
	if !changed {
		return results
	}
	// Verify: the state must now read back as requested.
	now2, _, err := s.List(context.WithoutCancel(ctx))
	byID := map[string]Entry{}
	for _, e := range now2 {
		byID[strings.ToLower(e.ID)] = e
	}
	want := Disabled
	if enabled {
		want = Enabled
	}
	for i := range results {
		r := &results[i]
		if r.Status != StatusChanged {
			continue
		}
		e, ok := byID[strings.ToLower(r.Entry.ID)]
		switch {
		case err != nil:
			r.Status, r.Reason = StatusFailed, fmt.Sprintf("written, but could not be verified: %v", err)
		case !ok:
			r.Status, r.Reason = StatusFailed, "written, but the entry is no longer listed"
		case e.State != want:
			r.Status, r.Reason = StatusFailed, "written, but Windows still reports it "+string(e.State)
		default:
			r.Entry = e
		}
	}
	return results
}

// ReadFolder reads the files of a Startup folder (desktop.ini and folders are
// ignored; Explorer does not run them). Shortcuts are parsed with ansi for
// strings stored in the ANSI code page; approval returns the StartupApproved
// value for a file name. A missing folder has no entries.
func ReadFolder(src Source, dir string, ansi func([]byte) (string, bool), approval func(string) []byte) ([]Raw, error) {
	list, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Raw
	for _, de := range list {
		if de.IsDir() || strings.EqualFold(de.Name(), "desktop.ini") {
			continue
		}
		path := filepath.Join(dir, de.Name())
		raw := Raw{Source: src, Name: de.Name(), File: path, Approval: approval(de.Name())}
		if strings.EqualFold(filepath.Ext(path), ".lnk") {
			raw.Link, raw.LinkErr = readLink(path, ansi)
		}
		out = append(out, raw)
	}
	return out, nil
}

func readLink(path string, ansi func([]byte) (string, bool)) (*Link, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLinkSize+1))
	if err != nil {
		return nil, err
	}
	return ParseLink(data, ansi)
}
