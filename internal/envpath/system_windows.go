package envpath

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	userEnvKey    = `Environment`
	machineEnvKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
)

// System reads the real PATH variables from the registry and writes only the
// current user's PATH (HKCU\Environment).
type System struct{}

func readValue(k registry.Key) (Value, error) {
	s, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return Value{}, nil
	}
	if err != nil {
		return Value{}, err
	}
	return Value{Raw: s, Expand: typ == registry.EXPAND_SZ, Exists: true}, nil
}

// Read returns the stored (unexpanded) PATH of a scope.
func (System) Read(scope Scope) (Value, error) {
	root, path, access := registry.CURRENT_USER, userEnvKey, uint32(registry.QUERY_VALUE)
	if scope == Machine {
		root, path, access = registry.LOCAL_MACHINE, machineEnvKey, registry.QUERY_VALUE|registry.WOW64_64KEY
	}
	k, err := registry.OpenKey(root, path, access)
	if errors.Is(err, registry.ErrNotExist) {
		return Value{}, nil
	}
	if err != nil {
		return Value{}, err
	}
	defer k.Close()
	return readValue(k)
}

// WriteUser replaces the user PATH if it still equals expected.
func (System) WriteUser(expected, updated Value) error {
	if !updated.Exists {
		return errors.New("refusing to delete the user PATH")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, userEnvKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, err := readValue(k)
	if err != nil {
		return err
	}
	if cur != expected {
		return ErrChanged
	}
	if updated.Expand {
		err = k.SetExpandStringValue("Path", updated.Raw)
	} else {
		err = k.SetStringValue("Path", updated.Raw)
	}
	if err != nil {
		return err
	}
	back, err := readValue(k)
	if err != nil {
		return err
	}
	if back != updated {
		return fmt.Errorf("the PATH read back differs from what was written")
	}
	return nil
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

// Broadcast sends WM_SETTINGCHANGE("Environment") to top-level windows, as
// the System Properties dialog does, so Explorer reloads the environment.
func (System) Broadcast() error {
	const (
		hwndBroadcast    = 0xFFFF
		wmSettingChange  = 0x001A
		smtoAbortIfHung  = 0x0002
		broadcastTimeout = 5000
	)
	param, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var result uintptr
	r, _, callErr := procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(param)), smtoAbortIfHung, broadcastTimeout, uintptr(unsafe.Pointer(&result)))
	if r == 0 {
		return fmt.Errorf("broadcast the environment change: %v", callErr)
	}
	return nil
}

// Expand expands %VARIABLES% with the current process environment.
func (System) Expand(s string) (string, bool) {
	x, err := registry.ExpandString(s)
	if err != nil {
		return s, false
	}
	return x, !strings.Contains(x, "%")
}
