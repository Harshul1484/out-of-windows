# Security Audit

Status: **v0.1.0-dev (Phases 1–8)**. This document records what the code does today. Planned
controls are listed separately under *Known limitations and future work*; nothing in the
other sections is aspirational. Update this file in the same change as any safety-relevant
code.

## Executive summary

`oow` is a local Windows maintenance tool. Its main risk is **unintended local data loss**
from destructive operations, not remote code execution: it has no network listeners, no
background service, no telemetry and no auto-update. The design favors bounded cleanup of
exact, reviewed targets over aggressive deletion. Every deletion is re-verified at the moment
it happens, through the same file handle that performs it, and is confined by a guard that
protects system, user, credential and tool-owned locations.

## Threat surface

Highest risk, in order:

1. **Direct file and directory deletion** (`filesystem.RemoveVerified`): the only deletion sink.
2. **Recursive cleanup of cache and temp roots** (`cleanup.Scan` / `Execute`), including the
   system-wide `%WINDIR%\Temp` when elevated.
3. **Path confusion**: alternate spellings, links, short names and races that could redirect a
   deletion outside its root.
4. **Elevated execution**: when run as administrator, the same mistakes reach system data.
5. **Configuration**: a lost or misread whitelist would silently widen deletion.
6. **Release integrity** of distributed binaries and of `oow update` (see *Distribution,
   self-update and self-removal*).

Lower risk: read-only commands (`history`, `config`, `version`, the home screen) and the
`analyze` flow (user-chosen items, Recycle Bin).

Out of scope for the threat model: an attacker who already runs code as the user or as
administrator, or who can replace `oow.exe`.

## Destructive operation boundaries

- **One sink.** Product code deletes only through `filesystem.RemoveVerified`, called by the
  cleanup executor. There is no use of `os.Remove`, `os.RemoveAll`, shell commands or
  PowerShell for deletion. (Test helpers remove their own sandbox only.)
- **Handle-verified deletion.** The target is opened with `FILE_FLAG_OPEN_REPARSE_POINT |
  FILE_FLAG_BACKUP_SEMANTICS` and `DELETE` access. Through that handle the code checks:
  - identity: type, size, creation time and last-write time equal the scan's fingerprint
    (directories: type and creation time);
  - not a reparse point; not a cloud placeholder (`OFFLINE`, `RECALL_ON_OPEN`,
    `RECALL_ON_DATA_ACCESS`);
  - the OS-resolved final path (`GetFinalPathNameByHandle`) is inside the deletion fence
    (when set) and passes `Guard.Check` with the rule's scope;
  - then deletes with `SetFileInformationByHandle(FileDispositionInfoEx)` (POSIX semantics,
    ignore read-only), falling back to classic disposition where unsupported.
  Files held open without `FILE_SHARE_DELETE` fail with a sharing violation and are skipped
  as "in use".
- **Directories** are removed only when empty, deepest first, and only if created before the
  rule's age cut-off.
- **Scoped cleanup.** `Guard.Check` refuses rule-driven cleanup without a validated scope, and
  any path not strictly inside it.
- **Age filter** uses both creation and last-write time, so freshly extracted archives with old
  timestamps are kept.
- **Confirmation.** Interactive runs require a `[y/N]` confirmation (default No) after a
  report and a checklist; non-interactive runs require `--yes` (exit 4 otherwise).
  `--dry-run` and `OOW_DRY_RUN=1` perform discovery only.
- **Cancellation.** Ctrl+C cancels scans before any deletion, and stops execution after the
  current item; a second Ctrl+C terminates.

### Protected locations (never deleted)

From Known Folder discovery **plus a hard-coded baseline** independent of discovery:

```text
X:\                                   every drive root
%WINDIR%  (and C:\Windows)            system tree
C:\Program Files, C:\Program Files (x86), Common Files
C:\ProgramData
C:\Windows.old, C:\PerfLogs, C:\Config.Msi, C:\MSOCache, C:\Boot, C:\EFI
X:\System Volume Information, X:\$Recycle.Bin, X:\Recovery, X:\$WinREAgent,
X:\$Windows.~BT, X:\$Windows.~WS, X:\$SysReset,
X:\pagefile.sys, X:\hiberfil.sys, X:\swapfile.sys, X:\DumpStack.log.tmp, X:\bootmgr, X:\BOOTNXT
C:\Users, C:\Users\Public, the user profile, AppData, Local, Roaming, LocalLow
Start Menu, Programs, Startup (user and common), SendTo, Templates,
%LOCALAPPDATA%\Programs, %LOCALAPPDATA%\Microsoft, %LOCALAPPDATA%\Packages,
%APPDATA%\Microsoft                    (never removed themselves, nor any ancestor)
```

System trees may be cleaned only inside a **reviewed exemption**. Exemptions live in the
guard, not in rules. Current list (all admin-only, all also cleaned by Windows Disk Cleanup):

```text
%WINDIR%\Temp
%ProgramData%\Microsoft\Windows\WER\ReportArchive
%ProgramData%\Microsoft\Windows\WER\ReportQueue
%WINDIR%\Minidump
```

### Application data boundaries

- **Profile roots** (`{profile}`) expand only to real directories containing the rule's
  marker file (e.g. Chromium's `Preferences`, Firefox's `cache2`), never through junctions or
  symlinks, and never to excluded names (JetBrains `Toolbox`). Only named cache folders inside
  a profile are roots.
- **Browsers**: only `Cache`, `Code Cache`, `GPUCache` and browser-wide shader caches. Cookies,
  `Login Data`, `Web Data`, `History`, `Bookmarks`, `Local State`, extensions, Service Worker
  storage, IndexedDB and Local Storage are never roots, and their file names are also on the
  sensitive list.
- **Running applications**: a rule whose `AppProcesses` is running is skipped entirely
  ("close it to clean this"); the process list comes from a Toolhelp snapshot.
- **Opt-in** (listed but unselected): Recycle Bin, thumbnail cache, JetBrains caches and
  indexes, Adobe media cache, Gradle caches, app crash dumps, kernel minidumps.
- **Never targeted** developer stores: `~\.m2\repository`, `~\.nuget\packages`, pnpm store,
  Cargo `registry\src` and `git`, `%LOCALAPPDATA%\Pub\Cache`, `ms-playwright`, Deno dir, model
  caches, `~\.rustup`, `~\.cargo\bin`.
- **Recycle Bin** is emptied with `SHEmptyRecycleBinW` (no confirmation UI, after `oow`'s own
  confirmation, which states the items cannot be restored); counts come from
  `SHQueryRecycleBinW` before and after. Sandbox mode uses a simulated bin folder.

### User content (never touched by automatic cleanup)

Desktop, Documents, Downloads, Pictures, Music, Videos, Favorites, Saved Games, Contacts,
Links, OneDrive (Known Folder and `OneDrive*` environment roots), and the Public equivalents.
A user may delete items there explicitly (analyzer and installers, through the Recycle Bin).

### Sensitive data (never deleted, for any purpose)

```text
~\.ssh  ~\.gnupg  ~\.aws  ~\.azure  ~\.kube  ~\.docker  ~\.config\gcloud
%APPDATA%\Microsoft\Protect (DPAPI master keys)  %APPDATA%\Microsoft\Credentials
%APPDATA%\Microsoft\Crypto  %APPDATA%\Microsoft\SystemCertificates
%LOCALAPPDATA%\Microsoft\Credentials  %LOCALAPPDATA%\Microsoft\Vault
%APPDATA%\gnupg  Bitwarden  %LOCALAPPDATA%\1Password  KeePass  KeePassXC
Electrum  Bitcoin  Ethereum  Exodus  Ledger Live  ~\OpenVPN
~\.claude  ~\.codex  ~\.cursor  ~\.ollama  ~\.lmstudio
%APPDATA%\Claude  %LOCALAPPDATA%\AnthropicClaude
%APPDATA%\Code\User  %APPDATA%\Cursor\User  (settings, workspace and global state)
%LOCALAPPDATA%\Docker  ~\.rustup  ~\.cargo\bin
```

### Sensitive file types (never removed by automatic cleanup, anywhere)

```text
*.vhd *.vhdx *.avhdx *.vmdk *.vdi *.qcow2        VM / WSL / Docker disks
*.kdbx *.kdb *.1pux *.opvault                      password databases
*.pst *.ost                                        Outlook data
*.pfx *.p12 *.pem *.key *.ppk *.gpg *.jks *.keystore id_rsa* id_dsa* id_ecdsa* id_ed25519* *.ovpn
*.wallet wallet.dat
Login Data, Cookies, Web Data, History, Bookmarks, Local State,
key4.db, logins.json, cert9.db, places.sqlite, cookies.sqlite, formhistory.sqlite
```

### Uninstall and leftovers

- `oow uninstall` never deletes program folders itself. It starts the registered mechanism
  (`msiexec /x {ProductCode}`, the registered `UninstallString` / `QuietUninstallString`,
  `Remove-AppxPackage`, `scoop uninstall`, `choco uninstall`) through `ShellExecuteExW`, so an
  uninstaller that needs administrator rights raises the normal UAC prompt; Chocolatey and global
  Scoop apps are started elevated explicitly. `MsiExec /I` registrations are converted to `/x`.
- Removal is verified by re-reading the registration (registry key, package list, package
  folder); errors other than "not found" count as still installed. Only a verified removal
  proceeds to leftovers.
- Entries whose registered uninstaller is missing are not offered for uninstall; they become
  leftover evidence only when the app's program is gone too.
- Leftover candidates come only from evidence (see `docs/SAFETY.md` §3b) and pass
  `Guard.Check` with `PurposeLeftover`: scope must be a leftover root (Program Files, Program
  Files (x86), ProgramData, Roaming, Local, LocalLow, `Local\Programs`), depth ≤ 3, first
  component not Windows/Microsoft-owned or shared (`Common Files`, `WindowsApps`,
  `Package Cache`, `Packages`, `Temp`, package managers), never user content, sensitive,
  protected or critical locations, never the Windows directory.
- Claims from installed apps, running processes (`QueryFullProcessImageName`), services
  (`ImagePath`) and Run/RunOnce entries keep folders; claims that would cover an entire root
  (from malformed registrations) are ignored rather than widening anything.
- Folders containing sensitive file types are never offered.
- Leftovers are moved with `SHFileOperationW` (`FO_DELETE` + `FOF_ALLOWUNDO`, plus
  `FOF_WANTNUKEWARNING` so the Shell warns instead of silently deleting), only on fixed drives,
  after `filesystem.Verify` re-checks identity, links, the fence and the guard through a handle.
  The short interval between verification and the Shell call is acceptable because the
  operation is recoverable from the Recycle Bin. A publisher folder left empty is removed with
  the verified deletion sink.
- Only high-confidence leftovers are preselected; `--yes` moves only those. Program Files and
  ProgramData candidates require elevation and are otherwise offered in an elevated window.

### Disk analyzer

- Scanning is read-only and never follows links or junctions; unreadable folders are counted.
- Deletion exists only in the interactive explorer, for items the user marked and confirmed
  on screen. Each item must pass `Guard.Check` with `PurposeUserSelected` (system trees,
  critical, protected and sensitive locations are refused; user content is allowed) and is
  then moved to the Recycle Bin through the verified recycle sink (`filesystem.Verify`, fixed
  drives only). There is no permanent delete in the analyzer. Every move is recorded in history.

### Monitoring

`status` and `processes` are read-only. Process data comes from one
`NtQuerySystemInformation(SystemProcessInformation)` call (no process handles are opened);
disk, network and GPU rates from PDH counters added by English name; GPU names from the
display-adapter registry key; NVIDIA details by running `nvidia-smi` (hidden window, 2 s
timeout) when it is installed. Nothing is stopped, changed or sent anywhere.

### Distribution, self-update and self-removal

- **Release pipeline** (`.github/workflows/release.yml`): only a pushed `v*` tag publishes.
  The build job has read-only permissions, runs vet and tests, builds `windows/amd64` and
  `windows/arm64` with `-trimpath` and stamped version, commit, date and repository, checks
  the stamp, and writes `SHA256SUMS`. A separate publish job (`contents: write`,
  `id-token: write`, `attestations: write`) re-verifies the checksums, attests every file in
  `SHA256SUMS`, and only then creates the release. Manual runs build and checksum but never
  attest or publish. Untrusted inputs (tag name, dispatch input) reach scripts through
  environment variables and are validated as semantic versions.
- **Installer** (`scripts/install.ps1`): no administrator rights; downloads into memory and
  checks size and SHA-256 against `SHA256SUMS` before writing anything; a missing checksum
  file, a missing entry or a mismatch stops it (no fallback). It writes only
  `%LOCALAPPDATA%\Programs\oow` (resolved through the Known Folder API) and the user `Path`
  value, appending its folder only if missing and keeping the value's type. The only file it
  replaces is an earlier `oow.exe` in that folder, which is first renamed to `oow.exe.old`
  (possible even while it runs; `oow` removes it on its next start); it deletes nothing else.
- **`oow update`** runs only when asked. It queries `releases/latest` over HTTPS (redirects to
  plain HTTP are refused), refuses development builds and package-managed installs (winget,
  Scoop, Chocolatey: detected from the executable's path, the user is given the manager's
  command), and selects exactly `oow-<version>-windows-<GOARCH>.exe`. The download is capped
  (256 MiB and the size GitHub lists), hashed while streaming, written next to the executable
  as `oow.exe.new` with exclusive create, and hashed again from disk; any failure removes the
  staged file through the verified sink and changes nothing else. The executable is then
  renamed to `oow.exe.old` and the new file moved in, with renames that never overwrite; if the
  second move fails the first is undone. `oow.exe.old` is removed at the next start through
  `RemoveVerified` with an exact final-path check (never a folder or link). There is no
  environment variable that changes where updates come from; tests point the client at a
  local server through a test-only hook. `GITHUB_TOKEN`/`GH_TOKEN` is optional, sent only to
  the API host (Go's client drops it on the redirect to GitHub's storage host; tested), and
  never printed.
- **`oow remove`** lists every item and needs confirmation or `--yes`. It removes the user
  `Path` entries equal to `%LOCALAPPDATA%\Programs\oow` (variables expanded for comparison
  only; other entries kept byte for byte; the write goes through `envpath.Store`, the same
  compare-and-swap writer as `repair`: it is refused if the value changed since it was read,
  and the value is read back afterwards), then broadcasts `WM_SETTINGCHANGE`. It moves the config and data
  folders to the Recycle Bin through `RecycleVerified` with `PurposeSelfRemove`: the guard
  allows exactly one of the tool's own folders, never a parent or child, and never one that is
  critical, whitelisted (or contains a whitelisted path), sensitive, in a system tree or in
  user content. Folders chosen with `OOW_CONFIG_DIR`/`OOW_DATA_DIR`, or not at the Known
  Folder location, are kept. The executable is removed only when it is exactly
  `%LOCALAPPDATA%\Programs\oow\oow.exe`, never elsewhere (a source build is reported and kept);
  because the running program cannot delete itself, `oow remove` uses no self-deletion
  technique and prints the command to run after exit. Package-managed installs are refused
  with the manager's uninstall command. The log file is closed before its folder is moved,
  and the run is recorded in history only when the history is kept (`--keep-data`).
- **Registry writes**: `oow update` writes none; `oow remove` writes only the user `Path`
  value (`HKCU\Environment`), only to drop the installer's entry. The other reviewed registry
  writes (StartupApproved values, user `Path` repair) are described in the next section.

### Startup, doctor, optimize and repair (Phase 6)

These commands delete no user files. What each one may change, and how:

- **`doctor` only reads.** Registry values (restart flags, `wuauserv` start type, Windows Update
  policy and pause times), `GetDiskFreeSpaceEx`, `GetAdaptersAddresses` (local adapter
  configuration; nothing is sent), `exec.LookPath` for package managers, a read-only cleanup scan,
  and folder write access checked by opening the folder with `FILE_ADD_FILE |
  FILE_ADD_SUBDIRECTORY` and closing it (no file is created; backup privileges are not enabled,
  so ACLs apply). Facts that cannot be read are reported as unknown, never as fine.
- **`startup` writes only StartupApproved values** (`HKCU|HKLM\...\Explorer\StartupApproved\Run`,
  `Run32`, `StartupFolder`), the mechanism Task Manager uses: `02 00 00 00` + zero FILETIME to
  enable, `03 00 00 00` + FILETIME to disable. Run/RunOnce values and Startup folder files are
  never edited or deleted, so every change is reversible (`oow startup enable`), and the previous
  value is recorded in history. RunOnce entries are not toggled. The entry is re-checked to still
  exist immediately before the write, the value is read back, and the new state is verified by
  listing again. HKLM keys are opened in the 64-bit view explicitly. Machine-wide entries are
  skipped with "requires administrator" unless elevated (interactively, an elevated window is
  offered). A program is called missing only after a verified "not found" on a fixed drive: bare
  names resolved through PATH, UNC paths, removable or disconnected drives and unreadable
  shortcuts are "unknown", so nothing is offered for repair on a guess.
- **Shortcut parsing** (`startup.ParseLink`, MS-SHLLINK) reads at most 1 MiB, bounds-checks every
  offset and count, and returns an error instead of panicking (fuzzed: `FuzzParseLink`). ANSI
  strings are decoded with the system code page, never guessed.
- **`optimize` runs only owner interfaces**, each only after confirmation (`--yes` for scripts):
  `DnsFlushResolverCache` (dnsapi), `Delete-DeliveryOptimizationCache -Force` (pinned files kept;
  this is the reviewed owner-tool path for the Delivery Optimization cache, which `oow` never
  deletes itself), and `Optimize-Volume -DriveLetter X -ReTrim` on fixed NTFS/ReFS volumes whose
  device reports no seek penalty and TRIM support (`IOCTL_STORAGE_QUERY_PROPERTY` on a handle opened
  with no access rights). PowerShell is started from System32 by full path, hidden, with fixed
  scripts (drive letters validated as `^[A-Z]:\\$`) and timeouts. The cache size is measured
  read-only before and after (links not followed). The admin tasks are skipped without elevation.
  Pending restarts, update state and low disk space are shown for information only; `oow` never
  restarts Windows, resets networking, restarts Explorer, edits update settings or tweaks the
  registry.
- **`repair` changes only user-level state.** User PATH (`HKCU\Environment\Path`): only missing,
  duplicate and empty entries the user confirmed (or the preselected ones with `--yes`) are
  removed; order, spelling, unexpanded variables and the value type are preserved; values with
  quoted entries are never rewritten; missing folders inside the profile are not preselected.
  Before writing, the previous value is saved as a `.reg` file in `<data dir>\backups` (written to
  a temporary file, renamed without overwriting, read back and parsed), and the write is a
  compare-and-swap against the analyzed value (a concurrent change aborts with nothing written).
  The value is read back after writing, `WM_SETTINGCHANGE("Environment")` is broadcast with
  `SendMessageTimeout`, and the backup path is recorded in history. The machine PATH is never
  written, only reported. Broken startup entries are disabled through the same StartupApproved
  path as `oow startup disable`.
- **Sandbox mode** replaces every one of these with simulations under `<sandbox>\registry`
  (startup values, PATH values, system facts, task state); the simulated Delivery Optimization
  cache is emptied through `filesystem.RemoveVerified`, confined to the cache folder and the fence.

### Project artifacts (purge) and installer packages

- **Purge scope.** Only folders passed as arguments, configured `purge.paths`, or existing default
  project folders in the profile are scanned; roots that are links, system trees, AppData,
  sensitive or protected locations, other users' profiles or the system drive are refused. The
  walk is depth-bounded, never follows links and skips dot folders, `node_modules`, vendored code
  and `Package Cache`. An artifact needs its project's marker file next to it; folders holding
  project markers are never artifacts, and artifacts are never searched for more projects.
- **Keep rules** (evaluated at scan time and again right before deletion): Git-tracked files
  (`git ls-files --cached` with `:(top,literal,icase)` pathspecs), nested `.git`, links and
  junctions, cloud-only placeholders, unreadable subfolders, sensitive file types (in package
  stores only at the top level). Git runs with `core.fsmonitor=false`, `core.hooksPath=NUL`,
  `GIT_OPTIONAL_LOCKS=0`, no inherited `GIT_*` variables and a timeout; it only reads. A missing
  or failing Git keeps every artifact inside a repository (fail closed). Recent activity (7 days)
  and dist/build/out without Git-ignore evidence are never preselected; `--yes` takes only the
  preselection.
- **Preselected: permanent deletion, verified per file; added from review: Recycle Bin.**
  Preselected artifacts (full evidence; the only ones `--yes` takes) are deleted permanently
  (rebuildable data; the Shell path is impractically slow, quota-limited and verifies only the
  top folder). Artifacts the user adds from review (weaker evidence or recent activity) are moved
  whole to the Recycle Bin through `RecycleVerified`; without a Recycle Bin they are kept, never
  deleted. Each artifact is first re-verified (same folder by creation time, not a link, guard,
  fresh walk with no new or changed files since the scan, Git again); then each file goes through
  `RemoveVerified` and folders are removed deepest first (or the folder is recycled), every final
  path checked with `PurposePurge` scoped to the artifact: known artifact name, parent not a drive root or never-remove location, outside
  system trees, AppData and profile tool folders (dot folders, `scoop`, `go\pkg`, Conda), never
  inside `.git`, never a sensitive file type outside package internals, never whitelisted or
  protected. A parent swapped for a junction after the scan resolves outside the scope and is
  refused at the sink (tested).
- **Installers** are identified by content (compound-file class ID, MSIX manifest, PE structure
  with setup-engine data or a setup version resource read with `GetFileVersionInfo`, ZIP root
  entries, ISO root directory), never by name or extension alone; MSI properties are read with the
  read-only Windows Installer database API; cloud placeholders are never opened. Search is
  limited to Downloads, Desktop and Documents (or user-given folders outside system trees and
  AppData, so `Windows\Installer` and `Package Cache` are never touched), 3 levels deep, skipping
  links and repositories. Only packages whose product is installed (exact ProductCode, MSIX
  identity or normalized name) and older than 7 days are preselected. Removal goes to the Recycle
  Bin through `RecycleVerified` with `PurposeUserSelected`. Every run is recorded in history.

### Tool-owned and whitelisted locations

`oow`'s own config and data directories, and every path in the user's whitelist, are
protected together with their ancestors (deleting a parent would delete them).

## Implementation map

| Control | Code |
|---|---|
| Win32 path normalization | `internal/safety/path.go` `Normalize`, `Key`, `IsWithin` |
| Location discovery (Known Folders, no env for roots) | `internal/safety/locations_windows.go` |
| Protection policy | `internal/safety/guard.go` `NewGuard`, `Check`, `ValidateRoot`, `IsSensitiveName` |
| Root resolution (exists, not a link, final == expected) | `internal/cleanup/scan.go` `resolveRoot` |
| Link-free enumeration | `internal/filesystem/walk.go` `Walk` |
| Verified deletion | `internal/filesystem/remove_windows.go` `RemoveVerified` |
| Deletion fence | `internal/filesystem/fence.go` |
| Rule schema validation | `internal/cleanup/rule.go` `Validate` |
| Whitelist load/validate (fail closed) | `internal/config/config.go`, `cli.App.requireConfig` |
| Operation log | `internal/history/history.go` |

## Links and path traversal

- Enumeration never descends into reparse points; they are reported as skipped.
- `RemoveVerified` never deletes a reparse point and re-resolves the final path, so a parent
  folder swapped for a junction after the scan is refused even when names, sizes and
  timestamps are forged to match (tested).
- A cleanup root that is itself a link, or whose final path differs from its expected path, is
  marked *review required* and left alone.
- Normalization strips `\\?\`, `\??\` and `\\.\` prefixes, resolves `.` and `..` (clamped at the
  root), trims trailing dots and spaces per component, and compares case-insensitively.
  Relative and drive-relative paths, alternate data streams, wildcards, volume GUID and device
  paths, and malformed UNC paths are rejected. UNC (network) paths are never deleted.
- 8.3 short names are resolved by the OS: the final path from the handle is always long-form.
- Hard links: deleting a hard link removes only that name; other links keep the data.
  Reclaimed totals may overstate freed space in that case; reports also show the measured
  free-space change.

## Privilege boundaries

- `oow` never requires elevation as a whole and never elevates silently.
- Rules marked `RequiresAdmin` are skipped with an explanation when not elevated. In an
  interactive session `oow clean` then *offers* to start a separate elevated window
  (`ShellExecuteExW` with `runas`, i.e. the standard UAC prompt) limited to exactly those
  targets; that window runs its own scan, report, checklist and confirmation. Declining UAC
  changes nothing. Sandbox mode never elevates.
- Elevated runs use exactly the same guard, exemptions and verified sink; elevation never
  widens what may be deleted.
- `FILE_FLAG_BACKUP_SEMANTICS` is used to open directories; `oow` does not enable
  `SeBackupPrivilege` or `SeRestorePrivilege`, so it cannot bypass ACLs.
- Access-denied items are skipped and counted, never retried with broader means.

## Sensitive data handling

- Logs (`%LOCALAPPDATA%\oow\logs\oow.log`) record paths of skipped or failed items and
  rule IDs; they never record file contents.
- History records counts, bytes and skip reasons per target, not file lists.
- JSON output includes full paths only with `--details`.
- No data leaves the machine. The only network access is `oow update`, on explicit request:
  it reads release metadata and downloads release files from GitHub, sending nothing but the
  request itself (and `GITHUB_TOKEN`/`GH_TOKEN`, when set, to the GitHub API only).

## Dry run, confirmation and audit logging

- `--dry-run` on every destructive command; `OOW_DRY_RUN=1` forces it globally. Tests assert
  dry runs leave the filesystem byte-for-byte unchanged.
- High-risk flows require explicit confirmation; scripts need `--yes`.
- Every real run is appended to `history.jsonl` (disable with `OOW_NO_OPLOG=1`), including
  cancelled runs.
- A malformed configuration blocks destructive commands instead of dropping the whitelist.

## Continuous security signals

- CI on disposable **Windows Server 2025 and 2022** VMs: `gofmt`, `go vet`, full tests,
  read-only real-system tests, and fuzzing of the guard invariant (`FuzzGuardScope`) and
  normalization (`FuzzNormalizeIdempotent`).
- **Real end-to-end cleanup on a disposable VM** (`scripts/ci/e2e-real.ps1`): plants canary
  files in Documents, Desktop, AppData and ProgramData plus a junction to Documents inside
  Temp, runs a real elevated `oow clean --yes`, and fails if any canary is touched, any junk
  survives, or any unexpected error occurs.
- CodeQL analysis of Go code and Dependabot updates for Go modules and GitHub Actions.

## Testing coverage

| Area | Tests |
|---|---|
| Dangerous paths never deletable in any spelling or scope | `internal/safety/guard_test.go` `TestDangerousPathsNeverDeletable`, `TestSystemAndCriticalNeverDeletableByUserSelection` |
| Dangerous / hijacked roots rejected | `TestDangerousPathsNeverValidRoots`, `TestRedirectedTempCannotWidenRoots`, `TestSystemTreeOnlyInsideExemption` |
| Sensitive locations and file types | `TestSensitiveLocationsNeverDeletable`, `TestSensitiveFileTypesNeverAutoCleaned` |
| Baseline without discovery | `TestBaselineProtectionWithoutDiscovery` |
| Scope enforcement, missing scope | `TestScopeIsEnforced`, `TestCleanupWithoutScopeDenied` |
| Normalization | `internal/safety/path_test.go`, `FuzzNormalizeIdempotent`, `FuzzGuardScope` |
| Real system paths and 8.3 names (CI) | `internal/safety/realsystem_test.go` |
| Links never followed or deleted | `internal/filesystem` `TestWalkDoesNotFollowJunctions`, `TestWalkRootJunctionRefused`, `TestRemoveVerifiedRefusesJunction` |
| Junction-swap race | `TestRemoveVerifiedJunctionSwapAttack`, `internal/cleanup` `TestJunctionSwapAfterScan` |
| Modified, replaced, locked, read-only, permission-denied | `TestRemoveVerified*`, `TestLockedFileSkippedOthersRemoved`, `TestFileChangedAfterScanIsKept`, `TestUnreadableFolderInsideRoot` |
| Deletion fence | `TestFenceBlocksDeletionOutside`, `TestFenceCannotBeWidened`, `TestCheckFence` |
| Dry run is zero-write | `TestDryRunChangesNothing`, `internal/cli` `TestCleanDryRunJSONChangesNothing`, `TestDryRunEnvForcesPreview` |
| Confirmation required | `TestCleanRefusesWithoutConfirmation` |
| Whitelist (rules and paths), malformed config | `TestWhitelistedRuleSkipped`, `TestWhitelistedPathKept`, `TestWhitelistRuleAndPath`, `TestMalformedConfigBlocksClean` |
| Redirected or missing roots | `TestRedirectedRootIsNotCleaned`, `TestMissingRootIsEmpty` |
| Cancellation | `TestCancelledExecuteRemovesNothing`, `TestCancelledScan`, `TestWalkCancellation` |
| Browser profile data, Electron app state, dev stores kept | `internal/cleanup/phase2_test.go` (`TestChromium…`, `TestFirefoxCacheOnly`, `TestElectronAppsKeepStateAndSettings`, `TestDeveloperCachesFollowRecoveryContract`, `TestINetCacheKeepsOutlookAttachments`, `TestJetBrainsIsOptInAndKeepsLocalHistory`) |
| Running apps skipped | `TestRunningAppIsSkipped` |
| Recycle Bin special, opt-in | `TestRecycleBinSpecial`, `internal/cli` `TestOptInTargetsNeedExplicitSelection` |
| Rule schema (`{profile}`, specials, roots) | `TestRuleSchemaValidation`, `TestBuiltinRulesAreValid` |
| Win32 struct layouts | `internal/elevation` `TestShellExecuteInfoSize`, `internal/system` `TestRecycleBinInfoLayout`, `internal/filesystem` `TestSHFileOpStructLayout` |
| Leftover guard purpose | `internal/safety` `TestLeftoverPurpose` |
| Uninstall plans, waiting, verification, cancel, restart, failure | `internal/uninstall/uninstall_test.go` |
| Leftovers: evidence, confidence, claims, traces, broken entries, sensitive content, admin, junctions, swaps | `internal/leftovers/leftovers_test.go` |
| Uninstall/leftovers CLI (dry run, confirmation, JSON, failure, end to end) | `internal/cli/uninstall_test.go` |
| Name normalization and command-line parsing | `internal/apps/apps_test.go` |
| Analyzer totals, links, unreadable folders, cancellation, largest files | `internal/analyzer/analyzer_test.go` |
| Analyzer Recycle Bin guard (user files yes; system, sensitive, folder roots, junctions no) | `internal/cli` `TestRecyclePathsGuard` |
| Explorer navigation, confirmation, size updates | `internal/ui/explorer_test.go` |
| End-to-end on a real VM | `scripts/ci/e2e-real.ps1` (temp, Windows Temp, Chrome profile with credential canaries, npm, Recycle Bin, uninstall of a registered app with leftovers) |
| Self-update: version ordering, dev builds, `SHA256SUMS` parsing, missing file or entry, mismatch, wrong size, asset per architecture, token only to the API and not across redirects, staging, replace, rollback, never overwriting | `internal/selfupdate/selfupdate_test.go` |
| `update` CLI: newer/same/older, dry run zero-write, confirmation, fail closed, dev build, package manager | `internal/cli/update_test.go` |
| Self-remove guard purpose (exact own folder only; whitelist, sensitive, system, user content, critical refused) | `internal/safety` `TestSelfRemovePurpose` |
| `remove` CLI: dry run, confirmation, PATH entry only, Recycle Bin, `--keep-data`, whitelist, package manager, copies outside the install folder, running executable | `internal/cli/remove_test.go`, `TestRemoveRunningExecutableNeedsAManualStep` |
| PATH editing, package-manager detection, verified executable and folder removal, links refused | `internal/install/install_test.go` |
| PowerShell scripts parse in PowerShell 7 and 5.1 | `.github/workflows/ci.yml` |
| StartupApproved byte semantics, target resolution (missing vs unknown), admin and RunOnce refusals, verified writes | `internal/startup/startup_test.go` (`TestParseApproval`, `TestApprovalDataMatchesTaskManager`, `TestCommandTarget`, `TestSetEnabled`, `TestSetEnabledVerifiesAndReportsFailures`) |
| Shortcut parser bounds | `internal/startup/lnk_test.go` (`TestParseLinkRejectsMalformed`, `FuzzParseLink`) |
| PATH analysis, exact removal, `.reg` backup round trip, no-overwrite backups | `internal/envpath/envpath_test.go` |
| Repair: preselection, backup before write, compare-and-swap, machine PATH untouched, cancellation | `internal/repair/repair_test.go` |
| Doctor never reports unreadable facts as ok; thresholds | `internal/doctor/doctor_test.go` |
| Optimize: admin tasks skipped without elevation, only ready tasks run, cancellation | `internal/optimize/optimize_test.go` |
| Write-access probe creates nothing | `internal/system/probe_test.go` `TestCanCreateInDoesNotWrite` |
| Startup/doctor/optimize/repair CLI: dry run and doctor change nothing, exit 4 without `--yes`, `OOW_DRY_RUN`, history | `internal/cli/system6_test.go` |
| Real StartupApproved write/read-back, `.lnk` parsing of Shell-made shortcuts, user PATH repair with backup (CI only) | `scripts/ci/e2e-real.ps1` (startup, doctor, optimize, repair group) |
| Purge guard purpose (scope, traversal, `.git`, sensitive files, AppData, tool folders, whitelist) | `internal/safety` `TestPurgePurpose`, `TestValidatePurgeArtifact`, `FuzzPurgeScope` |
| Purge discovery and keep rules (tracked files, nested repos, links, keys, vendor, recent, ambiguous) | `internal/purge` `TestFindInSandbox`, `TestRemoveDeletesOnlySelected` |
| Purge review artifacts go to the Recycle Bin (kept without one), preselected ones are deleted | `TestReviewArtifactsGoToTheRecycleBin`, `TestReviewArtifactWithoutRecycleBinIsKept` |
| Purge fail closed (Git missing or failing), changes and junction swaps after the scan, sink refusal | `TestGitMissingKeepsRepositoryArtifacts`, `TestGitFailureKeepsArtifacts`, `TestChangesAfterScanAreKept`, `TestSinkRefusesPathsOutsideTheArtifact`, `TestGitPathspecsAreLiteral` |
| Purge CLI (dry run zero-write, exit 4, JSON, history, folders, `OOW_DRY_RUN`) | `internal/cli/purge_test.go` |
| Installers by content, never by name; exact installed matching; Recycle Bin re-verification | `internal/installer/installer_test.go`, `internal/cli/installer_test.go` |
| Real purge and installer runs on a VM | `scripts/ci/e2e-real.ps1` (tracked `dist`, recent project, junction to Documents, deployment key, look-alike installers) |

All file-creating tests run inside `.sandbox/` with the deletion fence set; none touch the
developer's real system.

## Known limitations and future work

- Cache and temp cleanup, and purge of preselected project artifacts, are permanent (no
  undo); this is limited to regenerable data. User-chosen files (analyzer, installers,
  leftovers, purge artifacts added from review) go to the Recycle Bin.
- Identity is checked by type, size and timestamps rather than file ID; a same-size rewrite
  that also restores both timestamps would pass the identity check (the final-path and guard
  checks still apply).
- Free-space deltas are approximate when other programs write concurrently.
- Releases carry SHA-256 checksums (`SHA256SUMS`) and build-provenance attestations, and the
  installer and `oow update` verify the checksum and fail closed. The checksum file comes from
  the same release, so it proves the download matches the release, not that the release is
  authentic; the attestation proves that, but it is checked only by users
  (`gh attestation verify`), not automatically (that needs a Sigstore client). Binaries are
  not Authenticode-signed yet (no certificate). Attestations need a public repository (or
  GitHub Enterprise Cloud); until then a tag push fails at the attestation step, before
  anything is published.
- Microsoft Defender's machine-learning detection may flag `oow.exe` (and its test binaries)
  as an information stealer: the binary names credential stores, wallets and key files in
  order to protect them. It is a false positive; signing and submissions to Microsoft are the
  planned fixes, and strings are never obfuscated to evade it. Binaries carry Windows version
  information and an `asInvoker` manifest (checked in CI). See
  [docs/DEFENDER.md](docs/DEFENDER.md) for verification and the submission process.
- A running program cannot delete its own file on Windows, so `oow remove` leaves `oow.exe`
  and prints the exact command that removes it (and its empty folder) after exit.
- Cache locations of third-party apps are taken from their documented or long-standing
  layouts; when an app changes its layout the rule finds nothing (it never widens).
- Vendor uninstallers are third-party programs: `oow` controls when they run and verifies the
  result, not what they do. Scheduled tasks are not yet used as claims.
- Not implemented, each needing its own review: `optimize` tasks for the Windows Update
  cache and the component store (DISM). Installer packages' Authenticode signatures are
  noted but not verified (detection relies on content, not on the signature).
- Threat modelling of elevated uninstall flows (running vendor uninstallers) is pending.

To report a problem, see [SECURITY.md](SECURITY.md).
