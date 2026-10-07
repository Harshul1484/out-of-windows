package installer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/apps"
	"github.com/Harshul1484/out-of-windows/internal/safety"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func write(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := sandbox.WriteData(p, data, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInspectIdentifiesByContent(t *testing.T) {
	dir := testutil.Dir(t)
	pad := make([]byte, 8192)
	inno := sandbox.BuildPE(sandbox.PEOptions{
		Version:     map[string]string{"ProductName": "Fabrikam Player", "CompanyName": "Fabrikam, Inc.", "ProductVersion": "2.0.1", "FileDescription": "Fabrikam Player Setup"},
		FileVersion: [4]uint16{2, 0, 1, 0},
		Overlay:     append(append([]byte{}, sandbox.InnoOverlay...), pad...),
	})
	setupEXE := sandbox.BuildPE(sandbox.PEOptions{Overlay: append(append([]byte{}, sandbox.NSISOverlay...), pad...)})
	cases := []struct {
		name   string
		data   []byte
		typ    Type
		engine string
	}{
		{"inno.exe", inno, TypeEXE, "Inno Setup"},
		{"nsis.exe", setupEXE, TypeEXE, "NSIS"},
		{"burn.exe", sandbox.BuildPE(sandbox.PEOptions{Sections: []string{".wixburn"}}), TypeEXE, "WiX Burn"},
		{"described.exe", sandbox.BuildPE(sandbox.PEOptions{Version: map[string]string{"FileDescription": "Contoso Web Installer"}}), TypeEXE, ""},
		{"app.msix", sandbox.BuildZip(sandbox.ZipEntry{Name: "AppxManifest.xml", Data: sandbox.AppxManifest("Contoso.App", "CN=Contoso", "1.0.0.0", "Contoso App")}), TypeMSIX, ""},
		{"app.msixbundle", sandbox.BuildZip(sandbox.ZipEntry{Name: "AppxMetadata/AppxBundleManifest.xml", Data: []byte(`<Bundle><Identity Name="Contoso.App" Publisher="CN=Contoso" Version="1.0.0.0"/></Bundle>`)}), TypeBundle, ""},
		{"setup.zip", sandbox.BuildZip(sandbox.ZipEntry{Name: "setup.exe", Data: setupEXE}), TypeZIP, "NSIS"},
		{"media.iso", sandbox.BuildISO("MEDIA", map[string][]byte{"SETUP.EXE": setupEXE}), TypeISO, ""},
		{"photos.iso", sandbox.BuildISO("PHOTOS", map[string][]byte{"A.JPG": []byte("jpg")}), TypeISO, ""},
	}
	for _, c := range cases {
		d, err := Inspect(write(t, dir, c.name, c.data))
		if err != nil || d == nil {
			t.Errorf("%s: not identified (err %v)", c.name, err)
			continue
		}
		if d.Type != c.typ || d.Engine != c.engine || len(d.Evidence) == 0 {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	if d, _ := Inspect(filepath.Join(dir, "inno.exe")); d.Product != "Fabrikam Player" || d.Version != "2.0.1" || d.Publisher != "Fabrikam, Inc." {
		t.Errorf("version resource not read: %+v", d)
	}
	if d, _ := Inspect(filepath.Join(dir, "app.msix")); d.Identity != "Contoso.App" || d.Product != "Contoso App" || d.Publisher != "Contoso" {
		t.Errorf("manifest not read: %+v", d)
	}
	if d, _ := Inspect(filepath.Join(dir, "photos.iso")); d.Review == "" {
		t.Error("ISO without setup files must be review-only")
	}

	msiPath := filepath.Join(dir, "product.msi")
	if err := sandbox.CreateMSI(msiPath, map[string]string{"ProductName": "Northwind Sync", "ProductVersion": "3.1",
		"Manufacturer": "Northwind Traders", "ProductCode": sandbox.NorthwindProductCode}); err != nil {
		t.Fatal(err)
	}
	d, err := Inspect(msiPath)
	if err != nil || d == nil || d.Type != TypeMSI || d.Product != "Northwind Sync" || d.ProductCode != sandbox.NorthwindProductCode {
		t.Errorf("MSI: %+v, %v", d, err)
	}

	// Never installers, whatever their names say.
	for name, data := range map[string][]byte{
		"portable.exe": sandbox.BuildPE(sandbox.PEOptions{Version: map[string]string{"ProductName": "Tailspin Terminal", "FileDescription": "SSH client"}}),
		"library.exe":  sandbox.BuildPE(sandbox.PEOptions{DLL: true, Overlay: sandbox.NSISOverlay}),
		"notes.msi":    []byte("This is not an installer."),
		"report.msi":   sandbox.BuildCFB(sandbox.CLSIDWordDocument),
		"photos.zip":   sandbox.BuildZip(sandbox.ZipEntry{Name: "a.jpg", Data: make([]byte, 4096)}),
		"nested.zip":   sandbox.BuildZip(sandbox.ZipEntry{Name: "app/setup.exe", Data: setupEXE}),
		"fake.zip":     sandbox.BuildZip(sandbox.ZipEntry{Name: "setup.exe", Data: []byte(strings.Repeat("not a program ", 200))}),
		"setup.pdf":    []byte("%PDF-1.7\n%%EOF\n"),
		"setup.exe":    []byte("MZ but nothing else"),
		"empty.iso":    make([]byte, 0x9000),
	} {
		if d, err := Inspect(write(t, dir, name, data)); d != nil {
			t.Errorf("%s identified as an installer: %+v (%v)", name, d, err)
		}
	}
}

func TestMatchInstalledIsExact(t *testing.T) {
	inv := &apps.Inventory{Apps: []apps.App{
		{ID: "reg:hklm64:{AAAA}", Name: "Northwind Sync", Version: "3.1", ProductCode: "{AAAA}"},
		{ID: "reg:hkcu:VSCode", Name: "Microsoft Visual Studio Code (User)", Publisher: "Microsoft Corporation", Version: "1.90.0"},
		{ID: "appx:Contoso.Notes_2.0.0.0_x64__abc", Name: "Notes", Source: apps.SourceAppX, PackageName: "Contoso.Notes_2.0.0.0_x64__abc"},
		{ID: "reg:hklm64:Setup", Name: "Setup", Version: "1.0"},
	}}
	cases := []struct {
		d    Details
		want string
		how  string
	}{
		{Details{Type: TypeMSI, ProductCode: "{aaaa}", Product: "Something Else"}, "installed", "product code"},
		{Details{Type: TypeEXE, Product: "Visual Studio Code", Publisher: "Microsoft Corporation"}, "installed", "product name"},
		{Details{Type: TypeMSIX, Identity: "Contoso.Notes", Product: "Contoso Notes"}, "installed", "package identity"},
		{Details{Type: TypeEXE, Product: "Northwind Sync Pro"}, "not-found", ""},        // similar is not enough
		{Details{Type: TypeEXE, Product: "Setup", Description: "Setup"}, "unknown", ""}, // generic names never match
		{Details{Type: TypeZIP}, "unknown", ""},
	}
	for _, c := range cases {
		got := matchInstalled(&c.d, inv)
		if got.Status != c.want || got.Match != c.how {
			t.Errorf("%+v: got %+v, want %s/%s", c.d, got, c.want, c.how)
		}
	}
	if got := matchInstalled(&Details{Type: TypeEXE, Product: "Northwind Sync"}, nil); got.Status != "unknown" {
		t.Errorf("no inventory: %+v", got)
	}
	if !newerThan("2.1", "2.0.9") || newerThan("2.0", "2.0.0") || newerThan("", "1") || newerThan("1.x", "1") {
		t.Error("newerThan is wrong")
	}
}

type world struct {
	root string
	env  *Env
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := testutil.Dir(t)
	if err := sandbox.Seed(root); err != nil {
		t.Fatal(err)
	}
	l := sandbox.Locations(root)
	inv, err := sandbox.Apps{Root: root}.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return &world{root: root, env: &Env{Guard: safety.NewGuard(l, nil), Locations: l, Inventory: inv}}
}

func (w *world) find(t *testing.T) *Result {
	t.Helper()
	folders := ResolveFolders(w.env, []string{sandbox.DownloadsDir(w.root), sandbox.DesktopDir(w.root), sandbox.DocumentsDir(w.root)})
	for _, f := range folders {
		if f.Status != "ok" {
			t.Fatalf("folder %+v", f)
		}
	}
	return Find(context.Background(), w.env, folders, nil)
}

func TestFindInSandbox(t *testing.T) {
	w := newWorld(t)
	res := w.find(t)
	got := map[string]*Package{}
	var names []string
	for _, p := range res.Packages {
		got[p.Name] = p
		names = append(names, p.Name)
		testutil.AssertInSandbox(t, p.Path)
	}
	sort.Strings(names)
	want := map[string]struct {
		status    Status
		installed string
	}{
		"FabrikamPlayerSetup-2.0.1.exe":  {StatusReady, "installed"},
		"NorthwindSync-3.1.msi":          {StatusReady, "installed"},
		"ContosoStudio_4.2.0.0_x64.msix": {StatusReady, "installed"},
		"WingtipToys-9.0-setup.exe":      {StatusReview, "installed"}, // downloaded 2 days ago
		"ProsewareEditor-5.0-x64.msi":    {StatusReview, "not-found"},
		"AdventureWorks-1.2.zip":         {StatusReview, "unknown"},
		"Woodgrove-Bank-Setup.iso":       {StatusReview, "unknown"},
		"backup-2019.iso":                {StatusReview, "unknown"},
	}
	if len(got) != len(want) {
		t.Errorf("found %v", names)
	}
	for name, wnt := range want {
		p := got[name]
		if p == nil {
			t.Errorf("%s not found (found %v)", name, names)
			continue
		}
		if p.Status != wnt.status || p.Selected != (wnt.status == StatusReady) || p.Installed.Status != wnt.installed {
			t.Errorf("%s: status %s selected %v installed %+v reasons %v", name, p.Status, p.Selected, p.Installed, p.Reasons)
		}
	}
	if p := got["NorthwindSync-3.1.msi"]; p != nil && p.Installed.Match != "product code" {
		t.Errorf("Northwind matched by %q", p.Installed.Match)
	}
	for _, never := range []string{"tailspin-terminal.exe", "setup.pdf", "vacation-photos.zip", "notes.msi", "old-report.msi", "too-deep-setup.exe"} {
		if got[never] != nil {
			t.Errorf("%s offered as an installer", never)
		}
	}
}

func TestRecycleVerifiesEachPackage(t *testing.T) {
	w := newWorld(t)
	res := w.find(t)
	var chosen []*Package
	for _, p := range res.Packages {
		if p.Selected {
			chosen = append(chosen, p)
		}
	}
	if len(chosen) != 3 {
		t.Fatalf("selected %d packages", len(chosen))
	}
	// One package changes after the scan: it must be kept.
	changed := chosen[0]
	if err := os.WriteFile(changed.Path, []byte("replaced"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := Recycle(context.Background(), w.env.Guard, chosen, sandbox.Recycler{Root: w.root})
	if len(out.Recycled) != 2 || len(out.Skipped) != 1 || out.Skipped[0].Reason != "changed since it was scanned" || out.Errors != 0 {
		t.Fatalf("outcome %+v", out)
	}
	for _, r := range out.Recycled {
		if _, err := os.Lstat(r.Path); err == nil {
			t.Errorf("%s still exists", r.Path)
		}
	}
	if _, err := os.Lstat(changed.Path); err != nil {
		t.Error("changed package was removed")
	}
	bin, _ := os.ReadDir(filepath.Join(sandbox.RecycleBinDir(w.root), "S-1-5-21-sandbox"))
	if len(bin) != 3+2 {
		t.Errorf("Recycle Bin entries = %d, want 5", len(bin))
	}
}

func TestWhitelistAndRefusedFolders(t *testing.T) {
	w := newWorld(t)
	l := sandbox.Locations(w.root)
	w.env.Guard = safety.NewGuard(l, []string{filepath.Join(sandbox.DownloadsDir(w.root), "FabrikamPlayerSetup-2.0.1.exe")})
	res := w.find(t)
	for _, p := range res.Packages {
		if p.Name == "FabrikamPlayerSetup-2.0.1.exe" {
			t.Error("whitelisted package offered")
		}
	}
	for _, f := range ResolveFolders(w.env, []string{l.Windows, filepath.Join(l.LocalAppData, "Package Cache"),
		l.LocalAppData, `relative\path`, filepath.Join(w.root, "missing")}) {
		if f.Status == "ok" {
			t.Errorf("folder %s accepted", f.Path)
		}
	}
}
