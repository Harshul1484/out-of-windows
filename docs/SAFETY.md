# Safety model

`oow` deletes files. This document describes every layer that stands between a cleanup
rule and a deletion, and the tests that hold each layer in place. Changes to anything in
`internal/safety`, `internal/filesystem` or a cleanup rule must keep this document true.

## Principles

1. **Discover → Explain → Confirm → Act → Verify.** Nothing is deleted without being
   shown first, explained, and confirmed (`--yes` is the explicit confirmation for scripts).
2. **If safety cannot be established, do not delete.** Unknown situations become
   `skipped` or `review required`, never best guesses.
3. **Trust is worth more than gigabytes.** Conservative defaults; honest numbers.

## Layers

```text
rule (what to look for)
  └─ root resolution        Known Folder APIs → must exist, not be a link, final path == path
      └─ guard.ValidateRoot  root may not be or contain anything protected
          └─ scan             walk without following links; age + pattern filters;
                              guard.Check per item (scope = validated root)
              └─ user review   report, checklist, confirmation
                  └─ RemoveVerified (per item, at deletion time)
                       open handle without following reparse points
                       → same object as scanned? (type, size, created, modified)
                       → not a link / cloud placeholder
                       → OS final path inside the deletion fence
                       → guard.Check(final path) again
                       → delete through the same handle
```

### 1. Rules are narrow and reviewed

A rule (`internal/cleanup`) names a specific folder through a location token such as
`{LocalAppData}\D3DSCache`. `Rule.Validate` rejects bare locations (`{LocalAppData}`),
wildcards and `..` in roots. Only a root's *contents* are cleaned, never the root itself.
Every rule must state **What** it removes, **WhySafe** it is, and the **Impact** afterwards;
the UI shows all three.

Roots come from the Known Folder APIs (`SHGetKnownFolderPath`), never from environment
variables. In particular user temp is `{LocalAppData}\Temp`, not `%TEMP%`: a test asserts no
rule uses `{Temp}`, because `%TEMP%` can be pointed anywhere.

### 2. Root resolution

Before a rule is scanned its root must exist, must not be a junction or symlink, and its
OS-resolved final path must equal its expected path. Otherwise the rule is marked
`review required` and nothing inside it is touched.

### 3. The guard (`internal/safety`)

All paths are normalized with Win32 semantics before comparison: `\\?\`, `\??\` and `\\.\`
prefixes are stripped, `/` becomes `\`, `.` and `..` are resolved, trailing dots and spaces
are trimmed from every component, and comparison is case-insensitive. Relative paths,
drive-relative paths (`C:foo`), alternate data streams, wildcards, volume GUID / device paths
and malformed UNC paths are rejected outright.

The guard knows four kinds of location:

| Kind | Examples | Rule-driven cleanup | User-selected deletion |
|---|---|---|---|
| never-remove | drive roots, `C:\Users`, profile, `AppData`, `Local`, `Roaming`, Temp roots, Start Menu, every user folder root | never, nor any ancestor | never, nor any ancestor |
| system tree | `Windows`, `Program Files (x86)`, `ProgramData`, `System Volume Information`, `$Recycle.Bin`, `pagefile.sys`, `Windows.old` … | only inside a reviewed exemption | never |
| user content | Desktop, Documents, Downloads, Pictures, Music, Videos, OneDrive, Public folders | never | allowed (e.g. analyzer → Recycle Bin) |
| protected | the user's whitelist, `oow`'s own config and data | never | never |
| sensitive | `.ssh`, `.aws`, `.kube`, DPAPI keys, Credential Manager, Vault, password managers, wallets, VPN profiles, AI-tool state (`.claude`, `.ollama`…), editor `User` state, Docker Desktop data, `.rustup` | never | never |

Independently of location, **sensitive file types** are never removed by automatic cleanup:
VM/WSL/Docker disks (`*.vhdx`, `*.vhd`, `*.vmdk`…), password databases (`*.kdbx`), Outlook
stores (`*.pst`, `*.ost`), private keys and certificates (`*.pfx`, `*.pem`, `*.key`, `id_rsa*`…),
wallets, and browser credential databases (`Login Data`, `Cookies`, `key4.db`…). A user may
still delete such a file explicitly.

These come from discovery **plus a hard-coded baseline** (`C:\Windows`, `C:\Program Files`,
… on the system drive, and system files on every fixed drive), so protection does not depend
on discovery succeeding.

- **Exemptions** are the only way a rule may work inside a system tree. They live in the
  guard, not in rules, and currently contain `%WINDIR%\Temp`,
  `%ProgramData%\Microsoft\Windows\WER\ReportArchive` and `ReportQueue`, and
  `%WINDIR%\Minidump`: all admin-only and all also cleaned by Windows Disk Cleanup.
- **Profile roots** (`{profile}` in a rule root) expand only to real, non-link directories
  containing the rule's marker file, so a browser's `User Data\<profile>\Cache` is reachable
  but a stray folder or a junction named like a profile is not.
- **Scope**: rule-driven cleanup *must* carry the validated root as scope; any path not
  strictly inside it is refused. A cleanup request without a scope is refused.
- **ValidateRoot** additionally refuses any root that *contains* a protected location, that
  *is* a protected location (except the Temp containers, whose contents are cleanable), or that
  lies in user content or the whitelist. A hijacked `%TEMP%` (e.g. pointed at `C:\` or
  Documents) therefore cannot become a cleanup root.

### 3b. Uninstall and leftovers

- **Uninstall never deletes.** `oow` runs the app's own uninstaller (Windows Installer
  `msiexec /x`, the registered uninstall command, `Remove-AppxPackage`, Scoop or Chocolatey)
  and then checks the registration is gone. A failed or cancelled uninstall stops there; an
  unreadable registry state counts as "still installed".
- **Leftovers need evidence.** A folder is a candidate only with evidence that an app which is
  gone owned it: the install folder it registered, an exact normalized name match (versions,
  architecture tags and legal suffixes removed; names shorter than four characters or generic
  words like "data" or "launcher" never count), the app's executable inside, or the
  publisher's folder. Evidence comes from the app just uninstalled, oow's history, registry
  entries whose uninstaller and program are gone, and Windows usage traces (MuiCache and the
  Program Compatibility Assistant) of executables that no longer exist. Usage-trace evidence is
  at most medium confidence and ignored when the folder changed in the last 7 days.
- **Claims keep folders.** Anything matching an installed app (name, install folder, program
  folder), a running process, a service or a startup entry is kept and listed as such.
- **The leftover purpose** of the guard allows only folders at most three levels inside
  Program Files, ProgramData, AppData (Roaming, Local, LocalLow) or `AppData\Local\Programs`,
  never Windows- or Microsoft-owned, shared (Common Files, WindowsApps, Package Cache,
  Packages, Temp) or package-manager folders, never user content, sensitive or protected
  locations, and never folders containing sensitive files.
- **Leftovers go to the Recycle Bin** after the same handle-based identity, link and final-path
  verification as deletion, through the Shell (`SHFileOperationW` with undo). Drives without a
  Recycle Bin are refused rather than deleted from. Only high-confidence leftovers are
  preselected, and `--yes` takes only those.

### 3c. Project artifacts (purge) and installer packages

**Purge discovery.** `oow purge` scans only the folders passed on the command line, else
`purge.paths` from the configuration, else the usual project folders in the profile that exist
(`source\repos`, `Projects`, `dev`, `code`, `src`, `workspace`, `repos`, `GitHub`,
`Documents\GitHub`). A scan root must be a real folder (not a link, final path equal to its
path) outside system trees, AppData, sensitive and protected locations, other users' profiles,
and must not be the system drive or contain the whole profile. The walk is bounded (8 levels),
never follows links, and never enters dot folders, `node_modules`, vendored code (`vendor`,
`third_party`, `external`, `deps`, `Pods`, ...), `Package Cache` or guard-protected locations.

**What counts as an artifact.** Only a folder with a known name *next to its project's marker
file*: `node_modules`, `.next`, `.nuxt`, `.svelte-kit`, `.turbo`, `.parcel-cache`, `.angular` and
`dist`/`build`/`out` (package.json); `target` (Cargo.toml or pom.xml); `build` and `.gradle`
(Gradle files); `bin`/`obj` (a .csproj/.fsproj/.vbproj); `.venv`/`venv` (pyvenv.cfg inside and a
Python marker); `__pycache__` (only compiled files, anywhere in a Python project),
`.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `.tox`; `.dart_tool` (pubspec.yaml); CMake build
trees (`build`, `build-*`, `cmake-build-*` with CMakeCache.txt inside). A folder that itself
holds project markers is a project, never an artifact, and a found artifact is never searched
for more projects. Go `vendor` and other directly consumed stores are never targets.

**Kept, whatever the user selects** (re-checked right before deletion):

- Git-tracked content: `git ls-files --cached` with literal, case-insensitive pathspecs anchored at
  the repository root, run with `core.fsmonitor=false`, hooks disabled and inherited `GIT_*`
  variables dropped (so repository configuration cannot run programs or redirect Git). If the
  project is inside a repository and Git is missing or fails, the folder is kept (fail closed).
- A nested repository (`.git` anywhere inside), links or junctions (pnpm and workspace links:
  links are never followed or deleted, so such a folder could only be removed partially),
  cloud-only placeholders, unreadable subfolders.
- Sensitive files (`safety.IsSensitiveName`: keys, certificates, wallets, VM disks): anywhere in
  build output; in package stores (`node_modules`, `.venv`, `venv`, `.tox`) only directly in the
  store's top folder, because deeper such names are package content (certifi's `cacert.pem`,
  test fixtures).

**Listed but not selected**: anything changed (created or written) in the last 7 days; `dist`,
`build` and `out` of JavaScript projects unless Git ignores them; `bin`/`obj` without MSBuild's
layout. `--yes` takes only the default selection.

**Removal method: preselected artifacts are deleted, artifacts added from review are
recycled.** Artifacts with full evidence (`ready`, preselected; the only ones `--yes` takes) are
deleted permanently through the verified sink. Artifacts the user adds from review (`review`:
dist/build/out without Git-ignore evidence, recent activity, unusual layout) rest on weaker
evidence, so they are moved to the Recycle Bin as a whole folder with
`filesystem.RecycleVerified` (handle-verified identity, link and fence checks, final path
approved by `PurposePurge` with the artifact as scope) after the same re-checks; on a drive
without a Recycle Bin such a folder is kept with that reason, never deleted.

Why preselected artifacts are not recycled: the folders are rebuilt by the project's
own commands (the same recovery contract as caches); the Shell's Recycle Bin path is
impractically slow for folders of 100,000+ small files and such folders often exceed the bin's
size quota (the Shell then offers to delete permanently anyway); a recycled `node_modules`
would not free any space; and the Recycle Bin path verifies only the top folder before handing
the whole tree to the Shell, whereas the verified sink checks every file.

For each chosen artifact `purge.Remove` first re-checks: same folder (not a link, same creation
time), guard approval, a fresh walk that finds no `.git`, link, cloud-only or sensitive file and
nothing created or modified since the scan, and Git again. A preselected artifact is then
deleted file by file with `filesystem.RemoveVerified` (identity, link, cloud and fence checks
through the deleting handle) and folders deepest first; an artifact added from review is moved
whole with `filesystem.RecycleVerified`. Every final path is checked with **`PurposePurge`**: the scope must be the artifact folder, whose name must be a
known artifact name and whose parent must be a project folder (not a drive root, the profile or
a user-content root), outside system trees, AppData and tool folders in the profile (`.vscode`,
`.cargo`, `.nuget`, other dot folders, `scoop`, `go\pkg`, Conda). The path must lie inside that
folder, never inside a `.git` folder, and must not be a sensitive file type (except deep in a
package store, as above). User content is allowed (projects live in `Documents\GitHub`), the
whitelist and protected locations are not.

**Installers** (`oow installer`) are identified by content; a name or extension only decides
which files are opened. MSI/MSP: OLE compound file whose root storage carries the Windows
Installer class ID (product details read with the Windows Installer database API, read-only).
MSIX/APPX and bundles: ZIP with `AppxManifest.xml` or `AppxBundleManifest.xml`. Setup programs:
a PE executable (not a DLL) with Inno Setup, NSIS, WiX Burn, InstallShield or Advanced Installer
data, or a version resource (read with `GetFileVersionInfo`) that says setup or installer. ZIP
archives only with such an installer at their root; ISO images only with `setup.exe`,
`autorun.inf` or an MSI at their root (other images are review-only). Cloud placeholders are
never opened. Searching covers Downloads, Desktop and Documents (Known Folders) or folders the
user passes, 3 levels deep, skipping links, repositories, dot folders, `node_modules` and
`Package Cache`; system trees and AppData are refused because installer caches there are needed
for repair and uninstall. Installed state is an exact match (MSI ProductCode, MSIX identity, or
normalized product name with or without its publisher prefix; generic names never match). Only
installed packages older than 7 days are preselected. Confirmed packages go to the Recycle Bin
through the verified recycle sink with `PurposeUserSelected`.

### 3d. Updating and removing oow itself

- **The tool's own folders** stay protected for every purpose except `PurposeSelfRemove`,
  used only by `oow remove`: it allows exactly one of the config or data folders (never a
  parent or a child), and still refuses it when it is critical, whitelisted or contains a
  whitelisted path, sensitive, in a system tree or in user content. `oow remove` also keeps
  folders chosen through `OOW_CONFIG_DIR` / `OOW_DATA_DIR` or not at the Known Folder location.
  The folders go to the Recycle Bin through `RecycleVerified`, which re-checks the guard on
  the OS-resolved final path.
- **The executable** is removed only when it is exactly `%LOCALAPPDATA%\Programs\oow\oow.exe`
  (the installer's folder, from the Known Folder API). A running program cannot delete its own
  file, so `oow remove` uses no self-deletion technique: it prints the command that removes
  the file and the then-empty folder after exit.
- **Registry**: `oow update` writes none. `oow remove` writes only the user `Path`
  (`HKCU\Environment`), only to drop entries equal to the installer's folder, through the same
  `envpath.Store` compare-and-swap writer as `oow repair`: every other entry is kept verbatim,
  the value type is preserved, the write is refused if the value changed after it was read,
  and the value is read back afterwards.
- **Update** replaces the executable only with a download whose SHA-256 matches the release's
  `SHA256SUMS` (fail closed). The old file is renamed aside, never overwritten, and removed on
  the next start through `RemoveVerified` with an exact final-path check. Package-managed
  installs (winget, Scoop, Chocolatey) are never updated or removed by `oow`.

### 4. Scanning

The walker never descends into reparse points (junctions, symlinks, mount points); they are
reported as `skipped: link or junction (not followed)`. Cloud placeholders (OneDrive
"online-only" files) are skipped so cleanup never triggers downloads or deletes cloud data.
Unreadable folders are reported and the scan continues.

Age filters use **both** creation and last-write time: an archive extracted a minute ago has
old modification times but a fresh creation time, and must be treated as new.

### 5. Verified deletion (`internal/filesystem.RemoveVerified`)

Deletion happens through one handle opened with `FILE_FLAG_OPEN_REPARSE_POINT`:

1. **Identity**: type, size, creation and last-write time must equal the scan's fingerprint
   (directories: type and creation time). Otherwise `skipped: changed since it was scanned`.
2. **Links**: a reparse point is never deleted (`skipped`), so a link's target is never touched.
3. **Final path**: `GetFinalPathNameByHandle` returns where the handle *really* points. That
   path must be inside the deletion fence and must pass `guard.Check` with the rule's scope.
   This defeats a parent folder being swapped for a junction after the scan, even when the
   attacker reproduces names, sizes and timestamps exactly (tested).
4. **Delete** via `SetFileInformationByHandle(FileDispositionInfoEx)` with POSIX semantics and
   ignore-read-only, falling back to classic disposition on older Windows or FAT volumes.
   Files open without `FILE_SHARE_DELETE` fail with a sharing violation → `skipped: in use`.

Directories are removed only when empty, deepest first, and only if they are themselves old.

### 6. The deletion fence

`filesystem.SetFence(root)` confines every deletion in the process to one tree, checked on the
OS-resolved final path below all policy code. Once set it can only be narrowed. Tests set it
for every test binary (`testutil.Main`), and sandbox mode sets it to the sandbox, so neither a
test nor a sandbox run can delete anything outside its folder, whatever the code above does.

### 7. Elevation

`oow` never requires administrator rights as a whole. Targets that need them
(`RequiresAdmin`) are skipped with an explanation when not elevated.

### 8. Confirmation, history and honesty

Interactive runs show the report, a checklist (with What/Why/After for the focused target)
and a `[y/N]` confirmation that defaults to No. Non-interactive runs refuse to delete without
`--yes` (exit code 4). `OOW_DRY_RUN=1` turns every destructive command into a preview,
whatever flags are passed. Cache and temp cleanup and preselected project artifacts are deleted
permanently because the data is regenerated (see §3c for why); user files (analyzer,
installers, leftovers) and project artifacts the user adds from review go to the Recycle Bin,
and permanent deletion of user files will require typing a confirmation word.

Every real run is appended to `history.jsonl` with per-target counts and skip reasons. Reports
show bytes removed and the *measured* change in free space.

## Testing the safety model

| Concern | Tests |
|---|---|
| Dangerous paths never deletable in any spelling, under any scope | `safety/guard_test.go` `TestDangerousPathsNeverDeletable` |
| Dangerous paths never valid roots; hijacked `%TEMP%` | `TestDangerousPathsNeverValidRoots`, `TestRedirectedTempCannotWidenRoots` |
| Protection without discovery | `TestBaselineProtectionWithoutDiscovery` |
| Normalization edge cases | `TestNormalize`, `FuzzNormalizeIdempotent` |
| Invariant: allowed ⇒ strictly inside scope, never in System32 | `FuzzGuardScope` (also run in CI) |
| Junction swap with identical fingerprints | `filesystem` `TestRemoveVerifiedJunctionSwapAttack`, `cleanup` `TestJunctionSwapAfterScan` |
| Links never followed / never deleted | `TestWalkDoesNotFollowJunctions`, `TestRemoveVerifiedRefusesJunction` |
| Modified / replaced / locked / read-only / permission-denied files | `filesystem` and `cleanup` tests |
| Dry run changes nothing | `cleanup` `TestDryRunChangesNothing`, `cli` `TestCleanDryRunJSONChangesNothing` |
| Fence cannot be widened and blocks deletion | `TestFenceCannotBeWidened`, `TestFenceBlocksDeletionOutside` |
| Real system paths and 8.3 names protected | `safety/realsystem_test.go` (CI) |
| Purge purpose: scope, traversal, `.git`, sensitive files, AppData and tool folders | `safety` `TestPurgePurpose`, `TestValidatePurgeArtifact`, `FuzzPurgeScope` |
| Purge discovery, keep rules, Git fail-closed, junction swaps, changes after scan, review artifacts recycled (kept without a Recycle Bin) | `purge/purge_test.go` |
| Installers identified by content, exact installed matching, Recycle Bin | `installer/installer_test.go` |
| Real cleanup with canary files | `scripts/ci/e2e-real.ps1` (CI only) |
