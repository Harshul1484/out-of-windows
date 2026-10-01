package system

import (
	"testing"
	"unsafe"
)

// SHQUERYRBINFO is 24 bytes on 64-bit Windows; a mismatch would make
// SHQueryRecycleBinW reject the call or misreport sizes.
func TestRecycleBinInfoLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layout only")
	}
	if got := unsafe.Sizeof(shQueryRBInfo{}); got != 24 {
		t.Errorf("sizeof(shQueryRBInfo) = %d, want 24", got)
	}
}
