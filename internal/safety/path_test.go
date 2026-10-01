package safety

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
		err      error
	}{
		{`C:\Windows`, `C:\Windows`, nil},
		{`c:\windows\`, `C:\windows`, nil},
		{`C:/Windows/System32`, `C:\Windows\System32`, nil},
		{`C:\\Windows\\\System32\\`, `C:\Windows\System32`, nil},
		{`C:\Windows\.\System32`, `C:\Windows\System32`, nil},
		{`C:\Users\me\..\..\Windows`, `C:\Windows`, nil},
		{`C:\..\..\Windows`, `C:\Windows`, nil},
		{`C:\Windows.`, `C:\Windows`, nil},
		{`C:\Windows. . .\System32 `, `C:\Windows\System32`, nil},
		{`\\?\C:\Windows`, `C:\Windows`, nil},
		{`\??\C:\Windows`, `C:\Windows`, nil},
		{`\\.\C:\Windows`, `C:\Windows`, nil},
		{`\\?\UNC\server\share\dir`, `\\server\share\dir`, nil},
		{`\\server\share`, `\\server\share`, nil},
		{`C:\`, `C:\`, nil},
		{`C:\.`, `C:\`, nil},
		{`C:\..`, `C:\`, nil},
		{`d:\Data\..\`, `D:\`, nil},
		{``, "", ErrEmptyPath},
		{`   `, "", ErrEmptyPath},
		{`Windows\System32`, "", ErrRelativePath},
		{`\Windows`, "", ErrRelativePath},
		{`C:Windows`, "", ErrRelativePath},
		{`C:`, "", ErrRelativePath},
		{`C:\file.txt:stream`, "", ErrInvalidPath},
		{`C:\Windows\*`, "", ErrInvalidPath},
		{`C:\Windows\Sys?em32`, "", ErrInvalidPath},
		{"C:\\Win\x00dows", "", ErrInvalidPath},
		{`\\?\Volume{1234}\Windows`, "", ErrUnsupportedPath},
		{`\\?\GLOBALROOT\Device\HarddiskVolume1`, "", ErrUnsupportedPath},
		{`\\.\PhysicalDrive0`, "", ErrUnsupportedPath},
		{`\\server`, "", ErrUnsupportedPath},
		{`\\\.\0`, "", ErrUnsupportedPath},
		{`\\.\..`, "", ErrUnsupportedPath},
		{`\\?\UNC\.\share`, "", ErrUnsupportedPath},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("Normalize(%q) error = %v, want %v", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestIsWithin(t *testing.T) {
	cases := []struct {
		child, parent string
		within        bool
		strict        bool
	}{
		{`C:\Windows\System32`, `C:\Windows`, true, true},
		{`C:\WINDOWS\system32`, `C:\Windows`, true, true},
		{`C:\Windows`, `C:\Windows`, true, false},
		{`C:\windows`, `C:\Windows`, true, false},
		{`C:\WindowsApps`, `C:\Windows`, false, false},
		{`C:\Windows.old\x`, `C:\Windows`, false, false},
		{`C:\anything`, `C:\`, true, true},
		{`D:\anything`, `C:\`, false, false},
		{`C:\`, `C:\`, true, false},
		{`\\srv\share\a`, `\\srv\share`, true, true},
	}
	for _, c := range cases {
		if got := IsWithin(c.child, c.parent); got != c.within {
			t.Errorf("IsWithin(%q, %q) = %v, want %v", c.child, c.parent, got, c.within)
		}
		if got := IsStrictlyWithin(c.child, c.parent); got != c.strict {
			t.Errorf("IsStrictlyWithin(%q, %q) = %v, want %v", c.child, c.parent, got, c.strict)
		}
	}
}

func TestIsVolumeRoot(t *testing.T) {
	for p, want := range map[string]bool{
		`C:\`:            true,
		`Z:\`:            true,
		`C:\Windows`:     false,
		`\\srv\share`:    true,
		`\\srv\share\x`:  false,
		`C:\Windows\Sys`: false,
	} {
		if got := IsVolumeRoot(p); got != want {
			t.Errorf("IsVolumeRoot(%q) = %v, want %v", p, got, want)
		}
	}
}

func FuzzNormalizeIdempotent(f *testing.F) {
	for _, s := range []string{`C:\Windows`, `\\?\C:\a\..\b`, `C:/x/./y.`, `\\s\sh\x`, `C:\a. \b`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := Normalize(s)
		if err != nil {
			return
		}
		n2, err := Normalize(n)
		if err != nil || n2 != n {
			t.Fatalf("Normalize not idempotent: %q -> %q -> %q (%v)", s, n, n2, err)
		}
	})
}
