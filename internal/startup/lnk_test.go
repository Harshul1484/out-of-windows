package startup_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Harshul1484/out-of-windows/internal/startup"
)

func TestBuildLinkRoundTrip(t *testing.T) {
	for _, c := range []struct{ target, args string }{
		{`C:\Program Files\App\app.exe`, "--minimized"},
		{`D:\Tools\tool.exe`, ""},
		{`C:\Users\Zoë\Programs\Café\app.exe`, `--name "Zoë"`},
	} {
		l, err := startup.ParseLink(startup.BuildLink(c.target, c.args), nil)
		if err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		if got := l.TargetPath(`C:\Startup`, nil); got != c.target || l.Arguments != c.args {
			t.Errorf("round trip %q %q = %q %q", c.target, c.args, got, l.Arguments)
		}
	}
}

// link assembles a shell link from parts, for parser tests.
type link struct {
	flags    uint32
	idList   []byte
	linkInfo []byte
	strings  []string // in flag order
	unicode  bool
	extra    []byte
}

func (l link) bytes() []byte {
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	le(uint32(0x4C))
	b.Write([]byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46})
	le(l.flags)
	b.Write(make([]byte, 76-24))
	if l.flags&1 != 0 {
		le(uint16(len(l.idList)))
		b.Write(l.idList)
	}
	b.Write(l.linkInfo)
	for _, s := range l.strings {
		if l.unicode {
			u := utf16.Encode([]rune(s))
			le(uint16(len(u)))
			for _, c := range u {
				le(c)
			}
		} else {
			le(uint16(len(s)))
			b.WriteString(s)
		}
	}
	b.Write(l.extra)
	le(uint32(0))
	return b.Bytes()
}

// ansiLinkInfo builds a LinkInfo with only ANSI paths (header size 0x1C).
func ansiLinkInfo(base, suffix string) []byte {
	vol := []byte{0x11, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0x10, 0, 0, 0, 0}
	baseOff := 0x1C + len(vol)
	suffixOff := baseOff + len(base) + 1
	total := suffixOff + len(suffix) + 1
	var b bytes.Buffer
	for _, v := range []uint32{uint32(total), 0x1C, 1, 0x1C, uint32(baseOff), 0, uint32(suffixOff)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.Write(vol)
	b.WriteString(base + "\x00")
	b.WriteString(suffix + "\x00")
	return b.Bytes()
}

func TestParseLinkANSILinkInfoWithIDList(t *testing.T) {
	data := link{
		flags:    1 | 2 | 0x20, // IDList, LinkInfo, arguments (ANSI)
		idList:   []byte{0x02, 0x00},
		linkInfo: ansiLinkInfo(`C:\Old App\`, `bin\old.exe`),
		strings:  []string{"/background"},
	}.bytes()
	l, err := startup.ParseLink(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.TargetPath("", nil); got != `C:\Old App\bin\old.exe` || l.Arguments != "/background" {
		t.Errorf("target %q args %q", got, l.Arguments)
	}
	// Non-ASCII ANSI text is not guessed at without a code page decoder.
	data = link{flags: 2, linkInfo: ansiLinkInfo("C:\\Caf\xe9\\", "app.exe")}.bytes()
	l, err = startup.ParseLink(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.TargetPath("", nil); got != "" {
		t.Errorf("non-ASCII ANSI target = %q, want unknown", got)
	}
}

func TestParseLinkEnvironmentBlockAndRelativePath(t *testing.T) {
	// EnvironmentVariableDataBlock: size 0x314, signature 0xA0000001,
	// TargetAnsi[260], TargetUnicode[520].
	env := make([]byte, 0x314)
	binary.LittleEndian.PutUint32(env, 0x314)
	binary.LittleEndian.PutUint32(env[4:], 0xA0000001)
	copy(env[8:], `%ProgramFiles%\App\app.exe`)
	for i, c := range utf16.Encode([]rune(`%ProgramFiles%\App\app.exe`)) {
		binary.LittleEndian.PutUint16(env[8+260+2*i:], c)
	}
	data := link{flags: 0x200 | 0x80, unicode: true, extra: env}.bytes()
	l, err := startup.ParseLink(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	expand := func(s string) string { return strings.ReplaceAll(s, "%ProgramFiles%", `C:\Program Files`) }
	if got := l.TargetPath("", expand); got != `C:\Program Files\App\app.exe` {
		t.Errorf("env target = %q", got)
	}

	data = link{flags: 0x08 | 0x80, unicode: true, strings: []string{`..\Tools\tool.exe`}}.bytes()
	l, err = startup.ParseLink(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.TargetPath(`C:\Users\me\Startup`, nil); got != `C:\Users\me\Tools\tool.exe` {
		t.Errorf("relative target = %q", got)
	}

	// Only an ID list (a shell item such as a Store app): no file target.
	data = link{flags: 1, idList: []byte{0x02, 0x00}}.bytes()
	l, err = startup.ParseLink(data, nil)
	if err != nil || l.TargetPath("", nil) != "" {
		t.Errorf("ID-list-only link = %+v, %v", l, err)
	}
}

func TestParseLinkRejectsMalformed(t *testing.T) {
	good := startup.BuildLink(`C:\App\app.exe`, "--x")
	for name, data := range map[string][]byte{
		"empty":       nil,
		"short":       good[:40],
		"bad header":  append([]byte{0x4D}, good[1:]...),
		"bad clsid":   append(append(append([]byte{}, good[:4]...), make([]byte, 16)...), good[20:]...),
		"cut in info": good[:90],
		"cut in args": good[:len(good)-6],
	} {
		if _, err := startup.ParseLink(data, nil); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func FuzzParseLink(f *testing.F) {
	f.Add(startup.BuildLink(`C:\Program Files\App\app.exe`, "--minimized"))
	f.Add(link{flags: 1 | 2 | 0x20, idList: []byte{2, 0}, linkInfo: ansiLinkInfo(`C:\a\`, `b.exe`), strings: []string{"x"}}.bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		l, err := startup.ParseLink(data, nil)
		if err == nil {
			_ = l.TargetPath(`C:\Startup`, func(s string) string { return s })
		}
	})
}
