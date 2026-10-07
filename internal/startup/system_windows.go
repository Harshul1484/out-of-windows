package startup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/tasks"
)

// System reads startup entries of the running Windows system and writes only
// StartupApproved values and the Enabled flag of scheduled tasks.
type System struct{}

type regSource struct {
	src    Source
	root   registry.Key
	path   string
	access uint32
}

var regSources = []regSource{
	{SourceHKCURun, registry.CURRENT_USER, runKey, 0},
	{SourceHKCURunOnce, registry.CURRENT_USER, runOnceKey, 0},
	{SourceHKLMRun, registry.LOCAL_MACHINE, runKey, registry.WOW64_64KEY},
	{SourceHKLMRun32, registry.LOCAL_MACHINE, runKey, registry.WOW64_32KEY},
	{SourceHKLMRunOnce, registry.LOCAL_MACHINE, runOnceKey, registry.WOW64_64KEY},
	{SourceHKLMRunOnce32, registry.LOCAL_MACHINE, runOnceKey, registry.WOW64_32KEY},
}

func (s regSource) label() string { return sources[s.src].location }

// folders returns the user and common Startup folders (Known Folder APIs).
func folders() map[Source]string {
	out := map[Source]string{}
	if p, err := windows.KnownFolderPath(windows.FOLDERID_Startup, 0); err == nil {
		out[SourceUserFolder] = p
	}
	if p, err := windows.KnownFolderPath(windows.FOLDERID_CommonStartup, 0); err == nil {
		out[SourceCommonFolder] = p
	}
	return out
}

// approvalKey opens the StartupApproved key of a source.
func approvalKey(src Source, access uint32, create bool) (registry.Key, error) {
	info := sources[src]
	root := registry.CURRENT_USER
	if info.machine {
		root, access = registry.LOCAL_MACHINE, access|registry.WOW64_64KEY
	}
	path := approvedKey + `\` + info.approval
	if create {
		k, _, err := registry.CreateKey(root, path, access)
		return k, err
	}
	return registry.OpenKey(root, path, access)
}

func readApproval(src Source, name string) []byte {
	if sources[src].approval == "" {
		return nil
	}
	k, err := approvalKey(src, registry.QUERY_VALUE, false)
	if err != nil {
		return nil
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue(name)
	if err != nil {
		return nil
	}
	return b
}

// Resolver returns the resolver used for the real system.
func (System) Resolver() Resolver {
	sys := system.SystemDir()
	win := filepath.Dir(sys)
	return Resolver{
		Expand: func(s string) string {
			if x, err := registry.ExpandString(s); err == nil {
				return x
			}
			return s
		},
		Probe:  system.ProbePath,
		Search: []string{sys, win},
	}
}

// List reads every startup source. Unreadable sources become warnings.
func (s System) List(ctx context.Context) ([]Entry, []string, error) {
	var raws []Raw
	var warnings []string
	for _, rs := range regSources {
		k, err := registry.OpenKey(rs.root, rs.path, registry.QUERY_VALUE|rs.access)
		if err != nil {
			if !errors.Is(err, registry.ErrNotExist) {
				warnings = append(warnings, fmt.Sprintf("could not read %s: %v", rs.label(), err))
			}
			continue
		}
		names, err := k.ReadValueNames(-1)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not list %s: %v", rs.label(), err))
		}
		for _, n := range names {
			if n == "" {
				continue
			}
			v, _, err := k.GetStringValue(n)
			if err != nil {
				continue // not a string: Windows does not run it either
			}
			raws = append(raws, Raw{Source: rs.src, Name: n, Command: v, Approval: readApproval(rs.src, n)})
		}
		k.Close()
	}
	dirs := folders()
	for _, src := range []Source{SourceUserFolder, SourceCommonFolder} {
		dir, ok := dirs[src]
		if !ok {
			continue
		}
		if ctx.Err() != nil {
			return nil, warnings, ctx.Err()
		}
		r, err := ReadFolder(src, dir, ansiDecode, func(name string) []byte { return readApproval(src, name) })
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not read %s: %v", dir, err))
		}
		raws = append(raws, r...)
	}
	if ctx.Err() != nil {
		return nil, warnings, ctx.Err()
	}
	tr, tw := TaskRaws(ctx, tasks.System{})
	raws, warnings = append(raws, tr...), append(warnings, tw...)
	if ctx.Err() != nil {
		return nil, warnings, ctx.Err()
	}
	return Build(raws, s.Resolver()), warnings, nil
}

// SetTaskEnabled switches the Enabled flag of the scheduled task behind e
// through the Task Scheduler API (IRegisteredTask.Enabled), after checking
// that the task still exists, and reads it back.
func (System) SetTaskEnabled(e Entry, enabled bool) error {
	if e.Task == nil {
		return fmt.Errorf("%s is not a scheduled task", e.Name)
	}
	return TaskWriteError(tasks.System{}.SetEnabled(e.Task.Path, enabled))
}

// DecodeANSI decodes bytes in the system's ANSI code page (for ParseLink).
func DecodeANSI(b []byte) (string, bool) { return ansiDecode(b) }

// cpACP is the system ANSI code page identifier.
const cpACP = 0

// ansiDecode decodes bytes in the system's ANSI code page.
func ansiDecode(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", true
	}
	if s, ok := asciiOnly(b); ok {
		return s, true
	}
	n, err := windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return "", false
	}
	u := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
		return "", false
	}
	return windows.UTF16ToString(u), true
}

// SetApproval writes the StartupApproved value of e after checking that the
// entry is still registered, then reads it back.
func (System) SetApproval(e Entry, data []byte) error {
	if sources[e.Source].approval == "" {
		return fmt.Errorf("%s entries cannot be enabled or disabled", e.Source.Label())
	}
	if !stillPresent(e) {
		return ErrEntryGone
	}
	k, err := approvalKey(e.Source, registry.QUERY_VALUE|registry.SET_VALUE, true)
	if err != nil {
		return fmt.Errorf("open %s: %w", e.Source.ApprovalKey(), err)
	}
	defer k.Close()
	if err := k.SetBinaryValue(e.Name, data); err != nil {
		return fmt.Errorf("write %s\\%s: %w", e.Source.ApprovalKey(), e.Name, err)
	}
	got, _, err := k.GetBinaryValue(e.Name)
	if err != nil || !bytes.Equal(got, data) {
		return fmt.Errorf("the value read back from %s differs from what was written", e.Source.ApprovalKey())
	}
	return nil
}

func stillPresent(e Entry) bool {
	if e.Source.Folder() {
		_, err := os.Lstat(e.Location)
		return err == nil
	}
	for _, rs := range regSources {
		if rs.src != e.Source {
			continue
		}
		k, err := registry.OpenKey(rs.root, rs.path, registry.QUERY_VALUE|rs.access)
		if err != nil {
			return false
		}
		defer k.Close()
		_, _, err = k.GetStringValue(e.Name)
		return err == nil
	}
	return false
}
