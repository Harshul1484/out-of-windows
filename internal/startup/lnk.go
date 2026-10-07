package startup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Link is the part of a Windows shortcut (.lnk, [MS-SHLLINK]) needed to tell
// what it starts. Shortcuts whose target exists only as a shell item ID list
// (Store apps, control panel items) have no Target.
type Link struct {
	// LocalPath is the target from the LinkInfo structure (local base path
	// plus common path suffix).
	LocalPath string
	// Network is set when LinkInfo names a network location.
	Network bool
	// EnvTarget is the target from the EnvironmentVariableDataBlock, with
	// %VARIABLES% unexpanded.
	EnvTarget    string
	RelativePath string
	WorkingDir   string
	Arguments    string
}

// TargetPath returns the shortcut's target file: the environment-variable
// form when present (expanded), else the LinkInfo path, else the relative
// path resolved against dir. It returns "" when the shortcut has no file
// target.
func (l *Link) TargetPath(dir string, expand func(string) string) string {
	switch {
	case l.EnvTarget != "":
		if expand != nil {
			return expand(l.EnvTarget)
		}
		return l.EnvTarget
	case l.LocalPath != "":
		return l.LocalPath
	case l.RelativePath != "":
		return filepath.Clean(filepath.Join(dir, l.RelativePath))
	}
	return ""
}

// Shell link flags.
const (
	lnkHasIDList       = 1 << 0
	lnkHasLinkInfo     = 1 << 1
	lnkHasName         = 1 << 2
	lnkHasRelativePath = 1 << 3
	lnkHasWorkingDir   = 1 << 4
	lnkHasArguments    = 1 << 5
	lnkHasIconLocation = 1 << 6
	lnkIsUnicode       = 1 << 7
	lnkForceNoLinkInfo = 1 << 8
	lnkHasExpString    = 1 << 9

	lnkHeaderSize       = 0x4C
	linkInfoLocal       = 1 << 0
	linkInfoNetwork     = 1 << 1
	envBlockSignature   = 0xA0000001
	envBlockSize        = 0x314
	maxLinkSize         = 1 << 20
	maxLinkStringLength = 32767
)

// linkCLSID is {00021401-0000-0000-C000-000000000046} in its on-disk form.
var linkCLSID = []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}

var errTruncated = errors.New("truncated shortcut")

// ParseLink parses a shell link. ansi decodes strings stored in the system
// code page; with nil, only ASCII is accepted and other bytes make the string
// unusable (an unknown target is better than a wrong one). Malformed input
// returns an error, never a panic.
func ParseLink(data []byte, ansi func([]byte) (string, bool)) (*Link, error) {
	if len(data) > maxLinkSize {
		return nil, errors.New("shortcut is too large")
	}
	if len(data) < lnkHeaderSize {
		return nil, errTruncated
	}
	if binary.LittleEndian.Uint32(data) != lnkHeaderSize || !bytes.Equal(data[4:20], linkCLSID) {
		return nil, errors.New("not a shell link")
	}
	if ansi == nil {
		ansi = asciiOnly
	}
	flags := binary.LittleEndian.Uint32(data[20:])
	pos := lnkHeaderSize
	l := &Link{}

	if flags&lnkHasIDList != 0 {
		if pos+2 > len(data) {
			return nil, errTruncated
		}
		pos += 2 + int(binary.LittleEndian.Uint16(data[pos:]))
		if pos > len(data) {
			return nil, errTruncated
		}
	}
	if flags&lnkHasLinkInfo != 0 {
		if pos+4 > len(data) {
			return nil, errTruncated
		}
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		if size < 4 || pos+size > len(data) {
			return nil, errTruncated
		}
		if flags&lnkForceNoLinkInfo == 0 {
			if err := parseLinkInfo(data[pos:pos+size], ansi, l); err != nil {
				return nil, err
			}
		}
		pos += size
	}

	unicode := flags&lnkIsUnicode != 0
	for _, f := range []uint32{lnkHasName, lnkHasRelativePath, lnkHasWorkingDir, lnkHasArguments, lnkHasIconLocation} {
		if flags&f == 0 {
			continue
		}
		s, n, err := readCountedString(data[pos:], unicode, ansi)
		if err != nil {
			return nil, err
		}
		pos += n
		switch f {
		case lnkHasRelativePath:
			l.RelativePath = s
		case lnkHasWorkingDir:
			l.WorkingDir = s
		case lnkHasArguments:
			l.Arguments = s
		}
	}

	// Extra data blocks, until the terminal block (size < 4).
	for pos+8 <= len(data) {
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		if size < 4 {
			break
		}
		if size < 8 || pos+size > len(data) {
			return nil, errTruncated
		}
		sig := binary.LittleEndian.Uint32(data[pos+4:])
		if sig == envBlockSignature && flags&lnkHasExpString != 0 && size >= envBlockSize {
			block := data[pos : pos+envBlockSize]
			if s := utf16z(block[8+260 : 8+260+520]); s != "" {
				l.EnvTarget = s
			} else if s, ok := ansi(cstring(block[8 : 8+260])); ok {
				l.EnvTarget = s
			}
		}
		pos += size
	}
	return l, nil
}

func parseLinkInfo(info []byte, ansi func([]byte) (string, bool), l *Link) error {
	if len(info) < 0x1C {
		return errTruncated
	}
	u32 := func(off int) int { return int(binary.LittleEndian.Uint32(info[off:])) }
	headerSize, flags := u32(4), u32(8)
	if headerSize < 0x1C || headerSize > len(info) {
		return errTruncated
	}
	if flags&linkInfoNetwork != 0 {
		l.Network = true
	}
	if flags&linkInfoLocal == 0 {
		return nil
	}
	var base, suffix string
	var ok bool
	if headerSize >= 0x24 && len(info) >= 0x24 && u32(0x1C) != 0 {
		off := u32(0x1C)
		if off >= len(info) {
			return errTruncated
		}
		base, ok = utf16zAt(info, off), true
		if off := u32(0x20); off != 0 && off < len(info) {
			suffix = utf16zAt(info, off)
		}
	} else {
		off := u32(0x10)
		if off == 0 || off >= len(info) {
			return errTruncated
		}
		base, ok = ansi(cstring(info[off:]))
		if off := u32(0x18); ok && off != 0 && off < len(info) {
			var ok2 bool
			suffix, ok2 = ansi(cstring(info[off:]))
			ok = ok && ok2
		}
	}
	if ok && base != "" {
		l.LocalPath = joinSuffix(base, suffix)
	}
	return nil
}

func joinSuffix(base, suffix string) string {
	if suffix == "" {
		return base
	}
	if strings.HasSuffix(base, `\`) {
		return base + suffix
	}
	return base + `\` + suffix
}

func readCountedString(b []byte, unicode bool, ansi func([]byte) (string, bool)) (string, int, error) {
	if len(b) < 2 {
		return "", 0, errTruncated
	}
	n := int(binary.LittleEndian.Uint16(b))
	if n > maxLinkStringLength {
		return "", 0, fmt.Errorf("string too long in shortcut")
	}
	if unicode {
		end := 2 + 2*n
		if end > len(b) {
			return "", 0, errTruncated
		}
		return decodeUTF16(b[2:end]), end, nil
	}
	end := 2 + n
	if end > len(b) {
		return "", 0, errTruncated
	}
	s, _ := ansi(b[2:end])
	return s, end, nil
}

func decodeUTF16(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// utf16z decodes a NUL-terminated UTF-16 string from a fixed buffer.
func utf16z(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

func utf16zAt(b []byte, off int) string {
	if off < 0 || off >= len(b) {
		return ""
	}
	return utf16z(b[off:])
}

// cstring returns b up to its first NUL byte.
func cstring(b []byte) []byte {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return b[:i]
	}
	return b
}

func asciiOnly(b []byte) (string, bool) {
	for _, c := range b {
		if c >= 0x80 {
			return "", false
		}
	}
	return string(b), true
}

// BuildLink returns a minimal shell link to target with arguments, stored
// with Unicode strings and a LinkInfo local path. It exists so the sandbox and
// tests can create shortcuts without the Shell.
func BuildLink(target, args string) []byte {
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	flags := uint32(lnkHasLinkInfo | lnkIsUnicode)
	if args != "" {
		flags |= lnkHasArguments
	}
	// Header.
	le(uint32(lnkHeaderSize))
	b.Write(linkCLSID)
	le(flags)
	le(uint32(0x20)) // FILE_ATTRIBUTE_ARCHIVE
	b.Write(make([]byte, 24))
	le(uint32(0)) // file size
	le(int32(0))  // icon index
	le(uint32(1)) // SW_SHOWNORMAL
	b.Write(make([]byte, 2+2+4+4))

	// LinkInfo with a VolumeID, ANSI and Unicode local base paths.
	ansiPath := make([]byte, 0, len(target))
	for _, r := range target {
		if r < 0x80 {
			ansiPath = append(ansiPath, byte(r))
		} else {
			ansiPath = append(ansiPath, '?')
		}
	}
	volumeID := []byte{0x11, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0x10, 0, 0, 0, 0}
	uniPath := utf16Bytes(target)
	const headerSize = 0x24
	volOff := headerSize
	ansiOff := volOff + len(volumeID)
	suffixOff := ansiOff + len(ansiPath) + 1
	uniOff := suffixOff + 1
	uniSuffixOff := uniOff + len(uniPath) + 2
	total := uniSuffixOff + 2
	for _, v := range []uint32{uint32(total), headerSize, linkInfoLocal, uint32(volOff), uint32(ansiOff), 0,
		uint32(suffixOff), uint32(uniOff), uint32(uniSuffixOff)} {
		le(v)
	}
	b.Write(volumeID)
	b.Write(append(ansiPath, 0))
	b.WriteByte(0)
	b.Write(append(uniPath, 0, 0))
	b.Write([]byte{0, 0})

	if args != "" {
		a := utf16Bytes(args)
		le(uint16(len(a) / 2))
		b.Write(a)
	}
	le(uint32(0)) // terminal block
	return b.Bytes()
}

func utf16Bytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(out[2*i:], c)
	}
	return out
}
