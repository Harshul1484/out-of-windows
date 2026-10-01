package filesystem

import (
	"testing"
	"unsafe"
)

// SHFILEOPSTRUCTW is 56 bytes on 64-bit Windows.
func TestSHFileOpStructLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layout only")
	}
	if got := unsafe.Sizeof(shFileOpStruct{}); got != 56 {
		t.Errorf("sizeof(shFileOpStruct) = %d, want 56", got)
	}
}
