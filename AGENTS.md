# Agent Guide

This file is the shared source of truth for any AI agent working on this repository (Claude
Code, Codex, Copilot, Cursor and others). `CLAUDE.md` imports it, so every agent receives the
same project contract. Put machine-specific or personal overrides in `AGENTS.local.md` or
`CLAUDE.local.md`; both are git-ignored.

## Project

`oow` (out-of-windows) is a Windows-native system maintenance CLI written in Go: cleanup,
complete app uninstall, disk analysis, installer and developer-artifact cleanup, bounded
maintenance, diagnostics and a live status dashboard. It deletes files on real machines, so
**safety rules matter more than speed, features or reclaimed gigabytes.**

## Product Direction

`oow` is a terminal-first Windows maintenance toolkit. Its job is to help users see what is
using their machine, remove known-safe reclaimable data, uninstall apps completely, run
bounded and explained maintenance, and check health, from a CLI, a script (`--json`), or a
compact TUI. It is not a general Windows control panel, package manager, registry editor,
"PC optimizer", background service or GUI.

### What oow should do

- Make cleanup and uninstall boring, reviewable, logged, protected by path and app rules, and
  previewable with `--dry-run`.
- Follow **Discover → Explain → Confirm → Act → Verify** for every destructive action, and
  answer: what is removed, why it is safe, how much space, what happens afterwards, whether it
  can be undone, and what actually changed.
- Prefer the Recycle Bin for user-chosen files (analyzer, installers, leftovers, and purge
  artifacts the user adds from review). Permanent deletion is for regenerable caches,
  temporary data and the artifacts purge preselects: they are rebuildable by definition and
  dependency trees are too large for the Recycle Bin (see `docs/SAFETY.md` §3c).
- Keep `clean`, `uninstall`, `leftovers`, `purge` and `installer` focused on exact, known
  cleanup targets: temporary data, rebuildable caches, app leftovers with exact evidence,
  installer packages, and rebuildable build output.
- Keep `analyze` a disk explorer with safe, user-driven deletion. Optimize first paint,
  navigation, sorting, filtering and cancellation before adding dashboard features.
- Keep `status` a compact read-only dashboard plus stable JSON/NDJSON output. It may surface
  actionable signals; it must not become an alerting daemon or a metrics workbench.
- Keep `optimize` to explicit, bounded maintenance tasks that can be explained before running
  and tested without real elevation prompts.
- Keep `doctor` diagnose-only: it reports and suggests; it never applies destructive fixes.
- Keep UX dense and terminal-native: short labels, stable alignment, predictable shortcuts,
  one-screen summaries, optional drill-down. Color is never required to understand output.
- Keep routine per-item skips out of the headline summary: show honest grouped counts and
  reasons, keep per-item detail in logs and `--debug`.

### What oow must not do

- No registry cleaning, "RAM boosting", fake health or performance scores, "your PC is 73%
  slower" warnings, exaggerated claims, telemetry, background services, scheduled tasks or
  tray icons.
- Do not delete or rewrite application binaries, signed resources, user documents,
  credentials, sessions, browser profile data (cookies, passwords, history, bookmarks),
  active databases, or active developer-tool state.
- Do not broaden leftover matching from exact evidence (uninstall registry entries, install
  locations, package identities, exact folder ownership) to vendor-wide, publisher-prefix,
  generic-name or wildcard deletion.
- Do not substitute deleting an installation folder for running the app's real uninstaller.
- Do not turn `status` into a noisy dashboard; new rows need a common user action.
- Do not add prompts, flags, environment variables or config keys to patch edge cases. A new
  knob is a new setting: it is justified only when no single default is right for everyone,
  and the fix-by-default alternative must be stated and rejected first.

### Product decision filter

Before accepting a feature, answer in the PR or issue when the fit is not obvious:

1. Does it clearly belong to `clean`, `uninstall`, `leftovers`, `analyze`, `status`,
   `processes`, `optimize`, `startup`, `doctor`, `repair`, `purge`, `installer`, `history`,
   `config`, `update`, `remove` or `completion`?
2. Is it safe by default, previewable where destructive, testable in the sandbox without real
   elevation, and explainable on one terminal screen?
3. Can the user verify what will change before `oow` changes it?
4. Is the target data regenerable, disposable, or tied to exact app evidence?
5. Would documentation, a warning, or an explicit "not supported" answer be better?

If any answer is no or unclear, decline, narrow, or park the feature.

**Magnitude needs real samples.** When keep-or-kill depends on how much space a target holds
or how many users have it, ship a read-only probe first (list and size only, no writes, no
elevation, seconds to run, output that can be pasted whole). Print structure, not just totals
(rebuildable vs authored, active vs stale). Write the decision threshold down before the data
arrives.

## Repository Map

- `AGENTS.md`: this contract. `CLAUDE.md` imports it with `@AGENTS.md`; do not copy rules into
  other agent files.
- `cmd/oow/main.go`: entry point only. Business logic does not belong here.
- `internal/cli/`: command router, flags, text and JSON rendering, exit codes, home dispatch.
  One file per command (`clean.go`, `config_cmd.go`, `history_cmd.go`, ...); `json.go` holds
  the JSON schema types; `root.go` registers commands and the planned-command stubs.
- `internal/cleanup/`: the cleanup engine. `rule.go` (Rule schema, validation), `rules.go`
  (registry and shared explanations), `rules_<category>.go` (one file per category: temp,
  windows, browsers, apps, dev, logs), `scan.go` (read-only scan, `{profile}` expansion,
  specials), `execute.go` (verified execution, skip reasons, outcome accounting).
- `internal/elevation/`: relaunches one command elevated through the UAC prompt
  (`ShellExecuteExW` `runas`) and waits for it, and runs uninstallers through the Shell. Never
  used silently.
- `internal/apps/`: installed-app discovery (Uninstall registry views, AppX via PowerShell JSON,
  Scoop, Chocolatey), name normalization (`NormalizeName`, `IsDistinctive`), uninstall
  command-line parsing.
- `internal/uninstall/`: uninstall plans per source, execution through a `Runner`, waiting and
  verification through a `Checker`.
- `internal/purge/`: project artifact purge. `kinds.go` (artifact kinds and marker files),
  `purge.go` (roots, discovery, measurement, keep/review rules), `git.go` (read-only Git
  tracked/ignored checks), `remove.go` (re-verification and per-file verified deletion).
- `internal/installer/`: installer packages. `detect.go` (identification by content: compound
  file class IDs, MSIX manifests, PE setup-engine data, ZIP roots, ISO roots),
  `native_windows.go` (version resources, MSI properties, Known Folders), `installer.go`
  (search, exact installed matching, preselection, Recycle Bin).
- `internal/leftovers/`: evidence (uninstalled, history, broken entries, usage traces, broken
  Start menu and Desktop shortcuts in `shortcuts.go`), claims (installed apps, processes,
  services, startup, scheduled tasks), `Find` with confidence, `Recycle` (a leftover's broken
  shortcuts follow it under `PurposeShortcut`).
- `internal/monitor/`: read-only metrics. `Source` (real: kernel process table, per-core times,
  PDH English counters, `nvidia-smi`; sandbox: `sandbox.Monitor`) and `Compute` (rates from two
  readings). The dashboard is `internal/ui/status.go`.
- `internal/analyzer/`: parallel read-only scanner (folder tree, largest-files heap), on-demand
  file listing. The explorer TUI is `internal/ui/explorer.go`; deletion goes through
  `App.recyclePaths` (guard user-selected purpose + verified recycle).
- `internal/startup/`: startup entries (Run/RunOnce values, Startup folders, `.lnk` parser,
  scheduled tasks with sign-in or startup triggers), target resolution
  (`found`/`missing`/`unknown`), and enable/disable through StartupApproved values or a task's
  Enabled flag only (`SetEnabled`, verified). Real store `System`; sandbox `sandbox.Startup`.
- `internal/tasks/`: scheduled tasks through the Task Scheduler COM API (`ITaskService`, vtable
  slots from the type library, amd64/arm64 only): read-only listing and XML parsing, and the one
  write `SetEnabled` (`IRegisteredTask.Enabled`, read back). Sandbox `sandbox.Tasks`
  (`registry\tasks.json`).
- `internal/envpath/`: user and machine PATH analysis, exact entry removal, `.reg` backups, and a
  compare-and-swap write of the user PATH only (`Store`; sandbox `sandbox.Paths`).
- `internal/doctor/`: read-only `Probe` (sandbox `sandbox.Doctor`), `Facts`, and pure `Evaluate`
  checks with status, explanation and next step. Never changes anything.
- `internal/optimize/`: the bounded maintenance tasks, `Plan` and `Run` through a `Runner` (real:
  DNS flush, Delivery Optimization cmdlet, `Optimize-Volume -ReTrim`; sandbox `sandbox.Optimizer`).
- `internal/repair/`: user-level fixes from PATH and startup findings, `Apply` with a verified
  backup before any PATH write.
- `internal/safety/`: Win32 path normalization (`path.go`), discovered locations
  (`locations.go`, `locations_windows.go`), and the `Guard` (`guard.go`): protected,
  system, user-content, sensitive and whitelisted locations, exemptions, root validation.
- `internal/filesystem/`: the **only** deletion sink. `Walk` (never follows reparse points),
  `RemoveVerified` (handle-based, identity-checked delete), `FinalPath`, the deletion fence
  (`fence.go`), timestamps and free space helpers.
- `internal/config/`: `config.json` schema, load/validate/save (atomic), whitelist editing.
- `internal/history/`: append-only `history.jsonl` operation log.
- `internal/logging/`: slog setup; logs never go to stdout.
- `internal/system/`: Windows version, elevation, CPU, memory, disk, processes (read-only),
  and the Recycle Bin special (Shell API).
- `internal/ui/`: styles, formatting, prompts, spinner, Bubble Tea checklist and home screen.
- `internal/selfupdate/`: `oow update`: GitHub release client (token only to the API host),
  semantic versions, `SHA256SUMS` verification (fail closed), staging, rename-aside
  replacement with rollback, `.old` cleanup at start-up.
- `internal/install/`: how `oow` is installed: the installer folder
  (`%LOCALAPPDATA%\Programs\oow`), package-manager detection (winget, Scoop, Chocolatey), the
  installer's PATH entry (removed through `envpath.Store`, the same compare-and-swap writer
  `repair` uses), and executable removal through the verified sink (`ExeRemover`; the running
  program is never deleted).
- `internal/sandbox/`: simulated Windows layout and seed data for safe end-to-end runs.
- `internal/testutil/`: test sandbox, deletion fence setup, fixtures, file locking.
- `docs/SAFETY.md`: the safety model (design contract). `docs/ARCHITECTURE.md`,
  `docs/JSON.md` (output schemas), `docs/ROADMAP.md`.
- `SECURITY.md`: vulnerability reporting policy. `SECURITY_AUDIT.md`: security review notes.
- `scripts/ci/`: CI-only scripts (`e2e-real.ps1` performs a real cleanup and refuses to run
  outside CI).
- `scripts/install.ps1`, `scripts/install.cmd`: per-user installer (SHA-256 verified, fails
  closed, no admin). `packaging/`: winget, Scoop and Chocolatey templates, not published until
  the name is final.
- `.github/workflows/`: `ci.yml` (Windows Server 2025/2022 tests, fuzzing, real e2e,
  PowerShell script parsing), `codeql.yml`, `release.yml` (tag-driven builds, `SHA256SUMS`,
  attestations, GitHub release; manual runs are dry runs).

## Commands

```powershell
gofmt -l .                                   # must print nothing
go vet ./...
go test ./...                                # full suite, sandboxed
go test ./internal/cleanup                   # one package
$env:OOW_TEST_REAL_SYSTEM = "1"; go test ./...   # also read-only real-system tests
go test ./internal/safety -run '^$' -fuzz FuzzGuardScope -fuzztime 60s
go build -o bin/oow.exe ./cmd/oow

go run ./cmd/oow sandbox init .sandbox/demo          # simulated Windows layout
go run ./cmd/oow --sandbox .sandbox/demo clean --dry-run
$env:OOW_DRY_RUN = "1"; go run ./cmd/oow clean        # forced preview on the real system
```

Public docs and examples use the installed `oow` command. Use `go run ./cmd/oow` or
`bin/oow.exe` to verify source-tree behavior.

## Contracts and Schemas

These are interfaces users and scripts depend on. Changing them is a compatibility decision.

- **Exit codes** (`internal/cli/app.go`, documented in `docs/JSON.md`): 0 ok, 1 error,
  2 usage, 3 not available yet, 4 destructive action needs `--yes`, 130 cancelled.
- **JSON output**: every command supports `--json`; exactly one document on stdout, no
  decoration, prompts or color. Each document carries `schema` (e.g. `oow.clean/v1`).
  Within a version, fields may be added, never renamed, removed or redefined. Arrays are
  never `null`. Sizes in bytes, durations in ms, times RFC 3339. Errors are
  `{"error", "exit_code"}`. Update `docs/JSON.md` in the same change.
- **Config** (`%APPDATA%\oow\config.json`, `internal/config`): `version`, `whitelist.paths`,
  `whitelist.rules`, `ui.color`, `purge.paths`. Unknown newer versions are rejected; a
  malformed file is an error that blocks destructive commands (the whitelist must never be
  silently dropped). Writes are atomic (temp file + rename).
- **History** (`%LOCALAPPDATA%\oow\history.jsonl`): one JSON record per destructive run with
  per-target counts and skip reasons. Append-only; readers skip malformed lines. `oow history`
  is read-only. `OOW_NO_OPLOG=1` disables recording; preserve this behavior.
- **Cleanup rule schema** (`internal/cleanup/rule.go`): stable dotted `ID` (never reused or
  renamed: whitelists and JSON refer to it), `Name`, `Category`, `Roots` (location-token
  templates, contents only), `Include`/`Exclude` name patterns, `MaxDepth`, `MinAge` (creation
  **and** last-write time), `What`/`WhySafe`/`Impact` (all required, shown to users),
  `RequiresAdmin`, `DefaultSelected`, `AppProcesses` (skip while running), `DetectPaths`.
  `Rule.Validate` and the rule tests enforce the schema.
- **Location tokens**: `{Windows}`, `{WindowsTemp}`, `{ProgramData}`, `{UserProfile}`,
  `{RoamingAppData}`, `{LocalAppData}`, `{LocalLow}`, `{Temp}` (never used by rules). Unknown or
  unavailable tokens are errors, never empty strings.
- **Environment variables**: `OOW_DRY_RUN=1` (force preview), `OOW_SANDBOX=<dir>` / hidden
  `--sandbox` (simulated layout, fenced), `OOW_CONFIG_DIR`, `OOW_DATA_DIR`, `OOW_NO_OPLOG=1`,
  `NO_COLOR`; test-only `OOW_TEST_REAL_SYSTEM=1`, `OOW_KEEP_SANDBOX=1`.
- **Binary name**: always `buildinfo.Name`; never hard-code `oow` in messages. Data
  directories use `buildinfo.AppID`.

## Critical Safety Rules

- **All deletion goes through `filesystem.RemoveVerified`**, reached via the cleanup executor
  (or a future reviewed equivalent for the Recycle Bin). No `os.Remove`, `os.RemoveAll`,
  `DeleteFile`, `RemoveDirectory`, `SHFileOperation`, `del`, `rd /s` or PowerShell
  `Remove-Item` in product code. Test helpers may clean their own sandbox only.
- **Never follow reparse points** (junctions, symlinks, mount points) when scanning or
  deleting, and never delete a reparse point itself through cleanup. Detect them by
  `FILE_ATTRIBUTE_REPARSE_POINT`, not by `os.ModeSymlink`.
- **Re-check at the sink.** Identity (type, size, creation and write time), reparse status,
  cloud-placeholder status, the deletion fence and `Guard.Check` on the OS-resolved final
  path are verified through the same handle that deletes. A stale scan result is never
  trusted.
- **Rule-driven cleanup always carries a validated scope.** `Guard.Check` refuses
  `PurposeCleanup` without one. Roots must pass `Guard.ValidateRoot` and must not be links.
- **Never trust environment variables for cleanup roots.** Use Known Folder APIs. `%TEMP%`
  can point anywhere; user temp is `{LocalAppData}\Temp`.
- **Never touch protected locations**: drive roots; `Windows`, `Program Files`,
  `Program Files (x86)`, `ProgramData`, `System Volume Information`, `$Recycle.Bin`,
  `Recovery`, `pagefile.sys`/`hiberfil.sys`/`swapfile.sys`, `Windows.old`, `Config.Msi`
  (except reviewed exemptions); profile and AppData roots; user content folders;
  credential, key, wallet, VM and AI-tool state; the whitelist; the tool's own directories.
  Adding a system-tree exemption requires a written safety review in `docs/SAFETY.md`.
- **Sensitive file types are never auto-cleaned anywhere**: VM/WSL disks (`*.vhdx`,
  `*.vhd`, `*.vmdk`), password databases (`*.kdbx`), mail stores (`*.pst`, `*.ost`), private
  keys and certificates, wallets, browser credential databases. See `safety.sensitiveNames`.
- **Never raw-delete Windows Update staging, the Component Store (WinSxS), the Installer cache
  (`Windows\Installer`), the driver store, or `Package Cache`.** Their state cannot be proven
  inactive from file metadata. Use supported owner tools (DISM, Delivery Optimization and
  Storage Sense APIs) only through a reviewed `optimize` task.
- **Never write to the registry outside a reviewed feature.** The reviewed writes are the
  StartupApproved values (`startup enable|disable`, `repair` of broken startup entries) and
  the user `Path` value (`repair` of missing, duplicate and empty entries after a `.reg`
  backup; `remove` dropping the installer's entry; the install script adding it). The
  machine `Path` is never written. No "registry cleaning".
- **Never change scheduled tasks outside the reviewed write.** The only Task Scheduler write is
  a task's Enabled flag through `IRegisteredTask.Enabled` (`startup enable|disable`, `repair` of
  broken sign-in tasks), reversed by setting it back, with the previous flag in history. Tasks are
  never created, edited, run, stopped or deleted; other accounts' tasks need elevation.
- **Elevation is per operation.** `oow` never requires administrator rights as a whole.
  Admin-only targets are skipped with an explanation when not elevated. Verification and
  tests must never block on a UAC prompt; use the sandbox (elevation is simulated there).
- **Non-interactive deletion requires `--yes`** (exit 4 otherwise). `--dry-run` must stay
  zero-write for the filesystem it reports on.
- **A refusal must name its cause and the next step** (e.g. "requires administrator: run oow
  from an elevated terminal"). Never collapse distinct causes into a generic error.
- **Cancellation is safe.** Ctrl+C during a scan deletes nothing; during execution it stops
  after the current item and still reports and records the partial outcome.
- **Preserve operation history and logs.** Do not remove or weaken `history.jsonl` writes,
  `OOW_NO_OPLOG` semantics, or log-file diagnostics.
- **PRs touching destructive sinks need line-by-line review**: `RemoveVerified`, the executor,
  the guard, root resolution, rule roots, leftover matching, purge discovery. Audit every
  branch for matcher breadth, protected-path coverage and preserved confirmation. Treat AI
  or specialist review output as a claim to verify, never as approval.

## Working Rules

- Check `Guard.Check` / `ValidateRoot` behavior before adding any cleanup behavior; add guard
  tests for every new protected or exempt location.
- **A new cleanup target needs evidence and an explicit non-target list.** State bytes actually
  reclaimable on a real install, name sibling folders excluded as user data (profiles,
  settings, sessions, databases, downloads), and prove protection covers every reachable
  path. "It looks like a cache" is not evidence; an opaque index or database cannot prove a
  folder is unreferenced. Add seed files for the target *and* its non-targets to
  `sandbox.Seed`, and tests asserting both.
- **Classify by recovery contract, not by folder name or download cost.** Default-on: pure
  caches the owner regenerates transparently (HTTP/code/GPU caches, shader caches, package
  manager download caches such as npm `_cacache`, pip, yarn, NuGet `v3-cache`, Go build
  cache). Opt-in: large but regenerable state with a visible cost (JetBrains caches and
  indexes, Gradle caches, Cargo registry archives, Go module cache via owner command). Never:
  directly consumed or mixed-state stores (`~\.m2\repository`, `~\.nuget\packages`,
  pnpm store, Cargo `registry\src` and `git`, `%LOCALAPPDATA%\Pub\Cache`, `ms-playwright`,
  Deno dir, model and dataset caches such as `~\.cache\huggingface`, toolchains like
  `~\.rustup`, `~\.cargo\bin`).
- **Browsers**: clean only `Cache`, `Code Cache`, `GPUCache` and browser-level shader caches
  inside real profiles (marker file present). Never cookies, `Login Data`, `Web Data`,
  `History`, `Bookmarks`, `Local State`, extensions, Service Worker storage, IndexedDB or Local
  Storage. Skip the browser while it runs (including background modes).
- **Apps**: skip an app's cache while the app runs (`AppProcesses`). Electron/WebView2 apps:
  only `Cache`, `Code Cache`, `GPUCache` (and documented equivalents), never `Local Storage`,
  `IndexedDB`, `Session Storage`, `databases`, `blob_storage` or settings.
- **AI tools stay conservative**: Claude, Codex, Cursor, Copilot, Ollama, LM Studio and similar
  keep config, credentials, sessions, models and active versions; only exact, documented
  caches are eligible.
- **Uninstall and leftovers**: run the app's own uninstaller (MSI via Windows Installer,
  registered `UninstallString`/`QuietUninstallString`, AppX via package APIs, winget when it
  owns the app), wait, then verify the app is gone before looking for leftovers. Leftovers need
  multiple exact signals (uninstall entry, install location, package family name, exact
  folder names under known data roots) and carry a confidence level; low-confidence items are
  shown for review, never auto-selected. Never match on generic words or short names.
- **Purge**: a purge target is never a project container; protect Git-tracked content,
  nested repositories and deployment keys; artifacts with activity in the last 7 days are
  unselected by default; scan only configured roots. Preselected artifacts are deleted
  permanently (rebuildable by definition; dependency trees are too large for the Recycle Bin),
  per file through `RemoveVerified` with `safety.PurposePurge` scoped to the artifact folder;
  artifacts the user adds from review go to the Recycle Bin as a whole folder
  (`RecycleVerified`, same purpose), and without a Recycle Bin they are kept, never deleted.
  Both re-check Git and the folder right before acting. If Git cannot answer, keep the folder.
- **Installers**: identify installer packages by type and evidence (signature, MSI/MSIX
  metadata, product names), never by extension alone; route deletion to the Recycle Bin.
- **Long scans** need cancellation checkpoints in inner loops and bounded time; a timed-out or
  failed scan never feeds partial results into a deletion step, and reports itself as partial.
- **No size caches that can go stale.** Measure every time unless invalidation is exact.
- **Do not clean tiny UI state** just because it is regenerable (icon cache, jump lists, tiny
  thumbnails) unless the value is real and tested; visible glitches cost more trust than they
  save in megabytes.
- Keep code `gofmt`-clean; keep comments for *why*, not *what*.
- **Judge duplication by body and purpose, not by name.** Before declaring a symbol dead, grep
  `cmd`, `internal`, `scripts` and tests; tests alone are not production callers.
- Do not add AI attribution trailers (`Co-Authored-By` lines for AI tools) to commits or PRs.

## Hotspot Ownership

Keep edits narrow and run the listed tests when touching each area.

- `internal/safety/guard.go`: protection policy. Run `go test ./internal/safety` and its fuzz
  targets for at least 30s. Every new location needs positive and negative tests.
- `internal/filesystem/remove_windows.go`: the deletion sink. Run `go test ./internal/filesystem`
  (junction-swap, locked, read-only, changed, permission-denied, fence cases must stay).
- `internal/cleanup/scan.go`, `execute.go`, `rules*.go`: run `go test ./internal/cleanup` and
  `go test ./internal/cli`. Rule additions also update `sandbox.Seed`.
- `internal/cli/clean.go`: output rhythm is title, scan, grouped report, divider, totals, then
  prompt; re-run `oow --sandbox <dir> clean` (text and `--json`) and read the whole output
  after any change, not just the reported line.
- `internal/ui/*`: run `go test ./internal/ui`; check `NO_COLOR` and narrow terminals.
- `internal/config`, `internal/history`: schema changes need migration and tests.
- `internal/leftovers/find.go` and `internal/apps/app.go` (normalization, generic names): a
  matcher change can widen what is offered. Run `go test ./internal/leftovers ./internal/apps
  ./internal/cli` and add a sandbox case (`sandbox/apps.go`) for every new evidence or claim type.
- `internal/uninstall`: never add a code path that deletes program files directly; every
  removal goes through the app's own mechanism and `Checker` verification.
- `.github/workflows/ci.yml`, `scripts/ci/e2e-real.ps1`: keep canaries covering user content,
  app data, ProgramData and junctions.

## Verification

- Go changes: `gofmt -l .` (empty), `go vet ./...`, `go test ./...`.
- Cleanup behavior: verify with `--dry-run`, then in the sandbox
  (`oow --sandbox <dir> clean`), never first on a real system.
- **Never run tests or manual repros against the developer's real system.** Tests create files
  only through `internal/testutil` under `.sandbox/` with the deletion fence set by
  `testutil.Main`. Real-system reads are gated by `OOW_TEST_REAL_SYSTEM=1`. Real cleanup runs
  only on CI's disposable VMs.
- Every guard test must be observed failing against the unfixed code and passing after.
- Read the test runner's own summary and exit status; do not judge a run by a truncated
  `tail`/`head` of its output.
- **A `cancelled` CI run is not a passing one.** Verify the run for the commit that contains
  the change (`gh run list --json headSha,status,conclusion`).
- Documentation-only changes: check links and commands.

## GitHub Operations

- Re-read the live issue or PR (title, body, comments, labels, state) before replying or
  closing.
- Check for an open PR before fixing an issue yourself; prefer reviewing and merging a
  contributor's PR over landing an equivalent fix.
- When closing a fixed issue, say how to get the fix (next release or a source build) only when
  that path is confirmed, and invite reopening if the problem persists.
- For unreproducible reports, ask for `oow version --json` and the relevant command's `--json`
  output or `--debug` log, never for files that may contain private paths in public.

## Release

- **Never rewrite history reachable from a published release tag.** Tag tarball checksums
  change with rewritten commits and break every package manifest pinned to them.
- Releases are tag-driven (`v*`). Each release publishes `oow.exe` per architecture with
  SHA-256 checksums and build-provenance attestations; installers (PowerShell script, winget,
  Scoop, Chocolatey) verify checksums and fail closed on mismatch, never falling back to a
  less verified path.
- Confirm with the maintainer which distribution channels a release touches; never infer it.
- Version and commit are injected with `-ldflags -X .../internal/buildinfo.Version=...`.
