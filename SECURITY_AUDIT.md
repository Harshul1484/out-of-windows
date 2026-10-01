# Security Audit

Status: **v0.1.0-dev (Phase 1)**. This document records what the code does today. Planned
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
guard, not in rules. Current list:

```text
%WINDIR%\Temp
```

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

- `oow` never requires elevation as a whole and never self-elevates today.
- Rules marked `RequiresAdmin` are skipped with an explanation when not elevated.
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
| End-to-end on a real VM | `scripts/ci/e2e-real.ps1` |

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
- Planned with their own reviews: browser and application caches (profile-marker roots,
  skip-while-running), Recycle Bin emptying through the Shell API, elevation on demand,
  uninstall and leftovers (exact evidence and confidence levels), disk analyzer deletion via
  the Recycle Bin, purge safety rules (Git-tracked content, nested repositories, recent
  activity), `optimize` tasks through supported Windows APIs only.
- Threat modelling of elevated uninstall flows (running vendor uninstallers) is pending.

To report a problem, see [SECURITY.md](SECURITY.md).
