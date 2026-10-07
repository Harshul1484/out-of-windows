// Package tasks reads Windows scheduled tasks (Task Scheduler 2.0) and turns
// one task's Enabled flag on or off. Listing is read-only. SetEnabled is the
// only write: it is what Task Scheduler's own Disable and Enable commands do,
// it is reversed by setting the flag back, and it never creates, edits or
// deletes a task.
package tasks

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Trigger is the kind of event that starts a task.
type Trigger string

// Trigger kinds. Only enabled triggers are reported.
const (
	TriggerLogon        Trigger = "logon"        // when a user signs in
	TriggerBoot         Trigger = "boot"         // when Windows starts
	TriggerTime         Trigger = "time"         // once, at a time
	TriggerCalendar     Trigger = "calendar"     // daily, weekly or monthly
	TriggerIdle         Trigger = "idle"         // when the computer is idle
	TriggerEvent        Trigger = "event"        // on an event log entry
	TriggerRegistration Trigger = "registration" // when the task is registered
	TriggerSession      Trigger = "session"      // on lock, unlock, connect or disconnect
	TriggerOther        Trigger = "other"
)

var triggerKinds = map[string]Trigger{
	"LogonTrigger":              TriggerLogon,
	"BootTrigger":               TriggerBoot,
	"TimeTrigger":               TriggerTime,
	"CalendarTrigger":           TriggerCalendar,
	"IdleTrigger":               TriggerIdle,
	"EventTrigger":              TriggerEvent,
	"RegistrationTrigger":       TriggerRegistration,
	"SessionStateChangeTrigger": TriggerSession,
}

// Action is a program a task starts (an Exec action).
type Action struct {
	Command          string `json:"command"`
	Arguments        string `json:"arguments,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
}

// Task is a registered scheduled task, as far as oow needs to know it.
type Task struct {
	// Path is the task's full path in the Task Scheduler library, such as
	// `\Contoso\Updater` (always starting with a backslash).
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	// Triggers lists the kinds of the task's enabled triggers.
	Triggers []Trigger `json:"triggers"`
	// LogonUser is the account whose sign-in starts the task, "" when any
	// user's sign-in does (or the task has no logon trigger).
	LogonUser string `json:"logon_user,omitempty"`
	// UserID or GroupID is the principal the task runs as.
	UserID  string `json:"user_id,omitempty"`
	GroupID string `json:"group_id,omitempty"`
	// HighestPrivileges is set when the task runs with the highest privileges
	// available to its account (RunLevel HighestAvailable).
	HighestPrivileges bool     `json:"highest_privileges,omitempty"`
	Actions           []Action `json:"actions"`
	// OtherActions counts actions that do not start a program file (COM
	// handlers, e-mail and message actions).
	OtherActions int `json:"other_actions,omitempty"`
}

// Name is the last component of the task's path.
func (t Task) Name() string {
	p := strings.TrimRight(t.Path, `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Has reports whether one of the task's enabled triggers is of kind k.
func (t Task) Has(k Trigger) bool {
	for _, x := range t.Triggers {
		if x == k {
			return true
		}
	}
	return false
}

// Windows reports whether the task belongs to Windows itself (the
// \Microsoft\ folder of the library).
func (t Task) Windows() bool {
	return strings.HasPrefix(strings.ToLower(t.Path), `\microsoft\`)
}

// Account identifies the user oow runs as.
type Account struct {
	SID    string `json:"sid"`
	Name   string `json:"name"`
	Domain string `json:"domain,omitempty"`
}

// Matches reports whether id, a task principal or logon-trigger user as
// stored in a task (a SID, "name", "DOMAIN\name" or ".\name"), names a.
func (a Account) Matches(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if a.SID != "" && strings.EqualFold(id, a.SID) {
		return true
	}
	if a.Name == "" {
		return false
	}
	if strings.EqualFold(id, a.Name) || strings.EqualFold(id, `.\`+a.Name) {
		return true
	}
	return a.Domain != "" && strings.EqualFold(id, a.Domain+`\`+a.Name)
}

// OwnedBy reports whether t is the account's own task: it runs as that
// account (not a group or a service account), without the highest
// privileges, and when it has logon triggers, only at that account's sign-in.
// Such tasks are registered by the user and can be switched on and off
// without administrator rights; every other task needs them.
func (t Task) OwnedBy(a Account) bool {
	if t.GroupID != "" || t.HighestPrivileges || !a.Matches(t.UserID) {
		return false
	}
	if t.Has(TriggerLogon) && !a.Matches(t.LogonUser) {
		return false
	}
	return true
}

// CommandLine is the action as one command line: the program (quoted when
// it contains spaces) followed by its arguments.
func (a Action) CommandLine() string {
	cmd := strings.TrimSpace(a.Command)
	if cmd != "" && !strings.HasPrefix(cmd, `"`) && strings.ContainsAny(cmd, " \t") {
		cmd = `"` + cmd + `"`
	}
	return strings.TrimSpace(cmd + " " + strings.TrimSpace(a.Arguments))
}

// maxArgPaths bounds how many argument tokens are examined per action.
const maxArgPaths = 64

// Paths returns the absolute local paths an action refers to: its program,
// its working directory, and absolute paths inside its arguments (for
// example the script a host program runs, or the DLL rundll32 loads), with
// %VARIABLES% expanded by expand. Bare program names, relative paths and
// network paths are left out.
func (a Action) Paths(expand func(string) string) []string {
	var out []string
	for _, p := range []string{a.Program(expand), a.WorkingDir(expand)} {
		if p != "" {
			out = append(out, p)
		}
	}
	return append(out, a.ArgumentPaths(expand)...)
}

// Program is the absolute local path of the program, or "" when the
// command is a bare name, relative or on a network.
func (a Action) Program(expand func(string) string) string { return localAbs(a.Command, expand) }

// WorkingDir is the absolute local working directory, or "".
func (a Action) WorkingDir(expand func(string) string) string {
	return localAbs(a.WorkingDirectory, expand)
}

// ArgumentPaths are the absolute local paths inside the arguments.
func (a Action) ArgumentPaths(expand func(string) string) []string {
	var out []string
	for i, tok := range splitArgs(expandWith(expand, a.Arguments)) {
		if i >= maxArgPaths {
			break
		}
		if p := localAbs(absPathIn(tok), nil); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func expandWith(expand func(string) string, s string) string {
	if expand == nil {
		return s
	}
	return expand(s)
}

func localAbs(p string, expand func(string) string) string {
	p = strings.Trim(strings.TrimSpace(expandWith(expand, p)), `"`)
	if !isLocalAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

func isLocalAbs(p string) bool {
	return len(p) >= 3 && isLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// absPathIn returns the absolute local path that starts inside an argument
// token ("C:\x", "/log:C:\x", "--config=C:\x"), up to a comma or semicolon
// ("C:\x.dll,Entry" for rundll32), or "".
func absPathIn(tok string) string {
	for i := 0; i+2 < len(tok); i++ {
		if !isLocalAbs(tok[i:]) {
			continue
		}
		if i > 0 && (isLetter(tok[i-1]) || tok[i-1] >= '0' && tok[i-1] <= '9') {
			continue
		}
		p := tok[i:]
		if j := strings.IndexAny(p, ",;"); j >= 0 {
			p = p[:j]
		}
		return p
	}
	return ""
}

// splitArgs splits a command line's arguments, keeping double-quoted parts
// together and dropping the quotes.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			have = true
		case (r == ' ' || r == '\t') && !inQuote:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

// maxXMLSize bounds a task definition; real ones are a few kilobytes.
const maxXMLSize = 1 << 20

type xmlTask struct {
	Triggers struct {
		Items []xmlTrigger `xml:",any"`
	} `xml:"Triggers"`
	Principals struct {
		Items []xmlPrincipal `xml:"Principal"`
	} `xml:"Principals"`
	Settings struct {
		Enabled *string `xml:"Enabled"`
	} `xml:"Settings"`
	Actions struct {
		Items []xmlAction `xml:",any"`
	} `xml:"Actions"`
}

type xmlTrigger struct {
	XMLName xml.Name
	Enabled *string `xml:"Enabled"`
	UserID  string  `xml:"UserId"`
}

type xmlPrincipal struct {
	UserID   string `xml:"UserId"`
	GroupID  string `xml:"GroupId"`
	RunLevel string `xml:"RunLevel"`
}

type xmlAction struct {
	XMLName          xml.Name
	Command          string `xml:"Command"`
	Arguments        string `xml:"Arguments"`
	WorkingDirectory string `xml:"WorkingDirectory"`
}

// ParseXML reads a task definition in the Task Scheduler schema, as returned
// by IRegisteredTask.Xml. The text has already been decoded (the "UTF-16"
// encoding it declares is ignored). Path is left empty for the caller.
func ParseXML(text string) (Task, error) {
	if len(text) > maxXMLSize {
		return Task{}, errors.New("task definition is too large")
	}
	dec := xml.NewDecoder(strings.NewReader(text))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var x xmlTask
	if err := dec.Decode(&x); err != nil {
		return Task{}, fmt.Errorf("unreadable task definition: %w", err)
	}
	t := Task{Enabled: xmlBool(x.Settings.Enabled, true), Triggers: []Trigger{}, Actions: []Action{}}
	anyUserLogon, logonUser := false, ""
	for _, tr := range x.Triggers.Items {
		if !xmlBool(tr.Enabled, true) {
			continue
		}
		kind, ok := triggerKinds[tr.XMLName.Local]
		if !ok {
			kind = TriggerOther
		}
		if !t.Has(kind) {
			t.Triggers = append(t.Triggers, kind)
		}
		if kind == TriggerLogon {
			u := strings.TrimSpace(tr.UserID)
			switch {
			case u == "":
				anyUserLogon = true
			case logonUser == "":
				logonUser = u
			case !strings.EqualFold(logonUser, u):
				anyUserLogon = true // several users: treat as any user
			}
		}
	}
	if !anyUserLogon {
		t.LogonUser = logonUser
	}
	if len(x.Principals.Items) > 0 {
		p := x.Principals.Items[0]
		t.UserID, t.GroupID = strings.TrimSpace(p.UserID), strings.TrimSpace(p.GroupID)
		t.HighestPrivileges = strings.EqualFold(strings.TrimSpace(p.RunLevel), "HighestAvailable")
	}
	for _, a := range x.Actions.Items {
		if a.XMLName.Local != "Exec" {
			t.OtherActions++
			continue
		}
		t.Actions = append(t.Actions, Action{
			Command:          strings.TrimSpace(a.Command),
			Arguments:        strings.TrimSpace(a.Arguments),
			WorkingDirectory: strings.TrimSpace(a.WorkingDirectory),
		})
	}
	return t, nil
}

func xmlBool(v *string, def bool) bool {
	if v == nil {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(*v)) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	return def
}

// Errors returned by stores.
var (
	// ErrNotFound: the task no longer exists.
	ErrNotFound = errors.New("the scheduled task no longer exists")
	// ErrAccessDenied: the current user may not change the task.
	ErrAccessDenied = errors.New("access denied: the task belongs to another account or was registered by an administrator")
	// ErrUnsupported: this build cannot talk to the Task Scheduler.
	ErrUnsupported = errors.New("reading scheduled tasks is not supported on this architecture")
)

// Store reads scheduled tasks and switches their Enabled flag.
type Store interface {
	// List returns every task the current user can read. It changes
	// nothing. Warnings describe folders or tasks that could not be read.
	List(ctx context.Context) (tasks []Task, warnings []string, err error)
	// SetEnabled turns the task at path on or off after checking that it
	// still exists, then reads the flag back. It changes nothing else.
	SetEnabled(path string, enabled bool) error
	// Account is the user oow runs as.
	Account() Account
}
