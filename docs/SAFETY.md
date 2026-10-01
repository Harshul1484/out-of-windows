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
whatever flags are passed. Cache and temp cleanup deletes permanently because the data is
regenerated; user files (analyzer, installers) will go to the Recycle Bin, and permanent
deletion of user files will require typing a confirmation word.

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
| Real cleanup with canary files | `scripts/ci/e2e-real.ps1` (CI only) |
