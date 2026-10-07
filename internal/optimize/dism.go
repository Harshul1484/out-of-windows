package optimize

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// The component store (WinSxS) is changed only by DISM, its owner tool, with
// these fixed command lines. /English asks for English output so the report
// can be read on any display language. Never used: /ResetBase (installed
// updates could no longer be uninstalled), /SPSuperseded (service packs could
// no longer be uninstalled) and /Defer.
var (
	dismAnalyzeArgs = []string{"/Online", "/English", "/Cleanup-Image", "/AnalyzeComponentStore"}
	// /Quiet drops progress output; /NoRestart is required with it, because
	// DISM restarts Windows on its own under /Quiet when it needs a restart.
	dismCleanupArgs = []string{"/Online", "/English", "/Quiet", "/NoRestart", "/Cleanup-Image", "/StartComponentCleanup"}
)

// DISMCommandLine is the exact argument list oow passes to Dism.exe for the
// analysis or the cleanup; the sandbox records it for each simulated run.
func DISMCommandLine(cleanup bool) string {
	if cleanup {
		return strings.Join(dismCleanupArgs, " ")
	}
	return strings.Join(dismAnalyzeArgs, " ")
}

// Time limits. The analysis usually takes one to five minutes; the cleanup
// takes minutes to well over an hour on a store that was never cleaned.
// Neither limit ever stops DISM: oow only stops waiting (see waitDISM).
const (
	dismAnalyzeTimeout = 30 * time.Minute
	dismCleanupTimeout = 3 * time.Hour
)

// dismRestartRequired is ERROR_SUCCESS_REBOOT_REQUIRED: done, restart pending.
const dismRestartRequired = 3010

// ComponentStore is DISM's report on the component store (WinSxS), read from
// DISM /Online /Cleanup-Image /AnalyzeComponentStore. It exists only when the
// whole report could be read; an unreadable report is an error, never zeros.
type ComponentStore struct {
	// ActualBytes is the store's real size (hard links counted once).
	ActualBytes int64 `json:"actual_bytes"`
	// ExplorerBytes is the size File Explorer shows (hard links counted
	// again); SharedBytes is the part that is Windows itself. -1 when DISM
	// did not print them.
	ExplorerBytes int64 `json:"explorer_bytes"`
	SharedBytes   int64 `json:"shared_bytes"`
	// BackupsBytes ("Backups and Disabled Features") plus CacheBytes ("Cache
	// and Temporary Data") is the store's overhead; a cleanup frees part of it.
	BackupsBytes int64 `json:"backups_bytes"`
	CacheBytes   int64 `json:"cache_bytes"`
	// ReclaimablePackages is the number of superseded packages a cleanup can remove.
	ReclaimablePackages int `json:"reclaimable_packages"`
	// Recommended is DISM's own "Component Store Cleanup Recommended".
	Recommended bool `json:"cleanup_recommended"`
	// LastCleanup is "Date of Last Cleanup" exactly as DISM printed it.
	LastCleanup string `json:"last_cleanup,omitempty"`
}

// OverheadBytes is what the store keeps beyond Windows itself, as Microsoft
// defines it: backups and disabled features plus cache and temporary data.
func (c ComponentStore) OverheadBytes() int64 { return c.BackupsBytes + c.CacheBytes }

// ErrUnreadableReport means DISM's report lacked an expected English line or
// held a value that could not be read. The task then does not run.
var ErrUnreadableReport = errors.New("DISM's component store report could not be read")

// ErrNoDISM means this Windows has no DISM in System32.
var ErrNoDISM = errors.New("this Windows has no DISM (Dism.exe is missing from System32)")

type reportField struct {
	label    string
	required bool
	set      func(c *ComponentStore, v string) error
}

func sizeField(dst func(*ComponentStore) *int64) func(*ComponentStore, string) error {
	return func(c *ComponentStore, v string) error {
		n, err := parseDISMSize(v)
		*dst(c) = n
		return err
	}
}

// reportFields are the lines of the English report. The decision (run or
// not) and the explanation rest on the required ones.
var reportFields = []reportField{
	{"Windows Explorer Reported Size of Component Store", false, sizeField(func(c *ComponentStore) *int64 { return &c.ExplorerBytes })},
	{"Actual Size of Component Store", true, sizeField(func(c *ComponentStore) *int64 { return &c.ActualBytes })},
	{"Shared with Windows", false, sizeField(func(c *ComponentStore) *int64 { return &c.SharedBytes })},
	{"Backups and Disabled Features", true, sizeField(func(c *ComponentStore) *int64 { return &c.BackupsBytes })},
	{"Cache and Temporary Data", true, sizeField(func(c *ComponentStore) *int64 { return &c.CacheBytes })},
	{"Date of Last Cleanup", false, func(c *ComponentStore, v string) error { c.LastCleanup = v; return nil }},
	{"Number of Reclaimable Packages", true, func(c *ComponentStore, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 1_000_000 || strings.TrimLeft(v, "0123456789") != "" {
			return errors.New("not a count")
		}
		c.ReclaimablePackages = n
		return nil
	}},
	{"Component Store Cleanup Recommended", true, func(c *ComponentStore, v string) error {
		switch strings.ToLower(v) {
		case "yes":
			c.Recommended = true
		case "no":
			c.Recommended = false
		default:
			return errors.New("neither Yes nor No")
		}
		return nil
	}},
}

// ParseComponentStoreReport reads the output of DISM /AnalyzeComponentStore
// /English. Every required line must be present exactly once (or repeated
// with the same value) and readable; otherwise the result is an error wrapping
// ErrUnreadableReport, so a localized or changed report is never mistaken for
// an empty store or for a recommendation.
func ParseComponentStoreReport(out []byte) (ComponentStore, error) {
	text := decodeDISM(out)
	if code, msg, ok := dismFailure(text); ok {
		return ComponentStore{}, dismFailureError(code, msg)
	}
	values := map[string]string{}
	for _, line := range splitLines(text) {
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		label, value := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		for _, f := range reportFields {
			if !strings.EqualFold(label, f.label) {
				continue
			}
			if prev, seen := values[f.label]; seen && prev != value {
				return ComponentStore{}, fmt.Errorf("%w: %q appears twice with different values", ErrUnreadableReport, f.label)
			}
			values[f.label] = value
		}
	}
	c := ComponentStore{ExplorerBytes: -1, SharedBytes: -1}
	for _, f := range reportFields {
		v, ok := values[f.label]
		if !ok {
			if f.required {
				return ComponentStore{}, fmt.Errorf("%w: no %q line (DISM's output may not be in English)", ErrUnreadableReport, f.label)
			}
			continue
		}
		if err := f.set(&c, v); err != nil {
			if !f.required {
				// A display-only value that cannot be read stays unknown.
				continue
			}
			return ComponentStore{}, fmt.Errorf("%w: %s is %q", ErrUnreadableReport, f.label, v)
		}
	}
	// The overhead is part of the actual size; allow for DISM's rounding to
	// two decimals in different units.
	if c.OverheadBytes() > c.ActualBytes+c.ActualBytes/100+(1<<20) {
		return ComponentStore{}, fmt.Errorf("%w: the overhead (%s) exceeds the store's size (%s)", ErrUnreadableReport,
			sizeString(c.OverheadBytes()), sizeString(c.ActualBytes))
	}
	return c, nil
}

var dismUnits = map[string]float64{"bytes": 1, "kb": 1 << 10, "mb": 1 << 20, "gb": 1 << 30, "tb": 1 << 40}

// parseDISMSize reads sizes such as "4.88 GB", "506.90 MB", "279.52 KB" and
// "0 bytes". DISM prints at most two decimals, so a separator followed by
// three digits groups thousands ("1,010.50 MB", "1.023 bytes"). Anything else
// is an error.
func parseDISMSize(s string) (int64, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return -1, fmt.Errorf("not a size: %q", s)
	}
	unit, ok := dismUnits[strings.ToLower(f[1])]
	if !ok {
		return -1, fmt.Errorf("unknown unit in %q", s)
	}
	n, frac, ok := parseDISMNumber(f[0])
	if !ok || (unit == 1 && frac) {
		return -1, fmt.Errorf("not a number: %q", s)
	}
	v := math.Round(n * unit)
	if v < 0 || v > 1<<62 {
		return -1, fmt.Errorf("out of range: %q", s)
	}
	return int64(v), nil
}

// parseDISMNumber reads a non-negative number with an optional one- or
// two-digit decimal part after "." or ",", and optional thousands grouping
// with the other character. frac reports whether a decimal part was present.
func parseDISMNumber(s string) (n float64, frac bool, ok bool) {
	intPart, fracPart, dec := s, "", byte(0)
	if i := strings.LastIndexAny(s, ".,"); i >= 0 && len(s)-i-1 <= 2 {
		intPart, fracPart, dec = s[:i], s[i+1:], s[i]
		if fracPart == "" || !allDigits(fracPart) {
			return 0, false, false
		}
	}
	if intPart == "" {
		return 0, false, false
	}
	if j := strings.IndexAny(intPart, ".,"); j >= 0 {
		group := intPart[j]
		if group == dec {
			return 0, false, false
		}
		parts := strings.Split(intPart, string(group))
		if len(parts[0]) < 1 || len(parts[0]) > 3 || !allDigits(parts[0]) {
			return 0, false, false
		}
		for _, p := range parts[1:] {
			if len(p) != 3 || !allDigits(p) {
				return 0, false, false
			}
		}
		intPart = strings.Join(parts, "")
	}
	if !allDigits(intPart) {
		return 0, false, false
	}
	v, err := strconv.ParseFloat(intPart+"."+fracPart+"0", 64)
	if err != nil {
		return 0, false, false
	}
	return v, fracPart != "", true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// decodeDISM returns DISM's output as text. DISM writes single-byte text to
// a pipe; UTF-16 (with or without a byte order mark) is decoded too.
func decodeDISM(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return utf16le(b[2:])
	case looksUTF16LE(b):
		return utf16le(b)
	}
	return string(b)
}

func looksUTF16LE(b []byte) bool {
	n := min(len(b), 256) &^ 1
	if n < 4 {
		return false
	}
	zeros := 0
	for i := 1; i < n; i += 2 {
		if b[i] == 0 {
			zeros++
		}
	}
	return zeros*10 >= n/2*9
}

func utf16le(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// splitLines splits on CR and LF: DISM redraws its progress bar with CR.
func splitLines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' })
}

// dismFailure finds DISM's "Error: <code>" line and the message after it.
func dismFailure(text string) (code, msg string, ok bool) {
	lines := splitLines(text)
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "Error:") {
			continue
		}
		code = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "Error:")))
		var parts []string
		for _, m := range lines[i+1:] {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if strings.HasPrefix(m, "The DISM log file can be found at") {
				break
			}
			parts = append(parts, m)
		}
		msg = strings.Join(parts, " ")
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return code, msg, true
	}
	return "", "", false
}

// dismNextSteps name the next step for DISM errors with a known cause.
var dismNextSteps = map[string]string{
	"740":        "requires administrator: run it from an elevated terminal",
	"0x800f0806": "Windows has servicing operations pending: restart Windows, then run this task again",
	"87":         "this Windows' DISM does not support this option",
}

func dismFailureError(code, msg string) error {
	s := "DISM error " + code
	if msg != "" {
		s += ": " + strings.TrimSuffix(msg, ".")
	}
	if next := dismNextSteps[code]; next != "" {
		s += " (" + next + ")"
	}
	return errors.New(s)
}

// dismError describes a DISM run that exited with a failure code: DISM's own
// "Error:" message when it printed one, else the exit code.
func dismError(out []byte, exit uint32) error {
	if code, msg, ok := dismFailure(decodeDISM(out)); ok {
		return dismFailureError(code, msg)
	}
	code := strconv.FormatUint(uint64(exit), 10)
	if exit > 0xFFFF {
		code = fmt.Sprintf("0x%08x", exit)
	}
	return dismFailureError(code, "")
}
