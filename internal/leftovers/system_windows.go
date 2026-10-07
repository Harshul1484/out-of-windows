package leftovers

import (
	"context"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/tasks"
)

// ReadTraces reads programs Windows recorded as having run for the current
// user: the shell's MuiCache (with friendly names and companies) and the
// Program Compatibility Assistant store. Read-only.
func ReadTraces() []Trace {
	byExe := map[string]*Trace{}
	get := func(exe string) *Trace {
		k := strings.ToLower(exe)
		if t := byExe[k]; t != nil {
			return t
		}
		t := &Trace{Exe: exe}
		byExe[k] = t
		return t
	}

	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\Local Settings\Software\Microsoft\Windows\Shell\MuiCache`, registry.QUERY_VALUE); err == nil {
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			i := strings.LastIndex(n, ".")
			if i < 0 {
				continue
			}
			exe, field := n[:i], n[i+1:]
			if !strings.EqualFold(filepath.Ext(exe), ".exe") {
				continue
			}
			v, _, err := k.GetStringValue(n)
			if err != nil {
				continue
			}
			switch field {
			case "FriendlyAppName":
				get(exe).Name = v
			case "ApplicationCompany":
				get(exe).Company = v
			}
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows NT\CurrentVersion\AppCompatFlags\Compatibility Assistant\Store`, registry.QUERY_VALUE); err == nil {
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			if strings.EqualFold(filepath.Ext(n), ".exe") {
				get(n)
			}
		}
		k.Close()
	}
	out := make([]Trace, 0, len(byExe))
	for _, t := range byExe {
		out = append(out, *t)
	}
	return out
}

// SystemClaims lists folders still used by running processes, services,
// startup entries and scheduled tasks, so their folders are never treated as
// leftovers.
func SystemClaims() []ClaimPath {
	var out []ClaimPath
	out = append(out, processClaims()...)
	out = append(out, serviceClaims()...)
	out = append(out, startupClaims()...)
	out = append(out, scheduledTaskClaims()...)
	return out
}

// scheduledTaskClaims reads every scheduled task the user can see (read-only,
// through the Task Scheduler API) and claims the folders they use.
func scheduledTaskClaims() []ClaimPath {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	list, _, err := tasks.System{}.List(ctx)
	if err != nil {
		return nil
	}
	return TaskClaims(list, func(s string) string {
		if x, err := registry.ExpandString(s); err == nil {
			return x
		}
		return s
	})
}

func processClaims() []ClaimPath {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []ClaimPath
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_PATH*2)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil {
			exe := windows.UTF16ToString(buf[:size])
			out = append(out, ClaimPath{Path: filepath.Dir(exe), Owner: "running program " + filepath.Base(exe)})
		}
		windows.CloseHandle(h)
	}
	return out
}

func serviceClaims() []ClaimPath {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	var out []ClaimPath
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		img, _, err := sk.GetStringValue("ImagePath")
		sk.Close()
		if err != nil || img == "" {
			continue
		}
		if dir := commandDir(img); dir != "" {
			out = append(out, ClaimPath{Path: dir, Owner: "service " + n})
		}
	}
	return out
}

func startupClaims() []ClaimPath {
	var out []ClaimPath
	for _, s := range []struct {
		root   registry.Key
		path   string
		access uint32
	}{
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, 0},
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\RunOnce`, 0},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.WOW64_32KEY},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\RunOnce`, registry.WOW64_64KEY},
	} {
		k, err := registry.OpenKey(s.root, s.path, registry.QUERY_VALUE|s.access)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			v, _, err := k.GetStringValue(n)
			if err != nil {
				continue
			}
			if dir := commandDir(v); dir != "" {
				out = append(out, ClaimPath{Path: dir, Owner: "startup entry " + n})
			}
		}
		k.Close()
	}
	return out
}

// commandDir returns the folder of the program in a command line.
func commandDir(cmdline string) string {
	if x, err := registry.ExpandString(cmdline); err == nil {
		cmdline = x
	}
	cmdline = strings.TrimPrefix(strings.TrimSpace(cmdline), `\??\`)
	cmd, err := apps.ParseCommandLine(cmdline, apps.FileExists)
	if err != nil || !strings.ContainsAny(cmd.Exe, `\/`) {
		return ""
	}
	return filepath.Dir(cmd.Exe)
}
