package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// ProjectsDir is the simulated ~\source\repos, a default purge root.
func ProjectsDir(root string) string {
	return filepath.Join(Locations(root).UserProfile, "source", "repos")
}

// DownloadsDir, DesktopDir and DocumentsDir are the simulated user folders
// where installer packages are looked for.
func DownloadsDir(root string) string { return Locations(root).UserContent[2] }
func DesktopDir(root string) string   { return Locations(root).UserContent[0] }
func DocumentsDir(root string) string { return Locations(root).UserContent[1] }

// NorthwindProductCode is the ProductCode of the simulated Northwind Sync MSI.
const NorthwindProductCode = "{6F1D5C3A-2B4E-4C8D-9A7F-1E2D3C4B5A69}"

// seedPhase7 creates developer projects with rebuildable artifacts next to
// what must survive a purge, and installer packages next to look-alikes that
// must never be offered.
func seedPhase7(root string, l safety.Locations) error {
	if err := seedProjects(root, l); err != nil {
		return err
	}
	return seedInstallers(root)
}

func seedProjects(root string, l safety.Locations) error {
	const day = 24 * time.Hour
	now := time.Now()
	repos := ProjectsDir(root)
	gh := filepath.Join(DocumentsDir(root), "GitHub")
	j := filepath.Join
	type f struct {
		path string
		size int
		age  time.Duration
	}
	files := []f{
		// webapp (Git): node_modules and .next are ignored and old; dist is
		// committed, so it must be kept.
		{j(repos, "webapp", "package.json"), 400, 60 * day},
		{j(repos, "webapp", ".gitignore"), 30, 60 * day},
		{j(repos, "webapp", "src", "index.js"), 2000, 50 * day},
		{j(repos, "webapp", "node_modules", "react", "package.json"), 900, 40 * day},
		{j(repos, "webapp", "node_modules", "react", "index.js"), 300000, 40 * day},
		{j(repos, "webapp", "node_modules", "history", "index.js"), 20000, 40 * day},
		{j(repos, "webapp", "node_modules", "https-proxy-agent", "test", "key.pem"), 1700, 40 * day},
		{j(repos, "webapp", ".next", "cache", "webpack", "client.pack"), 800000, 30 * day},
		{j(repos, "webapp", "dist", "bundle.js"), 50000, 30 * day},

		// fresh-app: installed yesterday, so not selected by default.
		{j(repos, "fresh-app", "package.json"), 300, 2 * day},
		{j(repos, "fresh-app", "node_modules", "lodash", "lodash.js"), 540000, 1 * day},

		// rustapp: Cargo build output.
		{j(repos, "rustapp", "Cargo.toml"), 200, 90 * day},
		{j(repos, "rustapp", "src", "main.rs"), 500, 90 * day},
		{j(repos, "rustapp", "target", "CACHEDIR.TAG"), 177, 30 * day},
		{j(repos, "rustapp", "target", "debug", "rustapp.exe"), 2400000, 30 * day},
		{j(repos, "rustapp", "target", "debug", "deps", "librustapp.rlib"), 900000, 30 * day},

		// Api (.NET): bin and obj, next to a tools\bin folder of scripts.
		{j(repos, "Api", "Api.csproj"), 900, 80 * day},
		{j(repos, "Api", "Program.cs"), 1500, 80 * day},
		{j(repos, "Api", "bin", "Debug", "net8.0", "Api.dll"), 120000, 20 * day},
		{j(repos, "Api", "obj", "project.assets.json"), 30000, 20 * day},
		{j(repos, "Api", "obj", "Debug", "net8.0", "Api.AssemblyInfo.cs"), 900, 20 * day},
		{j(repos, "Api", "tools", "bin", "deploy.ps1"), 700, 80 * day},

		// pyproj: a virtual environment (with certifi's CA bundle deep
		// inside), compiled files and a pytest cache; venv-notes is a
		// hand-made folder called venv without pyvenv.cfg.
		{j(repos, "pyproj", "pyproject.toml"), 600, 70 * day},
		{j(repos, "pyproj", "app", "main.py"), 900, 70 * day},
		{j(repos, "pyproj", ".venv", "pyvenv.cfg"), 120, 25 * day},
		{j(repos, "pyproj", ".venv", "Lib", "site-packages", "certifi", "cacert.pem"), 290000, 25 * day},
		{j(repos, "pyproj", ".venv", "Lib", "site-packages", "requests", "api.py"), 6000, 25 * day},
		{j(repos, "pyproj", "app", "__pycache__", "main.cpython-312.pyc"), 1400, 25 * day},
		{j(repos, "pyproj", ".pytest_cache", "CACHEDIR.TAG"), 191, 25 * day},
		{j(repos, "pyproj", ".pytest_cache", "v", "cache", "nodeids"), 300, 25 * day},
		{j(repos, "pyproj", "tools", "venv", "notes.txt"), 200, 70 * day},

		// deploy-site: build output holding a deployment key is kept.
		{j(repos, "deploy-site", "package.json"), 300, 60 * day},
		{j(repos, "deploy-site", "build", "index.html"), 4000, 30 * day},
		{j(repos, "deploy-site", "build", "deploy_key.pem"), 1700, 30 * day},

		// handmade: dist outside Git may be hand-made, so it is for review.
		{j(repos, "handmade", "package.json"), 300, 60 * day},
		{j(repos, "handmade", "dist", "index.html"), 7000, 30 * day},

		// monorepo (Git): node_modules links the workspace package, so it is
		// kept; the package's own node_modules is removable.
		{j(repos, "monorepo", "package.json"), 500, 60 * day},
		{j(repos, "monorepo", ".gitignore"), 30, 60 * day},
		{j(repos, "monorepo", "node_modules", "typescript", "lib", "tsc.js"), 900000, 40 * day},
		{j(repos, "monorepo", "packages", "ui", "package.json"), 300, 60 * day},
		{j(repos, "monorepo", "packages", "ui", "src", "button.js"), 1200, 60 * day},
		{j(repos, "monorepo", "packages", "ui", "node_modules", "clsx", "clsx.js"), 4000, 40 * day},

		// nested-repo: a Git dependency checked out inside node_modules.
		{j(repos, "nested-repo", "package.json"), 300, 60 * day},
		{j(repos, "nested-repo", "node_modules", "private-dep", ".git", "HEAD"), 23, 40 * day},
		{j(repos, "nested-repo", "node_modules", "private-dep", "index.js"), 3000, 40 * day},

		// gosvc: Go keeps vendored code in vendor, which is never searched.
		{j(repos, "gosvc", "go.mod"), 100, 60 * day},
		{j(repos, "gosvc", "vendor", "example.com", "widget", "package.json"), 300, 60 * day},
		{j(repos, "gosvc", "vendor", "example.com", "widget", "node_modules", "x", "x.js"), 3000, 60 * day},

		// cpp-engine: a CMake build tree; assets\build is not one.
		{j(repos, "cpp-engine", "CMakeLists.txt"), 800, 60 * day},
		{j(repos, "cpp-engine", "build", "CMakeCache.txt"), 30000, 30 * day},
		{j(repos, "cpp-engine", "build", "engine.lib"), 700000, 30 * day},
		{j(repos, "cpp-engine", "assets", "build", "logo.png"), 9000, 60 * day},

		// android-app: Gradle output and project cache.
		{j(repos, "android-app", "settings.gradle"), 100, 60 * day},
		{j(repos, "android-app", "app", "build.gradle"), 1500, 60 * day},
		{j(repos, "android-app", "app", "build", "outputs", "apk", "debug", "app-debug.apk"), 5000000, 30 * day},
		{j(repos, "android-app", ".gradle", "8.5", "checksums", "checksums.lock"), 17, 30 * day},

		// tools: "out" holds its own package.json, so it is a project.
		{j(repos, "tools", "package.json"), 300, 60 * day},
		{j(repos, "tools", "out", "package.json"), 300, 60 * day},
		{j(repos, "tools", "out", "cli.js"), 3000, 60 * day},

		// A Flutter game in Documents\GitHub.
		{j(gh, "game", "pubspec.yaml"), 400, 60 * day},
		{j(gh, "game", "lib", "main.dart"), 3000, 60 * day},
		{j(gh, "game", ".dart_tool", "package_config.json"), 12000, 30 * day},

		// Tool state in the profile that looks like projects: never purged.
		{j(l.UserProfile, ".vscode", "extensions", "ms-python.python-2025.1.0", "package.json"), 9000, 60 * day},
		{j(l.UserProfile, ".vscode", "extensions", "ms-python.python-2025.1.0", "node_modules", "x", "x.js"), 3000, 60 * day},
		{j(l.RoamingAppData, "npm", "node_modules", "typescript", "package.json"), 3000, 60 * day},
	}
	for _, x := range files {
		if err := WriteFile(x.path, x.size, now.Add(-x.age)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(j(repos, "webapp", ".gitignore"), []byte("node_modules/\n.next/\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(j(repos, "monorepo", ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		return err
	}
	for _, p := range []string{j(repos, "webapp", ".gitignore"), j(repos, "monorepo", ".gitignore")} {
		if err := filesystem.SetTimes(p, now.Add(-60*day), now.Add(-60*day)); err != nil {
			return err
		}
	}
	link := j(repos, "monorepo", "node_modules", "@acme", "ui")
	if _, err := os.Lstat(link); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		if err := MakeJunction(link, j(repos, "monorepo", "packages", "ui")); err != nil {
			return err
		}
	}
	if err := gitRepo(j(repos, "webapp"), "package.json", ".gitignore", "src", "dist"); err != nil {
		return err
	}
	return gitRepo(j(repos, "monorepo"), "package.json", ".gitignore", "packages")
}

// gitRepo makes dir a Git repository with the given paths committed, using
// an isolated configuration (no user or system config, no hooks, no signing).
// Without Git it creates an empty .git folder, so the project still counts
// as a repository and purge keeps its artifacts (it cannot check them).
func gitRepo(dir string, add ...string) error {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		return nil
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	// The user's global config and hooks are kept out through paths that never
	// exist: Git treats a missing config file as empty and finds no hooks
	// there. (NUL does not work: Git for Windows fails to access() it.)
	absent := filepath.Join(dir, ".git", "oow-absent")
	env = append(env, "GIT_CONFIG_GLOBAL="+absent, "GIT_CONFIG_NOSYSTEM=1")
	run := func(args ...string) error {
		full := append([]string{"-C", dir, "-c", "core.hooksPath=" + absent, "-c", "commit.gpgsign=false",
			"-c", "user.name=oow sandbox", "-c", "user.email=sandbox@example.invalid", "-c", "core.autocrlf=false",
			"-c", "init.defaultBranch=main"}, args...)
		cmd := exec.Command(git, full...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("init", "-q"); err != nil {
		return err
	}
	if err := run(append([]string{"add", "--"}, add...)...); err != nil {
		return err
	}
	return run("commit", "-q", "--no-verify", "-m", "sandbox fixture")
}

// WriteData creates a file with the given content and creation and
// modification times set to t.
func WriteData(path string, data []byte, t time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return filesystem.SetTimes(path, t, t)
}

func seedInstallers(root string) error {
	const day = 24 * time.Hour
	now := time.Now()
	dl, desk, docs := DownloadsDir(root), DesktopDir(root), DocumentsDir(root)
	j := filepath.Join
	pad := make([]byte, 64*1024)
	inno := BuildPE(PEOptions{
		Version: map[string]string{"ProductName": "Fabrikam Player", "CompanyName": "Fabrikam, Inc.",
			"FileDescription": "Fabrikam Player Setup", "ProductVersion": "2.0.1",
			"Comments": "This installation was built with Inno Setup."},
		FileVersion: [4]uint16{2, 0, 1, 0},
		Overlay:     append(append([]byte{}, InnoOverlay...), pad...),
	})
	nsis := BuildPE(PEOptions{
		Version: map[string]string{"ProductName": "Wingtip Toys", "CompanyName": "Wingtip Toys",
			"FileDescription": "Wingtip Toys Installer", "ProductVersion": "9.0"},
		FileVersion: [4]uint16{9, 0, 0, 0},
		Overlay:     append(append([]byte{}, NSISOverlay...), pad...),
	})
	portable := BuildPE(PEOptions{
		Version: map[string]string{"ProductName": "Tailspin Terminal", "CompanyName": "Tailspin Toys",
			"FileDescription": "SSH and serial terminal", "ProductVersion": "0.81"},
		FileVersion: [4]uint16{0, 81, 0, 0},
	})
	zipSetup := BuildPE(PEOptions{Overlay: append(append([]byte{}, NSISOverlay...), pad[:4096]...)})
	files := []struct {
		path string
		data []byte
		age  time.Duration
	}{
		{j(dl, "FabrikamPlayerSetup-2.0.1.exe"), inno, 40 * day},
		{j(dl, "WingtipToys-9.0-setup.exe"), nsis, 2 * day},
		{j(dl, "AdventureWorks-1.2.zip"), BuildZip(ZipEntry{"setup.exe", zipSetup}, ZipEntry{"readme.txt", []byte("Run setup.exe")}), 20 * day},
		{j(desk, "ContosoStudio_4.2.0.0_x64.msix"), BuildZip(
			ZipEntry{"AppxManifest.xml", AppxManifest("Contoso.Studio", "CN=Contoso Ltd.", "4.2.0.0", "Contoso Studio")},
			ZipEntry{"Assets/StoreLogo.png", make([]byte, 300)}), 30 * day},
		{j(docs, "Installers", "Woodgrove-Bank-Setup.iso"), BuildISO("WOODGROVE_BANK", map[string][]byte{
			"SETUP.EXE": zipSetup, "AUTORUN.INF": []byte("[autorun]\r\nopen=setup.exe\r\n")}), 60 * day},
		{j(docs, "Installers", "backup-2019.iso"), BuildISO("BACKUP_2019", map[string][]byte{
			"PHOTOS.TXT": []byte("photos")}), 400 * day},

		// Never installers, whatever their names say.
		{j(dl, "tailspin-terminal.exe"), portable, 90 * day},
		{j(dl, "setup.pdf"), []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"), 90 * day},
		{j(dl, "vacation-photos.zip"), BuildZip(ZipEntry{"beach.jpg", []byte("\xFF\xD8\xFF\xE0 jpeg")},
			ZipEntry{"sunset.jpg", []byte("\xFF\xD8\xFF\xE0 jpeg")}), 90 * day},
		{j(dl, "notes.msi"), []byte("This is not an installer, just a text file with a misleading name.\r\n"), 90 * day},
		{j(dl, "old-report.msi"), BuildCFB(CLSIDWordDocument), 90 * day},
		{j(docs, "Installers", "a", "b", "c", "too-deep-setup.exe"), inno, 90 * day},
	}
	for _, f := range files {
		if err := WriteData(f.path, f.data, now.Add(-f.age)); err != nil {
			return err
		}
	}
	for _, m := range []struct {
		path  string
		props map[string]string
		age   time.Duration
	}{
		{j(dl, "NorthwindSync-3.1.msi"), map[string]string{"ProductName": "Northwind Sync", "ProductVersion": "3.1",
			"Manufacturer": "Northwind Traders", "ProductCode": NorthwindProductCode}, 100 * day},
		{j(dl, "ProsewareEditor-5.0-x64.msi"), map[string]string{"ProductName": "Proseware Editor", "ProductVersion": "5.0.0",
			"Manufacturer": "Proseware", "ProductCode": "{0B4C1A2E-5D6F-4A3B-8C9D-0E1F2A3B4C5D}"}, 30 * day},
	} {
		if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
			return err
		}
		if err := CreateMSI(m.path, m.props); err != nil {
			return err
		}
		if err := filesystem.SetTimes(m.path, now.Add(-m.age), now.Add(-m.age)); err != nil {
			return err
		}
	}
	return nil
}
