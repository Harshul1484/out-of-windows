package system

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32                = windows.NewLazySystemDLL("shell32.dll")
	procSHQueryRecycleBinW = shell32.NewProc("SHQueryRecycleBinW")
	procSHEmptyRecycleBinW = shell32.NewProc("SHEmptyRecycleBinW")
)

// shQueryRBInfo mirrors SHQUERYRBINFO with 64-bit natural alignment (the
// header packs it only for 32-bit Windows, which oow does not target).
type shQueryRBInfo struct {
	cbSize   uint32
	size     int64
	numItems int64
}

const (
	sherbNoConfirmation = 0x1
	sherbNoProgressUI   = 0x2
	sherbNoSound        = 0x4
	eUnexpected         = 0x8000FFFF // returned when the bin is already empty
)

// RecycleBin empties the real Recycle Bin of all drives through the Shell,
// exactly like "Empty Recycle Bin" in Explorer. It is a cleanup.Special.
type RecycleBin struct{}

// Measure returns the number of items and bytes in the Recycle Bin.
func (RecycleBin) Measure(ctx context.Context) (int, int64, error) {
	var info shQueryRBInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	hr, _, _ := procSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&info)))
	if hr != 0 {
		return 0, 0, fmt.Errorf("SHQueryRecycleBin failed: HRESULT 0x%08X", uint32(hr))
	}
	return int(info.numItems), info.size, nil
}

// Clean empties the Recycle Bin and reports what was removed, measured as the
// difference before and after.
func (b RecycleBin) Clean(ctx context.Context) (int, int64, error) {
	items, bytes, err := b.Measure(ctx)
	if err != nil {
		return 0, 0, err
	}
	if items == 0 {
		return 0, 0, nil
	}
	hr, _, _ := procSHEmptyRecycleBinW.Call(0, 0, sherbNoConfirmation|sherbNoProgressUI|sherbNoSound)
	afterItems, afterBytes, merr := b.Measure(ctx)
	if merr != nil {
		afterItems, afterBytes = 0, 0
	}
	removed, freed := items-afterItems, bytes-afterBytes
	if hr != 0 && uint32(hr) != eUnexpected {
		return removed, freed, fmt.Errorf("SHEmptyRecycleBin failed: HRESULT 0x%08X", uint32(hr))
	}
	return removed, freed, nil
}
