package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
	"github.com/Harshul1484/out-of-windows/internal/sandbox"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

type installerDoc struct {
	Schema  string `json:"schema"`
	DryRun  bool   `json:"dry_run"`
	Folders []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	} `json:"folders"`
	Installers []struct {
		Path      string   `json:"path"`
		Name      string   `json:"name"`
		Type      string   `json:"type"`
		Product   string   `json:"product"`
		Evidence  []string `json:"evidence"`
		Status    string   `json:"status"`
		Selected  bool     `json:"selected"`
		Reasons   []string `json:"reasons"`
		Installed struct {
			Status string `json:"status"`
			Match  string `json:"match"`
		} `json:"installed"`
	} `json:"installers"`
	Warnings []string `json:"warnings"`
	Summary  struct {
		Installers    int   `json:"installers"`
		Selected      int   `json:"selected"`
		SelectedBytes int64 `json:"selected_bytes"`
		Executed      bool  `json:"executed"`
		Recycled      int   `json:"recycled"`
		RecycledBytes int64 `json:"recycled_bytes"`
		Errors        int   `json:"errors"`
	} `json:"summary"`
	Recycled *struct {
		Recycled []struct {
			Path string `json:"path"`
		} `json:"recycled"`
	} `json:"recycled"`
}

func TestInstallerDryRunJSONChangesNothing(t *testing.T) {
	e := newEnv(t)
	before := testutil.SnapshotDir(t, e.system())
	out, _, code := e.run("installer", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[installerDoc](t, out)
	if d.Schema != "oow.installer/v1" || !d.DryRun || d.Summary.Executed || len(d.Folders) != 3 || d.Warnings == nil {
		t.Errorf("header = %+v", d)
	}
	if d.Summary.Installers != 8 || d.Summary.Selected != 3 {
		t.Errorf("summary = %+v", d.Summary)
	}
	selected := map[string]bool{}
	for _, p := range d.Installers {
		testutil.AssertInSandbox(t, p.Path)
		if len(p.Evidence) == 0 || p.Reasons == nil {
			t.Errorf("%s: evidence %v reasons %v", p.Name, p.Evidence, p.Reasons)
		}
		if p.Selected {
			selected[p.Name] = true
		}
		switch strings.ToLower(p.Name) {
		case "tailspin-terminal.exe", "notes.msi", "old-report.msi", "vacation-photos.zip", "setup.pdf":
			t.Errorf("%s offered as an installer", p.Name)
		}
	}
	for _, n := range []string{"FabrikamPlayerSetup-2.0.1.exe", "NorthwindSync-3.1.msi", "ContosoStudio_4.2.0.0_x64.msix"} {
		if !selected[n] {
			t.Errorf("%s not selected", n)
		}
	}
	if len(testutil.SnapshotDir(t, e.system())) != len(before) {
		t.Fatal("dry run changed the filesystem")
	}
	text, _, _ := e.run("installer", "--dry-run")
	for _, want := range []string{"Installer packages", "Fabrikam Player 2.0.1", "installed", "no installed app matches it", "Dry run"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output lacks %q:\n%s", want, text)
		}
	}
}

func TestInstallerRefusesWithoutConfirmation(t *testing.T) {
	e := newEnv(t)
	before := testutil.SnapshotDir(t, e.system())
	if _, _, code := e.run("installer"); code != cli.ExitNeedsConfirm {
		t.Fatalf("code = %d, want %d", code, cli.ExitNeedsConfirm)
	}
	out, _, code := e.run("installer", "--json")
	if code != cli.ExitNeedsConfirm || !strings.Contains(out, `"exit_code": 4`) {
		t.Fatalf("json: code=%d out=%s", code, out)
	}
	if len(testutil.SnapshotDir(t, e.system())) != len(before) {
		t.Fatal("files moved without confirmation")
	}
}

func TestInstallerYesRecyclesSelected(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("installer", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	d := decode[installerDoc](t, out)
	if !d.Summary.Executed || d.Summary.Recycled != 3 || d.Summary.RecycledBytes != d.Summary.SelectedBytes ||
		d.Summary.Errors != 0 || d.Recycled == nil || len(d.Recycled.Recycled) != 3 {
		t.Fatalf("summary = %+v", d.Summary)
	}
	dl := sandbox.DownloadsDir(e.root)
	for _, gone := range []string{"FabrikamPlayerSetup-2.0.1.exe", "NorthwindSync-3.1.msi"} {
		if _, err := os.Lstat(filepath.Join(dl, gone)); err == nil {
			t.Errorf("%s not moved", gone)
		}
	}
	for _, kept := range []string{"WingtipToys-9.0-setup.exe", "ProsewareEditor-5.0-x64.msi", "tailspin-terminal.exe",
		"setup.pdf", "notes.msi", "vacation-photos.zip", "AdventureWorks-1.2.zip"} {
		if _, err := os.Lstat(filepath.Join(dl, kept)); err != nil {
			t.Errorf("%s was moved", kept)
		}
	}
	bin, _ := os.ReadDir(filepath.Join(sandbox.RecycleBinDir(e.root), "S-1-5-21-sandbox"))
	if len(bin) != 3+3 {
		t.Errorf("Recycle Bin entries = %d, want 6", len(bin))
	}
	hist, _, _ := e.run("history", "--json")
	if !strings.Contains(hist, `"command": "installer"`) {
		t.Errorf("history lacks the installer run:\n%s", hist)
	}
}

func TestInstallerFolderArguments(t *testing.T) {
	e := newEnv(t)
	l := sandbox.Locations(e.root)
	for _, bad := range []string{l.Windows, filepath.Join(l.LocalAppData, "Google"), l.ProgramData} {
		if _, _, code := e.run("installer", bad, "--dry-run"); code != cli.ExitUsage {
			t.Errorf("installer %s: code %d, want %d", bad, code, cli.ExitUsage)
		}
	}
	out, _, code := e.run("installer", sandbox.DesktopDir(e.root), "--dry-run", "--json")
	d := decode[installerDoc](t, out)
	if code != 0 || len(d.Folders) != 1 || len(d.Installers) != 1 || d.Installers[0].Type != "msix" ||
		d.Installers[0].Installed.Status != "installed" {
		t.Errorf("code %d, doc %+v", code, d)
	}
}
