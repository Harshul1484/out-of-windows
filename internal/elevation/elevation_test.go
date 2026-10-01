package elevation

import (
	"testing"
	"unsafe"
)

func TestCommandLineQuoting(t *testing.T) {
	got := CommandLine([]string{"clean", "--rule", "temp.windows,logs.minidumps", "--pause", `C:\Program Files\x`, `say "hi"`})
	want := `clean --rule temp.windows,logs.minidumps --pause "C:\Program Files\x" "say \"hi\""`
	if got != want {
		t.Errorf("CommandLine = %s\nwant           %s", got, want)
	}
}

// SHELLEXECUTEINFOW is 112 bytes on 64-bit Windows; a layout mistake here
// would corrupt the call.
func TestShellExecuteInfoSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layout only")
	}
	if got := unsafe.Sizeof(shellExecuteInfo{}); got != 112 {
		t.Errorf("sizeof(shellExecuteInfo) = %d, want 112", got)
	}
}
