package purge

import (
	"path/filepath"
	"strings"
)

// Kind is one type of rebuildable project artifact.
type Kind struct {
	// ID is stable and appears in JSON output.
	ID string
	// Names are folder name patterns (lower case).
	Names []string
	// Label says what the folder holds; Ecosystem names the toolchain.
	Label     string
	Ecosystem string
	// Markers: the parent folder must directly contain one of these files.
	Markers []string
	// Inside: when set, the folder must directly contain one of these files.
	Inside []string
	// Ambiguous kinds have names that are also used for hand-made folders
	// (dist, build, out). They are preselected only when Git ignores them.
	Ambiguous bool
	// Anywhere kinds (__pycache__) occur in any folder of a project, not
	// only next to its marker file.
	Anywhere bool
	// Rebuild tells the user how the folder comes back.
	Rebuild string
	// shape, when set, checks the folder's direct children (lower-case
	// names, folders marked with a trailing backslash). A folder that does
	// not have the expected shape is offered for review only.
	shape func(top []string) (ok bool, why string)
	// only, when set, lists the only file patterns the folder may contain;
	// anything else keeps it.
	only []string
}

// Marker files per ecosystem.
var (
	nodeMarkers   = []string{"package.json"}
	gradleMarkers = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}
	dotnetMarkers = []string{"*.csproj", "*.fsproj", "*.vbproj"}
	pythonMarkers = []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "pipfile",
		"tox.ini", "pytest.ini", "mypy.ini", "ruff.toml", ".ruff.toml", "conftest.py", "manage.py"}
)

// projectMarkers make a folder a project. A folder that directly contains
// one is never itself an artifact, whatever its name (for example a
// workspace package called "build", or dist\package.json).
var projectMarkers = concat(nodeMarkers, gradleMarkers, dotnetMarkers, pythonMarkers, []string{
	"cargo.toml", "pom.xml", "pubspec.yaml", "cmakelists.txt", "go.mod", "composer.json",
	"gemfile", "mix.exs", "*.sln",
})

// Kinds are checked in order; the first match wins (Gradle's build before
// CMake's before a JavaScript build folder).
var Kinds = []*Kind{
	{ID: "node_modules", Names: []string{"node_modules"}, Label: "installed npm packages", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "npm install (or yarn / pnpm install)"},
	{ID: "next", Names: []string{".next"}, Label: "Next.js build output and cache", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "next build or next dev"},
	{ID: "nuxt", Names: []string{".nuxt"}, Label: "Nuxt build output", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "nuxt build or nuxt dev"},
	{ID: "svelte-kit", Names: []string{".svelte-kit"}, Label: "SvelteKit generated files", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "the next vite dev or vite build"},
	{ID: "turbo", Names: []string{".turbo"}, Label: "Turborepo cache", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "the next turbo run"},
	{ID: "parcel-cache", Names: []string{".parcel-cache"}, Label: "Parcel cache", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "the next Parcel build"},
	{ID: "angular", Names: []string{".angular"}, Label: "Angular CLI cache", Ecosystem: "Node.js",
		Markers: nodeMarkers, Rebuild: "the next ng build or ng serve"},
	{ID: "cargo-target", Names: []string{"target"}, Label: "Cargo build output", Ecosystem: "Rust",
		Markers: []string{"cargo.toml"}, Rebuild: "cargo build"},
	{ID: "maven-target", Names: []string{"target"}, Label: "Maven build output", Ecosystem: "Java",
		Markers: []string{"pom.xml"}, Rebuild: "mvn package"},
	{ID: "gradle-build", Names: []string{"build"}, Label: "Gradle build output", Ecosystem: "Java/Kotlin",
		Markers: gradleMarkers, Rebuild: "gradle build"},
	{ID: "gradle-cache", Names: []string{".gradle"}, Label: "Gradle project cache", Ecosystem: "Java/Kotlin",
		Markers: gradleMarkers, Rebuild: "the next Gradle build"},
	{ID: "cmake-build", Names: []string{"build", "build-*", "cmake-build-*"}, Label: "CMake build tree", Ecosystem: "C/C++",
		Markers: []string{"cmakelists.txt"}, Inside: []string{"CMakeCache.txt"}, Rebuild: "configure and build with CMake again"},
	{ID: "js-output", Names: []string{"dist", "build", "out"}, Label: "JavaScript build output", Ecosystem: "Node.js",
		Markers: nodeMarkers, Ambiguous: true, Rebuild: "your build script (for example npm run build)"},
	{ID: "dotnet-bin", Names: []string{"bin"}, Label: ".NET build output", Ecosystem: ".NET",
		Markers: dotnetMarkers, Rebuild: "dotnet build", shape: msbuildOutput},
	{ID: "dotnet-obj", Names: []string{"obj"}, Label: ".NET intermediate files", Ecosystem: ".NET",
		Markers: dotnetMarkers, Rebuild: "dotnet build", shape: msbuildIntermediate},
	{ID: "pycache", Names: []string{"__pycache__"}, Label: "compiled Python files", Ecosystem: "Python",
		Markers: pythonMarkers, Anywhere: true, Rebuild: "Python recompiles them on the next run",
		only: []string{"*.pyc", "*.pyo"}},
	{ID: "pytest-cache", Names: []string{".pytest_cache"}, Label: "pytest cache", Ecosystem: "Python",
		Markers: pythonMarkers, Rebuild: "the next pytest run"},
	{ID: "mypy-cache", Names: []string{".mypy_cache"}, Label: "mypy cache", Ecosystem: "Python",
		Markers: pythonMarkers, Rebuild: "the next mypy run"},
	{ID: "ruff-cache", Names: []string{".ruff_cache"}, Label: "Ruff cache", Ecosystem: "Python",
		Markers: pythonMarkers, Rebuild: "the next ruff run"},
	{ID: "tox", Names: []string{".tox"}, Label: "tox environments", Ecosystem: "Python",
		Markers: []string{"tox.ini", "pyproject.toml", "setup.cfg"}, Rebuild: "the next tox run (downloads packages again)"},
	{ID: "venv", Names: []string{".venv", "venv"}, Label: "Python virtual environment", Ecosystem: "Python",
		Markers: pythonMarkers, Inside: []string{"pyvenv.cfg"},
		Rebuild: "python -m venv plus reinstalling packages (pip install -r requirements.txt, uv sync or poetry install)"},
	{ID: "dart-tool", Names: []string{".dart_tool"}, Label: "Dart and Flutter tool state", Ecosystem: "Dart",
		Markers: []string{"pubspec.yaml"}, Rebuild: "dart pub get or flutter pub get"},
}

// msbuildOutput accepts bin folders laid out the way MSBuild writes them:
// bin\Debug, bin\Release or a platform folder.
func msbuildOutput(top []string) (bool, string) {
	for _, n := range top {
		switch n {
		case `debug\`, `release\`, `x64\`, `x86\`, `arm64\`, `arm\`, `anycpu\`:
			return true, ""
		}
	}
	return false, "has no Debug or Release output folder, so it may not be MSBuild output"
}

// msbuildIntermediate accepts obj folders with NuGet restore files or
// configuration folders.
func msbuildIntermediate(top []string) (bool, string) {
	for _, n := range top {
		if n == "project.assets.json" || strings.Contains(n, ".nuget.") || n == `debug\` || n == `release\` {
			return true, ""
		}
	}
	return false, "has no NuGet restore files or Debug/Release folders, so it may not be MSBuild output"
}

// KindByID returns the kind with the given ID.
func KindByID(id string) *Kind {
	for _, k := range Kinds {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func (k *Kind) matchesName(name string) bool { return matchAny(k.Names, name) }

// hasMarker reports whether files (lower-case names directly inside the
// parent) include one of the kind's markers.
func (k *Kind) hasMarker(files map[string]bool) bool {
	return anyMatch(k.Markers, files)
}

func anyMatch(patterns []string, files map[string]bool) bool {
	for _, p := range patterns {
		if !strings.ContainsAny(p, "*?[") {
			if files[p] {
				return true
			}
			continue
		}
		for f := range files {
			if ok, _ := filepath.Match(p, f); ok {
				return true
			}
		}
	}
	return false
}

func matchAny(patterns []string, name string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, lower); ok {
			return true
		}
	}
	return false
}

// markersIn returns the project marker files present (lower case, sorted
// as found).
func markersIn(files map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for f := range files {
		if matchAny(projectMarkers, f) && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
