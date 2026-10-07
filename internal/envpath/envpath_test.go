package envpath_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/envpath"
	"github.com/Harshul1484/out-of-windows/internal/system"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type layout struct {
	f       *testutil.Fixture
	profile string
	opts    envpath.Options
}

func newLayout(t *testing.T) *layout {
	f := testutil.NewFixture(t)
	l := &layout{f: f, profile: f.Path(`Users\me`)}
	f.Dir(`Users\me\AppData\Local\Microsoft\WindowsApps`, time.Hour)
	f.Dir(`Program Files\Tool\bin`, time.Hour)
	f.Dir(`Users\me\.cargo`, time.Hour)
	vars := map[string]string{"localappdata": f.Path(`Users\me\AppData\Local`), "userprofile": l.profile,
		"programfiles": f.Path(`Program Files`)}
	l.opts = envpath.Options{
		Expand: func(s string) (string, bool) {
			ok := true
			for {
				i := strings.Index(s, "%")
				if i < 0 {
					return s, ok
				}
				j := strings.Index(s[i+1:], "%")
				if j < 0 {
					return s, ok
				}
				name := s[i+1 : i+1+j]
				v, found := vars[strings.ToLower(name)]
				if !found {
					return s, false
				}
				s = s[:i] + v + s[i+2+j:]
			}
		},
		Probe:   system.ProbePath,
		Profile: l.profile,
	}
	return l
}

func TestAnalyze(t *testing.T) {
	l := newLayout(t)
	raw := strings.Join([]string{
		`%LOCALAPPDATA%\Microsoft\WindowsApps`, // 0 ok
		`%ProgramFiles%\Tool\bin`,              // 1 ok
		l.f.Path(`Program Files\Gone\bin`),     // 2 missing outside the profile
		`%USERPROFILE%\go\bin`,                 // 3 missing inside the profile: review
		"",                                     // 4 empty
		strings.ToUpper(l.f.Path(`Program Files\Tool\bin`)) + `\`, // 5 duplicate of 1
		`%UNDEFINED%\bin`,                  // 6 unknown
		`\\server\share\tools`,             // 7 unknown (network)
		l.f.Path(`Program Files\Gone\bin`), // 8 duplicate of 2 (counted once as missing)
	}, ";") + ";"
	r := envpath.Analyze(envpath.User, envpath.Value{Raw: raw, Expand: true, Exists: true}, l.opts)
	if r.Missing != 2 || r.Duplicates != 2 || r.Empty != 1 || r.Unknown != 2 || r.Quoted {
		t.Fatalf("counts: missing=%d dup=%d empty=%d unknown=%d\n%+v", r.Missing, r.Duplicates, r.Empty, r.Unknown, r.Entries)
	}
	if len(r.Entries) != 9 {
		t.Fatalf("entries = %d (the trailing ';' is not an entry)", len(r.Entries))
	}
	want := []envpath.Problem{"", "", "missing", "missing", "empty", "duplicate", "", "", "duplicate"}
	for i, e := range r.Entries {
		if e.Problem != want[i] || e.Index != i {
			t.Errorf("entry %d %q: problem %q, want %q", i, e.Raw, e.Problem, want[i])
		}
	}
	if !r.Entries[3].Review || r.Entries[2].Review {
		t.Error("only the missing folder inside the profile needs review")
	}
	if r.Entries[5].DuplicateOf != 1 || r.Entries[8].DuplicateOf != 2 {
		t.Errorf("duplicate_of = %d, %d", r.Entries[5].DuplicateOf, r.Entries[8].DuplicateOf)
	}
	if r.Length != len(raw) || r.ExpandedLength <= 0 {
		t.Errorf("lengths %d %d", r.Length, r.ExpandedLength)
	}
	if r.Issues() != 5 {
		t.Errorf("issues = %d", r.Issues())
	}
}

func TestAnalyzeQuotedAndEmpty(t *testing.T) {
	l := newLayout(t)
	r := envpath.Analyze(envpath.User, envpath.Value{Raw: `"` + l.f.Path(`Program Files\Tool\bin`) + `";C:\x`, Exists: true}, l.opts)
	if !r.Quoted || r.Entries[0].State != system.Present {
		t.Errorf("quoted entry = %+v", r)
	}
	r = envpath.Analyze(envpath.User, envpath.Value{}, l.opts)
	if len(r.Entries) != 0 || r.Issues() != 0 {
		t.Errorf("unset PATH = %+v", r)
	}
}

func TestRemoveKeepsEverythingElse(t *testing.T) {
	v := envpath.Value{Raw: `%A%\bin;C:\Gone;;C:\Dup;c:\dup\;`, Expand: true, Exists: true}
	got := envpath.Remove(v, []int{1, 2, 4})
	if got.Raw != `%A%\bin;C:\Dup;` || !got.Expand || !got.Exists {
		t.Errorf("Remove = %+v", got)
	}
	if got := envpath.Remove(v, nil); got != v {
		t.Errorf("Remove(nothing) = %+v", got)
	}
}

func TestCombinedLength(t *testing.T) {
	m := envpath.Report{ExpandedLength: 100}
	u := envpath.Report{ExpandedLength: 50}
	if n := envpath.CombinedLength(m, u); n != 151 {
		t.Errorf("combined = %d", n)
	}
	if n := envpath.CombinedLength(m, envpath.Report{}); n != 100 {
		t.Errorf("combined without user = %d", n)
	}
}

func TestRegFileRoundTrip(t *testing.T) {
	for _, v := range []envpath.Value{
		{Raw: `%USERPROFILE%\bin;C:\Program Files\Zoë "quoted";C:\x`, Expand: true, Exists: true},
		{Raw: `C:\plain`, Expand: false, Exists: true},
		{Raw: "", Expand: true, Exists: true},
	} {
		data := envpath.RegFile(v)
		if data[0] != 0xFF || data[1] != 0xFE {
			t.Fatal("no UTF-16LE BOM")
		}
		got, err := envpath.ParseRegFile(data)
		if err != nil || got != v {
			t.Errorf("round trip %+v = %+v, %v", v, got, err)
		}
	}
	text := string(envpath.RegFile(envpath.Value{Raw: "A", Expand: true, Exists: true})[2:])
	text = strings.ReplaceAll(text, "\x00", "")
	if !strings.Contains(text, `[HKEY_CURRENT_USER\Environment]`) || !strings.Contains(text, `"Path"=hex(2):41,00,00,00`) {
		t.Errorf("reg file = %q", text)
	}
}

func TestWriteBackupNeverOverwrites(t *testing.T) {
	dir := testutil.Dir(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	v := envpath.Value{Raw: `C:\a;C:\b`, Expand: true, Exists: true}
	p1, err := envpath.WriteBackup(dir, v, now)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := envpath.WriteBackup(dir, envpath.Value{Raw: `C:\other`, Exists: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 || filepath.Dir(p1) != dir || !strings.HasSuffix(p1, ".reg") {
		t.Fatalf("backups %s %s", p1, p2)
	}
	data, _ := os.ReadFile(p1)
	if got, err := envpath.ParseRegFile(data); err != nil || got != v {
		t.Errorf("first backup = %+v, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
}

func TestRealPathValues(t *testing.T) {
	testutil.SkipUnlessRealSystem(t)
	s := envpath.System{}
	opts := envpath.Options{Expand: s.Expand, Probe: system.ProbePath}
	for _, scope := range []envpath.Scope{envpath.Machine, envpath.User} {
		v, err := s.Read(scope)
		if err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		r := envpath.Analyze(scope, v, opts)
		t.Logf("%s PATH: %d entries, %d missing, %d duplicates, %d empty, %d unknown, length %d (expanded %d), expandable %v",
			scope, len(r.Entries), r.Missing, r.Duplicates, r.Empty, r.Unknown, r.Length, r.ExpandedLength, v.Expand)
		if scope == envpath.Machine && (!v.Exists || len(r.Entries) == 0) {
			t.Error("the machine PATH should exist")
		}
	}
}
