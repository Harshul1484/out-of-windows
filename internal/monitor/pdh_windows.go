package monitor

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Minimal Performance Data Helper (PDH) wrapper. Counters are added by their
// English names (PdhAddEnglishCounterW), so they work on every display
// language of Windows.

var (
	pdh                             = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQueryW               = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW       = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData         = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterValue = pdh.NewProc("PdhGetFormattedCounterValue")
	procPdhGetFormattedCounterArray = pdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery               = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble = 0x00000200
	pdhFmtNoCap  = 0x00008000
	pdhMoreData  = 0x800007D2
	pdhValidData = 0x00000000
	pdhNewData   = 0x00000001
)

type pdhCounterValue struct {
	CStatus uint32
	_       uint32
	Double  float64
}

type pdhCounterItem struct {
	Name  *uint16
	Value pdhCounterValue
}

type pdhQuery struct {
	h        uintptr
	counters map[string]uintptr
}

func openPDH(paths []string) *pdhQuery {
	q := &pdhQuery{counters: map[string]uintptr{}}
	if procPdhOpenQueryW.Find() != nil {
		return q
	}
	if r, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&q.h))); r != 0 {
		q.h = 0
		return q
	}
	for _, p := range paths {
		s, err := windows.UTF16PtrFromString(p)
		if err != nil {
			continue
		}
		var c uintptr
		if r, _, _ := procPdhAddEnglishCounterW.Call(q.h, uintptr(unsafe.Pointer(s)), 0, uintptr(unsafe.Pointer(&c))); r == 0 {
			q.counters[p] = c
		}
	}
	q.collect()
	return q
}

func (q *pdhQuery) collect() {
	if q.h != 0 {
		procPdhCollectQueryData.Call(q.h)
	}
}

// value returns a single counter value, or ok=false.
func (q *pdhQuery) value(path string) (float64, bool) {
	c, ok := q.counters[path]
	if !ok {
		return 0, false
	}
	var v pdhCounterValue
	if r, _, _ := procPdhGetFormattedCounterValue.Call(c, pdhFmtDouble|pdhFmtNoCap, 0, uintptr(unsafe.Pointer(&v))); r != 0 {
		return 0, false
	}
	if v.CStatus != pdhValidData && v.CStatus != pdhNewData {
		return 0, false
	}
	return v.Double, true
}

// array returns all instances of a wildcard counter.
func (q *pdhQuery) array(path string) map[string]float64 {
	c, ok := q.counters[path]
	if !ok {
		return nil
	}
	var size, count uint32
	r, _, _ := procPdhGetFormattedCounterArray.Call(c, pdhFmtDouble|pdhFmtNoCap, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if r != pdhMoreData || size == 0 {
		return nil
	}
	buf := make([]byte, size)
	r, _, _ = procPdhGetFormattedCounterArray.Call(c, pdhFmtDouble|pdhFmtNoCap, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return nil
	}
	out := make(map[string]float64, count)
	items := unsafe.Slice((*pdhCounterItem)(unsafe.Pointer(&buf[0])), count)
	for _, it := range items {
		if it.Value.CStatus != pdhValidData && it.Value.CStatus != pdhNewData {
			continue
		}
		out[windows.UTF16PtrToString(it.Name)] += it.Value.Double
	}
	return out
}

func (q *pdhQuery) close() {
	if q.h != 0 {
		procPdhCloseQuery.Call(q.h)
		q.h = 0
	}
}
