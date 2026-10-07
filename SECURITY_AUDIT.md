# Security Audit

Status: **v0.1.0-dev (Phases 1–5)**. This document records what the code does today. Planned
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
6. **Release integrity** of distributed binaries (no releases yet; see future work).

Lower risk: read-only commands (`history`, `config`, `version`, the home screen) and the
planned `analyze` flow (user-chosen items, Recycle Bin).

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
A user may delete items there explicitly (planned analyzer, Recycle Bin).

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
- No data leaves the machine: there is no network code.

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
| StartupApproved byte semantics, target resolution (missing vs unknown), admin and RunOnce refusals, verified writes | `internal/startup/startup_test.go` (`TestParseApproval`, `TestApprovalDataMatchesTaskManager`, `TestCommandTarget`, `TestSetEnabled`, `TestSetEnabledVerifiesAndReportsFailures`) |
| Shortcut parser bounds | `internal/startup/lnk_test.go` (`TestParseLinkRejectsMalformed`, `FuzzParseLink`) |
| PATH analysis, exact removal, `.reg` backup round trip, no-overwrite backups | `internal/envpath/envpath_test.go` |
| Repair: preselection, backup before write, compare-and-swap, machine PATH untouched, cancellation | `internal/repair/repair_test.go` |
| Doctor never reports unreadable facts as ok; thresholds | `internal/doctor/doctor_test.go` |
| Optimize: admin tasks skipped without elevation, only ready tasks run, cancellation | `internal/optimize/optimize_test.go` |
| Write-access probe creates nothing | `internal/system/probe_test.go` `TestCanCreateInDoesNotWrite` |
| Startup/doctor/optimize/repair CLI: dry run and doctor change nothing, exit 4 without `--yes`, `OOW_DRY_RUN`, history | `internal/cli/system6_test.go` |
| Real StartupApproved write/read-back, `.lnk` parsing of Shell-made shortcuts, user PATH repair with backup (CI only) | `scripts/ci/e2e-real.ps1` (startup, doctor, optimize, repair group) |

All file-creating tests run inside `.sandbox/` with the deletion fence set; none touch the
developer's real system.

## Known limitations and future work

- Cache and temp cleanup is permanent (no undo); this is limited to regenerable data. User
  files (analyzer, installers, purge) will go to the Recycle Bin.
- Identity is checked by type, size and timestamps rather than file ID; a same-size rewrite
  that also restores both timestamps would pass the identity check (the final-path and guard
  checks still apply).
- Free-space deltas are approximate when other programs write concurrently.
- No releases exist yet. Planned: signed binaries, SHA-256 checksums, build-provenance
  attestations, and installers that verify and fail closed.
- Cache locations of third-party apps are taken from their documented or long-standing
  layouts; when an app changes its layout the rule finds nothing (it never widens).
- Vendor uninstallers are third-party programs: `oow` controls when they run and verifies the
  result, not what they do. Scheduled tasks are not yet used as claims.
- Planned with their own reviews: disk analyzer deletion via the Recycle Bin, purge safety rules (Git-tracked
  content, nested repositories, recent activity), `optimize` tasks through supported Windows
  APIs only (Delivery Optimization, Windows Update cache, component store).
- Threat modelling of elevated uninstall flows (running vendor uninstallers) is pending.

To report a problem, see [SECURITY.md](SECURITY.md).
