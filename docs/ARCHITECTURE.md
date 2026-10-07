# Architecture

## Goals

- One fast, self-contained `oow.exe` with no runtime dependencies.
- Native Windows APIs (`golang.org/x/sys/windows`, registry, Known Folders, Toolhelp, file
  handles) instead of parsing localized command output, so it works on any Windows language.
- Safety enforced by dedicated layers that every destructive feature goes through.
- Platform-specific code isolated in `*_windows.go` files; policy code is pure Go and
  testable in isolation.

## Layout

```text
cmd/oow/                 main: calls cli.Main()
internal/
  buildinfo/             name, version (ldflags), so the binary can be renamed
  cli/                   cobra commands, JSON schemas, exit codes, home dispatch
  cleanup/               Rule registry, Scan (read-only), Execute (verified)
  safety/                Normalize, Locations, Guard; DiscoverLocations (Known Folders)
  filesystem/            Walk (no link following), RemoveVerified, RecycleVerified,
                         FinalPath, fence
  apps/                  installed apps (registry, AppX, Scoop, Chocolatey), names
  uninstall/             uninstall plans, Runner (the app's own uninstaller), Checker
  leftovers/             evidence, claims, Find with confidence, Recycle
  analyzer/              parallel read-only disk scanner, largest files
  monitor/               read-only metrics (process table, per-core, PDH, GPU)
  startup/               Run/RunOnce and Startup folder entries, .lnk parser,
                         StartupApproved enable/disable
  envpath/               PATH analysis, .reg backup, compare-and-swap user PATH write
  doctor/                read-only probes and checks
  optimize/              bounded maintenance tasks through owner interfaces
  repair/                user PATH and startup fixes found by doctor
  purge/                 project artifact discovery (markers, Git), verified removal
  installer/             installer packages identified by content, Recycle
  selfupdate/            GitHub releases, SHA256SUMS, staged replacement
  install/               installer folder, package-manager detection, PATH entry
  elevation/             one elevated relaunch per operation (UAC), Shell execution
  config/                config.json (whitelist, preferences), directories
  history/               append-only history.jsonl
  logging/               slog → %LOCALAPPDATA%\oow\logs\oow.log (+stderr with --debug)
  system/                OS version, elevation, CPU/memory/disk, processes, Recycle Bin
  ui/                    styles (NO_COLOR aware), formatting, prompts, spinner,
                         Bubble Tea checklist, home screen, explorer, status dashboard
  sandbox/               simulated Windows layout + seeding for e2e testing
  testutil/              sandbox/fence setup for tests, fixtures, file locking
docs/                    this file, SAFETY.md, JSON.md, ROADMAP.md
scripts/                 install.ps1 / install.cmd; ci/ holds CI-only end-to-end scripts
packaging/               winget, Scoop and Chocolatey templates
```

## Where changes happen

Every change to the machine goes through one of these sinks; review them line by line.

| Change | Sink | Guard purpose | Used by |
|---|---|---|---|
| Permanent file and folder deletion | `filesystem.RemoveVerified` | `PurposeCleanup` (rule scope), `PurposePurge` (artifact scope) | `clean`, `purge` (preselected), `update` and `remove` (own executable, exact path via `install.RemoveOwnFile`), sandbox simulations |
| Move to the Recycle Bin | `filesystem.RecycleVerified` | `PurposeUserSelected`, `PurposeLeftover`, `PurposePurge`, `PurposeSelfRemove` | `analyze`, `installer`, `leftovers`, `purge` (from review), `remove` |
| Empty the Recycle Bin | `system.RecycleBin` (`SHEmptyRecycleBinW`) | — | `clean` (Recycle Bin rule) |
| App uninstall | `uninstall.Runner` (the app's own uninstaller) | — | `uninstall` |
| StartupApproved values | `startup.SetEnabled` → `Store.SetApproval` (read back) | — | `startup`, `repair` |
| User `Path` value | `envpath.Store.WriteUser` (compare-and-swap) | — | `repair`, `remove` |
| Maintenance tasks | `optimize.Runner` (DNS API, owner cmdlets, DISM for the component store) | — | `optimize` |
| Executable replacement | `selfupdate` (rename aside, never overwrite) | — | `update` |

The suggested `cmd/<command>` layout is realised as `internal/cli/<command>.go`: Go's
convention is one `cmd/<binary>` per executable, and keeping commands internal prevents
them from becoming a public API.

## Data flow of `oow clean`

```text
cli.runClean
  ├─ pickRules(--rule)                 cleanup.BuiltinRules()
  ├─ cleanup.Scan(ctx, env, rules)     concurrent per rule, read-only
  │    resolveRoot → guard.ValidateRoot → filesystem.Walk → guard.Check per item
  ├─ printScan / JSON                  explain
  ├─ ui.RunChecklist + Confirm         choose and confirm (or --yes)
  ├─ cleanup.Execute(ctx, env, chosen) per item: filesystem.RemoveVerified(path, fingerprint,
  │                                    guard.Check on final path)
  ├─ history.Append                    record
  └─ printOutcome / JSON               verify: removed bytes + measured free-space delta
```

`cleanup.Env` carries everything machine-specific (locations, guard, elevation, whitelist,
running processes, remover), so the engine runs unchanged against the real system, the
sandbox, or a test fixture.

## Adding a cleanup target

Add a `&Rule{...}` to a rules file in `internal/cleanup` (one file per category as the set
grows). Required: a stable dotted `ID`, `Name`, `Category`, `Roots` using location tokens,
and the `What` / `WhySafe` / `Impact` explanations. Optional: `MinAge`, `Include` /
`Exclude` name patterns, `MaxDepth`, `RequiresAdmin`, `DefaultSelected`, `AppProcesses`
(skip while the app runs), `DetectPaths` (only offer when the app is installed).

`TestBuiltinRulesAreValid` and `TestBuiltinRootsAreValidInSimulatedLayout` check every
rule. Add sample files for the new target to `sandbox.Seed` and assert on them in the
cleanup tests. If the target lives in a system tree it also needs a guard exemption in
`safety.NewGuard`, with a review that updates `docs/SAFETY.md`.

## Location tokens

`{Windows}`, `{WindowsTemp}`, `{ProgramData}`, `{UserProfile}`, `{RoamingAppData}`,
`{LocalAppData}`, `{LocalLow}`, `{Temp}` (discouraged). Unknown or unavailable tokens are an
error, never an empty string.

## Concurrency and cancellation

Rules scan concurrently (bounded by CPU count). Every walk checks its context between
entries; Ctrl+C cancels the context, the scan returns partial results marked cancelled, and
nothing is deleted. During execution Ctrl+C stops after the current item; the partial
outcome is still reported and recorded. A second Ctrl+C terminates immediately.

## Output

Text output is written for humans (symbols always paired with words, color optional,
width-aware). `--json` switches every command to stable JSON on stdout with no decoration,
prompts, spinners or color; errors become `{"error": ..., "exit_code": ...}`. Logs never go
to stdout.

## Storage

| What | Where | Override |
|---|---|---|
| config.json | `%APPDATA%\oow\` | `OOW_CONFIG_DIR` |
| history.jsonl, logs\ | `%LOCALAPPDATA%\oow\` | `OOW_DATA_DIR` |
| everything, in sandbox mode | `<sandbox>\oow-data\` | `OOW_SANDBOX` / `--sandbox` |

These directories are themselves protected from cleanup.
