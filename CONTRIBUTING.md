# Contributing

Thanks for helping. Because `oow` deletes files, the bar for anything that can delete is
high: read [docs/SAFETY.md](docs/SAFETY.md) before changing `internal/safety`,
`internal/filesystem` or cleanup rules.

## Setup

Go 1.26+ on Windows 10/11. (Cross-compiling with `GOOS=windows` works from other systems,
but the tests must run on Windows.)

```powershell
go build ./...
go test ./...
```

## Never test against your real system

- Tests create files only through `internal/testutil`, under the git-ignored `.sandbox/`
  folder in the repository. Every test package that touches files must have
  `func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }`, which sets the process-wide
  **deletion fence**; fixture helpers refuse to run without it.
- Tests that read real system state (Known Folders, memory, processes) call
  `testutil.SkipUnlessRealSystem` and only run with `OOW_TEST_REAL_SYSTEM=1` (CI sets it).
- To try the CLI by hand, use the simulated layout:

  ```powershell
  go run ./cmd/oow sandbox init .sandbox\demo
  go run ./cmd/oow --sandbox .sandbox\demo clean --dry-run
  go run ./cmd/oow --sandbox .sandbox\demo clean
  ```

  In sandbox mode all locations, config, history and logs live in the sandbox, and deletion is
  fenced to it.
- Real cleanup is tested only in CI on disposable VMs (`scripts/ci/e2e-real.ps1`, which
  refuses to run outside CI).

## Checklist for changes

- `gofmt`, `go vet ./...`, `go test ./...` pass.
- New cleanup targets: explanation fields filled in, sandbox seed + tests added, no broad
  roots, no `{Temp}`.
- New destructive features: go through `cleanup`/`filesystem.RemoveVerified` (or a reviewed
  equivalent), support `--dry-run`, `--json`, require confirmation, record history.
- JSON: only add fields within a schema version; update [docs/JSON.md](docs/JSON.md).
- No telemetry, no performance scores, no registry cleaning.

## Commit messages

Short imperative summary line, body explaining why when it is not obvious.
