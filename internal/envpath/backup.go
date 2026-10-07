package envpath

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// RegFile renders v as a registry script (.reg) that restores the user PATH
// when double-clicked or imported with `reg import`. Values are written in
// hex form (hex(2) for REG_EXPAND_SZ, hex(1) for REG_SZ), so no character
// needs escaping. The file is UTF-16LE with a BOM, as regedit writes it.
func RegFile(v Value) []byte {
	kind := 1
	if v.Expand {
		kind = 2
	}
	var hexs []string
	for _, c := range utf16.Encode([]rune(v.Raw + "\x00")) {
		hexs = append(hexs, fmt.Sprintf("%02x,%02x", byte(c), byte(c>>8)))
	}
	text := "Windows Registry Editor Version 5.00\r\n\r\n" +
		"[HKEY_CURRENT_USER\\Environment]\r\n" +
		fmt.Sprintf("\"Path\"=hex(%d):%s\r\n", kind, strings.Join(hexs, ","))
	u := utf16.Encode([]rune(text))
	out := make([]byte, 2+2*len(u))
	out[0], out[1] = 0xFF, 0xFE
	for i, c := range u {
		binary.LittleEndian.PutUint16(out[2+2*i:], c)
	}
	return out
}

// ParseRegFile reads back a file written by RegFile (used by tests and to
// verify a backup before relying on it).
func ParseRegFile(data []byte) (Value, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xFE || len(data)%2 != 0 {
		return Value{}, errors.New("not a UTF-16 registry file")
	}
	u := make([]uint16, (len(data)-2)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(data[2+2*i:])
	}
	text := string(utf16.Decode(u))
	for _, line := range strings.Split(text, "\r\n") {
		rest, ok := strings.CutPrefix(line, `"Path"=hex(`)
		if !ok || len(rest) < 3 || rest[1:3] != "):" {
			continue
		}
		v := Value{Expand: rest[0] == '2', Exists: true}
		fields := strings.Split(rest[3:], ",")
		if len(fields)%2 != 0 {
			return Value{}, errors.New("odd number of bytes")
		}
		var chars []uint16
		for i := 0; i < len(fields); i += 2 {
			var lo, hi byte
			if _, err := fmt.Sscanf(fields[i]+" "+fields[i+1], "%02x %02x", &lo, &hi); err != nil {
				return Value{}, err
			}
			chars = append(chars, uint16(lo)|uint16(hi)<<8)
		}
		if n := len(chars); n > 0 && chars[n-1] == 0 {
			chars = chars[:n-1]
		}
		v.Raw = string(utf16.Decode(chars))
		return v, nil
	}
	return Value{}, errors.New("no Path value in the registry file")
}

// WriteBackup saves v as a .reg file in dir and returns its path. The file is
// written to a temporary name, flushed and renamed, and never overwrites an
// existing backup. The backup is read back and compared before it is trusted.
func WriteBackup(dir string, v Value, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data := RegFile(v)
	base := "path-user-" + now.Format("20060102-150405")
	var target string
	for i := 0; ; i++ {
		target = filepath.Join(dir, base+".reg")
		if i > 0 {
			target = filepath.Join(dir, fmt.Sprintf("%s-%d.reg", base, i))
		}
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		if i > 100 {
			return "", errors.New("too many backups with the same time")
		}
	}
	tmp, err := os.CreateTemp(dir, ".path-backup-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", err
	}
	back, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(back, data) {
		return "", errors.New("backup file does not match what was written")
	}
	if got, err := ParseRegFile(back); err != nil || got.Raw != v.Raw || got.Expand != v.Expand {
		return "", errors.New("backup file cannot be read back")
	}
	return target, nil
}
