package install

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// SystemUserPath is the real user PATH: the "Path" value of
// HKEY_CURRENT_USER\Environment. It is the only registry value `remove`
// writes, and only to drop the entry the installer added.
type SystemUserPath struct{}

const envKey = `Environment`

// Get reads the raw, unexpanded value.
func (SystemUserPath) Get() (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE)
	if err != nil {
		return "", true, err
	}
	defer k.Close()
	v, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", true, err
	}
	return v, typ == registry.EXPAND_SZ, nil
}

// Set writes the value with its original kind and broadcasts the change.
func (SystemUserPath) Set(value string, expandable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if expandable {
		err = k.SetExpandStringValue("Path", value)
	} else {
		err = k.SetStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

// Expand expands %VARIABLES% for comparison.
func (SystemUserPath) Expand(entry string) string {
	if s, err := registry.ExpandString(entry); err == nil {
		return s
	}
	return entry
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

// broadcastEnvironmentChange tells Explorer and other top-level windows to
// reload the environment (WM_SETTINGCHANGE "Environment"), so new terminals
// see the updated PATH. Best effort: a hung window cannot block us for long.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xFFFF
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	env, _ := windows.UTF16PtrFromString("Environment")
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
