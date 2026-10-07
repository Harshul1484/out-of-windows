package safety

import (
	"strings"
	"testing"
)

// testLocations is a typical machine layout expressed as plain strings. No
// filesystem access is involved in guard tests.
func testLocations() Locations {
	return Locations{
		SystemDrive:      `C:\`,
		Windows:          `C:\Windows`,
		WindowsTemp:      `C:\Windows\Temp`,
		ProgramFiles:     `C:\Program Files`,
		ProgramFilesX86:  `C:\Program Files (x86)`,
		ProgramData:      `C:\ProgramData`,
		CommonFilesExtra: []string{`C:\Program Files\Common Files`},
		UsersRoot:        `C:\Users`,
		PublicProfile:    `C:\Users\Public`,
		UserProfile:      `C:\Users\alice`,
		RoamingAppData:   `C:\Users\alice\AppData\Roaming`,
		LocalAppData:     `C:\Users\alice\AppData\Local`,
		LocalLow:         `C:\Users\alice\AppData\LocalLow`,
		Temp:             `C:\Users\alice\AppData\Local\Temp`,
		UserContent: []string{
			`C:\Users\alice\Desktop`, `C:\Users\alice\Documents`, `C:\Users\alice\Downloads`,
			`C:\Users\alice\Pictures`, `D:\Media\Videos`, `C:\Users\alice\OneDrive`,
		},
		CriticalExtra: []string{
			`C:\Users\alice\AppData\Roaming\Microsoft\Windows\Start Menu`,
			`C:\Users\alice\AppData\Local\Microsoft`,
		},
		FixedDrives: []string{`C:\`, `D:\`},
		SelfDirs:    []string{`C:\Users\alice\AppData\Roaming\oow`, `C:\Users\alice\AppData\Local\oow`},
	}
}

// dangerousPaths must never be deletable by automatic cleanup, under any
// scope, in any spelling.
var dangerousPaths = []string{
	`C:\`, `D:\`, `E:\`,
	`C:\Windows`, `C:\Windows\System32`, `C:\Windows\System32\kernel32.dll`,
	`C:\Windows\System32\drivers\etc\hosts`, `C:\Windows\SysWOW64`, `C:\Windows\WinSxS`,
	`C:\Windows\explorer.exe`, `C:\Windows\Fonts`, `C:\Windows\Installer`,
	`C:\Program Files`, `C:\Program Files\Contoso\app.exe`, `C:\Program Files (x86)`,
	`C:\Program Files\Common Files`, `C:\ProgramData`, `C:\ProgramData\Microsoft\Windows\Start Menu`,
	`C:\Users`, `C:\Users\Public`, `C:\Users\alice`, `C:\Users\alice\AppData`,
	`C:\Users\alice\AppData\Local`, `C:\Users\alice\AppData\Roaming`, `C:\Users\alice\AppData\LocalLow`,
	`C:\Users\alice\AppData\Local\Temp`, `C:\Windows\Temp`,
	`C:\Users\alice\Documents`, `C:\Users\alice\Documents\thesis.docx`,
	`C:\Users\alice\Desktop\notes.txt`, `C:\Users\alice\Pictures\2024\img.jpg`,
	`C:\Users\alice\OneDrive\work.xlsx`, `D:\Media\Videos\clip.mp4`,
	`C:\Users\alice\AppData\Local\Microsoft`, `C:\Users\alice\AppData\Roaming\oow\config.json`,
	`C:\System Volume Information`, `C:\$Recycle.Bin`, `D:\$Recycle.Bin\S-1-5-21`,
	`C:\pagefile.sys`, `C:\hiberfil.sys`, `C:\swapfile.sys`, `C:\Recovery`, `C:\Boot`,
	`C:\Windows.old`, `C:\Config.Msi`,
}

func variants(p string) []string {
	out := []string{
		p,
		strings.ToUpper(p),
		strings.ToLower(p),
		strings.ReplaceAll(p, `\`, `/`),
		p + `\`,
		p + `.`,
		p + ` `,
		`\\?\` + p,
		`\??\` + p,
	}
	if len(p) > 3 && strings.HasPrefix(p, `C:\`) {
		// Traversal from an exempt directory back out to the target.
		out = append(out, `C:\Windows\Temp\..\..`+p[2:])
		out = append(out, `C:\Users\alice\AppData\Local\Temp\x\..\..\..\..\..\..`+p[2:])
	}
	return out
}

func TestDangerousPathsNeverDeletable(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	scopes := []string{
		"",
		`C:\Windows\Temp`,                   // the exempt system scope
		`C:\Users\alice\AppData\Local\Temp`, // a valid user scope
		`C:\`,                               // an invalid, over-broad scope
		`C:\Windows`,                        // an invalid, over-broad scope
	}
	for _, p := range dangerousPaths {
		for _, v := range variants(p) {
			for _, scope := range scopes {
				d := g.Check(Request{Path: v, Purpose: PurposeCleanup, Scope: scope})
				if d.Allowed {
					t.Errorf("cleanup allowed for dangerous path %q (scope %q)", v, scope)
				}
			}
		}
	}
}

func TestSystemAndCriticalNeverDeletableByUserSelection(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	for _, p := range dangerousPaths {
		if strings.Contains(p, `\Documents\`) || strings.Contains(p, `\Desktop\`) ||
			strings.Contains(p, `\Pictures\`) || strings.Contains(p, `\OneDrive\`) ||
			strings.Contains(p, `\Videos\`) {
			continue // user files inside content folders may be deleted by explicit choice
		}
		for _, v := range variants(p) {
			if d := g.Check(Request{Path: v, Purpose: PurposeUserSelected}); d.Allowed {
				t.Errorf("user-selected deletion allowed for %q", v)
			}
		}
	}
}

func TestDangerousPathsNeverValidRoots(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	for _, p := range dangerousPaths {
		if strings.EqualFold(p, `C:\Windows\Temp`) || strings.EqualFold(p, `C:\Users\alice\AppData\Local\Temp`) {
			continue // the Temp containers are valid roots (contents only)
		}
		for _, v := range variants(p) {
			if _, err := g.ValidateRoot(v); err == nil {
				t.Errorf("ValidateRoot accepted dangerous root %q", v)
			}
		}
	}
}

func TestAllowedCleanupPaths(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	cases := []struct{ path, scope string }{
		{`C:\Users\alice\AppData\Local\Temp\setup.log`, `C:\Users\alice\AppData\Local\Temp`},
		{`C:\Users\alice\AppData\Local\Temp\7z\payload.bin`, `C:\Users\alice\AppData\Local\Temp`},
		{`C:\Windows\Temp\MpCmdRun.log`, `C:\Windows\Temp`},
		{`C:\Users\alice\AppData\Local\D3DSCache\a\b.idx`, `C:\Users\alice\AppData\Local\D3DSCache`},
		{`C:\Users\alice\AppData\Local\Microsoft\Windows\WER\ReportArchive\r\Report.wer`,
			`C:\Users\alice\AppData\Local\Microsoft\Windows\WER\ReportArchive`},
	}
	for _, c := range cases {
		root, err := g.ValidateRoot(c.scope)
		if err != nil {
			t.Errorf("ValidateRoot(%q): %v", c.scope, err)
			continue
		}
		if d := g.Check(Request{Path: c.path, Purpose: PurposeCleanup, Scope: root}); !d.Allowed {
			t.Errorf("Check(%q) denied: %s", c.path, d.Reason)
		}
	}
}

func TestScopeIsEnforced(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	scope := `C:\Users\alice\AppData\Local\D3DSCache`
	for _, p := range []string{
		`C:\Users\alice\AppData\Local\D3DSCache`, // the root itself
		`C:\Users\alice\AppData\Local\D3DSCacheX\a`,
		`C:\Users\alice\AppData\Local\Other\a`,
		`C:\Users\alice\AppData\Local\D3DSCache\..\Other\a`,
	} {
		if d := g.Check(Request{Path: p, Purpose: PurposeCleanup, Scope: scope}); d.Allowed {
			t.Errorf("Check(%q) allowed outside scope %q", p, scope)
		}
	}
}

func TestSystemTreeOnlyInsideExemption(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	for _, root := range []string{
		`C:\Windows\Logs`, `C:\Windows\SoftwareDistribution`, `C:\Program Files\Contoso\cache`,
		`C:\ProgramData\Contoso\cache`, `C:\Windows\System32\LogFiles`,
	} {
		if _, err := g.ValidateRoot(root); err == nil {
			t.Errorf("ValidateRoot(%q) accepted a non-exempt system subtree", root)
		}
	}
	if _, err := g.ValidateRoot(`C:\Windows\Temp\sub`); err != nil {
		t.Errorf("ValidateRoot inside exemption: %v", err)
	}
}

func TestUserContentProtectedFromCleanupButNotUserChoice(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	p := `C:\Users\alice\Downloads\setup.exe`
	if d := g.Check(Request{Path: p, Purpose: PurposeCleanup}); d.Allowed {
		t.Errorf("cleanup allowed in Downloads")
	}
	if d := g.Check(Request{Path: p, Purpose: PurposeUserSelected}); !d.Allowed || d.Class != ClassUserContent {
		t.Errorf("user-selected deletion in Downloads = %+v", d)
	}
	if _, err := g.ValidateRoot(`C:\Users\alice\Downloads\cache`); err == nil {
		t.Errorf("cleanup root inside Downloads accepted")
	}
}

func TestWhitelistProtectsSubtreeAndAncestors(t *testing.T) {
	g := NewGuard(testLocations(), []string{`C:\Users\alice\AppData\Local\Temp\keep-me`})
	scope := `C:\Users\alice\AppData\Local\Temp`
	for _, p := range []string{
		`C:\Users\alice\AppData\Local\Temp\keep-me`,
		`C:\Users\alice\AppData\Local\Temp\KEEP-ME\inner\file.txt`,
	} {
		d := g.Check(Request{Path: p, Purpose: PurposeCleanup, Scope: scope})
		if d.Allowed || d.Class != ClassProtected {
			t.Errorf("whitelisted %q: %+v", p, d)
		}
	}
	if d := g.Check(Request{Path: `C:\Users\alice\AppData\Local\Temp\other.txt`, Purpose: PurposeCleanup, Scope: scope}); !d.Allowed {
		t.Errorf("non-whitelisted file denied: %s", d.Reason)
	}
	if _, err := g.ValidateRoot(`C:\Users\alice\AppData\Local\Temp\keep-me\sub`); err == nil {
		t.Errorf("root inside whitelist accepted")
	}
}

// A hostile or mistaken %TEMP% must not turn into a broad cleanup root.
func TestRedirectedTempCannotWidenRoots(t *testing.T) {
	for _, temp := range []string{
		`C:\`, `C:\Users`, `C:\Users\alice`, `C:\Users\alice\AppData\Local`,
		`C:\Users\alice\Documents`, `C:\Windows`, `C:\Program Files`,
	} {
		l := testLocations()
		l.Temp = temp
		g := NewGuard(l, nil)
		if _, err := g.ValidateRoot(temp); err == nil {
			t.Errorf("TEMP=%q accepted as cleanup root", temp)
		}
	}
}

func TestBaselineProtectionWithoutDiscovery(t *testing.T) {
	g := NewGuard(Locations{}, nil) // discovery failed completely
	for _, p := range []string{
		`C:\Windows\System32\x.dll`, `C:\Program Files\x`, `C:\Program Files (x86)\x`,
		`C:\ProgramData\x`, `C:\Users`, `C:\`, `C:\pagefile.sys`,
	} {
		if d := g.Check(Request{Path: p, Purpose: PurposeCleanup}); d.Allowed {
			t.Errorf("baseline guard allowed %q", p)
		}
	}
}

func TestSensitiveLocationsNeverDeletable(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	for _, p := range []string{
		`C:\Users\alice\.ssh\id_ed25519`, `C:\Users\alice\.ssh`, `C:\Users\alice\.aws\credentials`,
		`C:\Users\alice\AppData\Roaming\Microsoft\Protect\S-1-5-21\key`,
		`C:\Users\alice\AppData\Roaming\Microsoft\Credentials\ABCD`,
		`C:\Users\alice\AppData\Local\Microsoft\Vault\x`,
		`C:\Users\alice\AppData\Roaming\Bitwarden\data.json`,
		`C:\Users\alice\AppData\Roaming\Electrum\wallets\default_wallet`,
		`C:\Users\alice\.claude\settings.json`, `C:\Users\alice\.ollama\models\blob`,
		`C:\Users\alice\AppData\Roaming\Code\User\settings.json`,
		`C:\Users\alice\AppData\Local\Docker\wsl\disk\docker_data.vhdx`,
		`C:\Users\alice\.cargo\bin\cargo.exe`,
	} {
		for _, purpose := range []Purpose{PurposeCleanup, PurposeUserSelected} {
			d := g.Check(Request{Path: p, Purpose: purpose, Scope: `C:\Users\alice`})
			if d.Allowed {
				t.Errorf("%q allowed (purpose %d)", p, purpose)
			}
		}
	}
	for _, root := range []string{`C:\Users\alice\.ssh`, `C:\Users\alice\.aws\cache`, `C:\Users\alice\AppData\Roaming\Code\User\workspaceStorage`} {
		if _, err := g.ValidateRoot(root); err == nil {
			t.Errorf("sensitive root %q accepted", root)
		}
	}
	// Cache folders next to sensitive ones are still valid roots.
	for _, root := range []string{`C:\Users\alice\AppData\Roaming\Code\Cache`, `C:\Users\alice\.cargo\registry\cache`} {
		if _, err := g.ValidateRoot(root); err != nil {
			t.Errorf("cache root %q rejected: %v", root, err)
		}
	}
}

func TestSensitiveFileTypesNeverAutoCleaned(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	scope := `C:\Users\alice\AppData\Local\Temp`
	for _, name := range []string{
		"ext4.vhdx", "disk.VHD", "vault.kdbx", "archive.pst", "mail.ost", "cert.pfx", "server.key",
		"id_rsa", "id_ed25519.pub", "wallet.dat", "Login Data", "Cookies", "key4.db", "logins.json",
	} {
		p := scope + `\x\` + name
		if d := g.Check(Request{Path: p, Purpose: PurposeCleanup, Scope: scope}); d.Allowed || d.Class != ClassSensitive {
			t.Errorf("cleanup of %s = %+v", name, d)
		}
	}
	// A user may still delete such a file explicitly (e.g. an old VM disk in Downloads).
	if d := g.Check(Request{Path: `C:\Users\alice\Downloads\old-vm.vhdx`, Purpose: PurposeUserSelected}); !d.Allowed {
		t.Errorf("explicit deletion denied: %s", d.Reason)
	}
	for _, ok := range []string{"setup.log", "keyboard.txt", "history.txt.tmp", "cookies-backup.zip"} {
		if IsSensitiveName(ok) {
			t.Errorf("%s wrongly classified as sensitive", ok)
		}
	}
}

func TestLeftoverPurpose(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	pf, pd := `C:\Program Files`, `C:\ProgramData`
	roam, local := `C:\Users\alice\AppData\Roaming`, `C:\Users\alice\AppData\Local`
	progs := local + `\Programs`
	allowed := [][2]string{
		{pf + `\Contoso`, pf},
		{pf + `\Contoso\Studio`, pf},
		{`C:\Program Files (x86)\Fabrikam\Player\bin`, `C:\Program Files (x86)`},
		{pd + `\Contoso`, pd},
		{roam + `\Contoso\Studio`, roam},
		{local + `\Contoso Studio`, local},
		{progs + `\Fabrikam Player`, progs},
	}
	for _, c := range allowed {
		if d := g.Check(Request{Path: c[0], Purpose: PurposeLeftover, Scope: c[1]}); !d.Allowed {
			t.Errorf("leftover %s denied: %s", c[0], d.Reason)
		}
	}
	denied := [][2]string{
		{pf, pf},                                                // the root itself
		{pf + `\Common Files\Contoso`, pf},                      // shared
		{pf + `\WindowsApps\Contoso.App_1.0`, pf},               // Store packages
		{pf + `\Microsoft Office`, pf},                          // Microsoft
		{pf + `\Windows Defender`, pf},                          // Windows
		{pf + `\Contoso\a\b\c`, pf},                             // too deep
		{pd + `\Microsoft\Windows\Start Menu`, pd},              // Windows
		{pd + `\Package Cache\{1234}`, pd},                      // installer cache
		{local + `\Temp\Contoso`, local},                        // temp
		{local + `\Packages\Contoso.App_8w`, local},             // AppX data
		{local + `\Microsoft\Teams`, local},                     // Microsoft
		{progs + `\Fabrikam`, local},                            // wrong (less specific) root
		{roam + `\oow`, roam},                                   // this tool
		{roam + `\Bitwarden`, roam},                             // sensitive
		{roam + `\Microsoft`, roam},                             // critical
		{`C:\Users\alice\Documents\Contoso`, `C:\Users\alice`},  // not a leftover root
		{`C:\Windows\Contoso`, `C:\Windows`},                    // not a leftover root
		{pf + `\Contoso`, ""},                                   // no scope
		{`C:\Program Files\Contoso\..\..\Windows\System32`, pf}, // traversal
	}
	for _, c := range denied {
		if d := g.Check(Request{Path: c[0], Purpose: PurposeLeftover, Scope: c[1]}); d.Allowed {
			t.Errorf("leftover %s (scope %q) allowed", c[0], c[1])
		}
	}
	if r, ok := g.LeftoverRootFor(progs + `\Fabrikam\x`); !ok || r.Kind != "user programs" {
		t.Errorf("LeftoverRootFor = %+v, %v", r, ok)
	}
}

func TestCleanupWithoutScopeDenied(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	d := g.Check(Request{Path: `E:\scratch\file.tmp`, Purpose: PurposeCleanup})
	if d.Allowed {
		t.Error("automatic cleanup allowed without a scope")
	}
	if d := g.Check(Request{Path: `E:\scratch\file.tmp`, Purpose: PurposeUserSelected}); !d.Allowed {
		t.Errorf("user-selected ordinary path denied: %s", d.Reason)
	}
}

func TestUNCAndInvalidPathsDenied(t *testing.T) {
	g := NewGuard(testLocations(), nil)
	for _, p := range []string{
		`\\server\share\file.txt`, `\\?\UNC\server\share\x`, `relative\path`, ``,
		`C:\a\file.txt:hidden`, `\\?\Volume{abc}\x`, `C:temp`,
	} {
		if d := g.Check(Request{Path: p, Purpose: PurposeUserSelected}); d.Allowed {
			t.Errorf("Check(%q) allowed", p)
		}
	}
}

func TestExpandRejectsUnknownAndMissingTokens(t *testing.T) {
	l := testLocations()
	if _, err := l.Expand(`{Nope}\x`); err == nil {
		t.Error("unknown token accepted")
	}
	l.LocalAppData = ""
	if _, err := l.Expand(`{LocalAppData}\D3DSCache`); err == nil {
		t.Error("missing location expanded to a relative path")
	}
	l = testLocations()
	got, err := l.Expand(`{LocalAppData}\D3DSCache`)
	if err != nil || got != `C:\Users\alice\AppData\Local\D3DSCache` {
		t.Errorf("Expand = %q, %v", got, err)
	}
}

// FuzzGuardScope checks the core invariant: anything the guard allows for
// cleanup lies strictly inside the scope and never inside System32.
func FuzzGuardScope(f *testing.F) {
	g := NewGuard(testLocations(), nil)
	for _, s := range []string{
		`C:\Windows\Temp\a`, `C:\Windows\Temp\..\System32\x`, `C:\Users\alice\AppData\Local\Temp\x`,
		`\\?\C:\Windows\Temp\..\..\Windows\System32`, `C:/Windows/Temp/./x.`,
	} {
		f.Add(s, `C:\Windows\Temp`)
		f.Add(s, `C:\Users\alice\AppData\Local\Temp`)
	}
	f.Fuzz(func(t *testing.T, p, scope string) {
		d := g.Check(Request{Path: p, Purpose: PurposeCleanup, Scope: scope})
		if !d.Allowed {
			return
		}
		n, err := Normalize(p)
		if err != nil {
			t.Fatalf("allowed unparseable path %q", p)
		}
		s, _ := Normalize(scope)
		if s == "" || !IsStrictlyWithin(n, s) {
			t.Fatalf("allowed %q outside scope %q", n, scope)
		}
		for _, forbidden := range []string{`C:\Windows\System32`, `C:\Program Files`, `C:\Users\alice\Documents`} {
			if IsWithin(n, forbidden) {
				t.Fatalf("allowed %q inside %s", n, forbidden)
			}
		}
	})
}

// The tool's own folders may be removed only through PurposeSelfRemove, only
// exactly (never a parent or child), and only when nothing else protects them.
func TestSelfRemovePurpose(t *testing.T) {
	cfg, data := `C:\Users\alice\AppData\Roaming\oow`, `C:\Users\alice\AppData\Local\oow`
	g := NewGuard(testLocations(), nil)
	for _, p := range []string{cfg, data, `\\?\C:\Users\alice\AppData\Local\OOW\`} {
		if d := g.Check(Request{Path: p, Purpose: PurposeSelfRemove}); !d.Allowed {
			t.Errorf("own folder %s denied: %s", p, d.Reason)
		}
		// Every other purpose still refuses it.
		for _, purpose := range []Purpose{PurposeCleanup, PurposeUserSelected, PurposeLeftover} {
			if d := g.Check(Request{Path: p, Purpose: purpose, Scope: `C:\Users\alice\AppData\Local`}); d.Allowed {
				t.Errorf("own folder %s allowed for purpose %d", p, purpose)
			}
		}
	}
	for _, p := range append([]string{
		data + `\history.jsonl`,                // a child
		data + `\logs`,                         // a child folder
		`C:\Users\alice\AppData\Local`,         // a parent
		`C:\Users\alice\AppData\Local\oow-old`, // a lookalike
		`C:\Users\alice\AppData\Local\Contoso`, // unrelated
		`C:\Users\alice\Documents`,             // user content
	}, dangerousPaths...) {
		if d := g.Check(Request{Path: p, Purpose: PurposeSelfRemove}); d.Allowed {
			t.Errorf("self-remove allowed %s", p)
		}
	}

	// Own folders that something else protects are refused.
	risky := testLocations()
	risky.SelfDirs = []string{
		`C:\Users\alice\Documents\oow`,          // user content (e.g. OOW_DATA_DIR)
		`C:\Program Files\oow`,                  // system tree
		`C:\Users\alice\.ssh`,                   // sensitive
		`C:\Users\alice\AppData\Local\Temp`,     // critical
		`C:\Users\alice\AppData\Local\Contoso`,  // whitelisted below
		`C:\Users\alice\AppData\Local\Fabrikam`, // contains a whitelisted path
		`C:\Users`,                              // critical
		`D:\`,                                   // drive root
	}
	g = NewGuard(risky, []string{`C:\Users\alice\AppData\Local\Contoso`, `C:\Users\alice\AppData\Local\Fabrikam\keep`})
	for _, p := range risky.SelfDirs {
		if d := g.Check(Request{Path: p, Purpose: PurposeSelfRemove}); d.Allowed {
			t.Errorf("self-remove allowed protected own folder %s", p)
		}
	}
}
