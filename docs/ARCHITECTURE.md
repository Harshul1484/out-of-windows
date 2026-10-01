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
  filesystem/            Walk (no link following), RemoveVerified, FinalPath, fence
  config/                config.json (whitelist, preferences), directories
  history/               append-only history.jsonl
  logging/               slog → %LOCALAPPDATA%\oow\logs\oow.log (+stderr with --debug)
  system/                OS version, elevation, CPU/memory/disk, processes
  ui/                    styles (NO_COLOR aware), formatting, prompts, spinner,
                         Bubble Tea checklist and home screen
  sandbox/               simulated Windows layout + seeding for e2e testing
  testutil/              sandbox/fence setup for tests, fixtures, file locking
docs/                    this file, SAFETY.md, JSON.md, ROADMAP.md
scripts/ci/              CI-only end-to-end scripts
```

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
