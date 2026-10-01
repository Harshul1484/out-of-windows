# out-of-windows (`oow`)

**A Windows-native system maintenance tool for the terminal.** One small binary to clean
temporary files and caches, uninstall apps completely, analyze disk usage, remove old
installers and developer build artifacts, and watch your system live.

> **Safety first.** `oow` deletes files, so safety is part of the architecture, not an
> afterthought. Every destructive command previews first (`--dry-run`), explains *what* it
> removes and *why it is safe*, asks for confirmation, re-verifies every item at the moment
> of deletion, and records what it did. System folders, your documents, and anything you
> whitelist are protected by a dedicated safety layer that is tested against attack-style
> scenarios (junction swaps, path tricks, files changed mid-run).

Inspired by the workflow of [Mole](https://github.com/tw93/mole) for macOS, rebuilt from
scratch for Windows internals: Known Folders, NTFS reparse points, Windows Installer, AppX,
winget, and Windows-specific caches.

```text
╭──────────────────────────────────────────────────────────────────────────╮
│  OUT OF WINDOWS  oow 0.1.0                                    Windows 11 │
│                                                                          │
│  ● CPU        ███░░░░░░░░░░░░░░░  17%                                    │
│  ● MEMORY     ██████░░░░░░░░░░░░  11.4 GB / 31.8 GB                      │
│  ● DISK C:    █████████████░░░░░  742 GB / 953 GB                        │
│  ────────────────────────────────────────────────────────────────────    │
│  CLEAN                                                                   │
│  › ① Deep Clean                                                          │
│    ② Uninstall Apps                                                      │
│    ③ Remove Installers                                                   │
│  ...                                                                     │
╰──────────────────────────────────────────────────────────────────────────╯
```

## Status

`oow` is being built in phases ([roadmap](docs/ROADMAP.md)). Commands not yet available
say so and exit with code 3.

| Command | What it does | Status |
|---|---|---|
| `oow` | Interactive home screen with live CPU, memory and disk | ✅ |
| `oow clean` | Temp files, caches and logs that are safe to delete | ✅ initial targets |
| `oow config` | Settings, whitelist, list of protected locations | ✅ |
| `oow history` | What previous runs removed | ✅ |
| `oow uninstall` / `leftovers` | Complete uninstall + orphaned app data | Phase 3 |
| `oow analyze` | Interactive disk explorer, large files | Phase 4 |
| `oow status` / `processes` | Live CPU/GPU/RAM/disk/network dashboard | Phase 5 |
| `oow optimize` / `doctor` / `startup` | Bounded maintenance, diagnostics, startup apps | Phase 6 |
| `oow purge` / `installer` | Developer artifacts, unused installers | Phase 7 |
| `oow update` / `remove` | Self-update and self-removal | Phase 8 |

## Install

Packaged releases (GitHub Releases, winget, Scoop, Chocolatey and a PowerShell installer)
are planned once the name is final. Until then, build from source with Go 1.26+:

```powershell
git clone https://github.com/Harshul1484/out-of-windows
cd out-of-windows
go build -o oow.exe ./cmd/oow
.\oow.exe --help
```

Works from PowerShell, Command Prompt and Windows Terminal. Supported: Windows 10 1809+
and Windows 11 (x64); Windows Server 2022/2025 are tested in CI.

## Usage

```powershell
oow                      # interactive home screen
oow clean --dry-run      # see what would be removed, change nothing
oow clean                # choose targets, confirm, clean
oow clean --list         # every cleanup target with what/why/after explanations
oow clean --rule temp.user --yes      # non-interactive, one target
oow clean --dry-run --json            # machine-readable preview
oow config whitelist add "D:\keep-this" windows.directx-shader-cache
oow config protected     # every location the safety layer protects
oow history              # what was removed and when
```

Global flags: `--json` (stable machine output, see [docs/JSON.md](docs/JSON.md)),
`--no-color` (or set `NO_COLOR`), `--debug` (debug log to stderr).

### What `oow clean` removes today

| Target | Location | Rule |
|---|---|---|
| User temporary files | `%LOCALAPPDATA%\Temp` | created *and* modified > 24 h ago, not in use |
| Windows temporary files | `%WINDIR%\Temp` | same; needs an elevated terminal |
| DirectX shader cache | `%LOCALAPPDATA%\D3DSCache` | rebuilt automatically |
| Windows error reports | `%LOCALAPPDATA%\Microsoft\Windows\WER\Report{Archive,Queue}` | diagnostic copies |

Browser, application, Windows Update, Delivery Optimization, thumbnail caches, Recycle Bin
and uninstalled-app leftovers arrive in Phases 2–3.

## How it keeps your data safe

`oow` follows **Discover → Explain → Confirm → Act → Verify**. In short:

- **Narrow rules, never wildcards.** Each cleanup target is a reviewed rule rooted at a
  specific folder resolved through Windows Known Folder APIs (not environment variables),
  with an explanation of what it removes, why that is safe, and what happens afterwards.
- **A safety guard decides, not the rules.** Drive roots, `Windows`, `Program Files`,
  `ProgramData`, profile roots, your Documents/Desktop/Pictures/OneDrive, the tool's own
  data, and everything you whitelist are refused, including through `..`, `\\?\`,
  case, trailing-dot and 8.3 short-name tricks.
- **Verified deletion.** Each item is opened by handle without following links, compared
  with what the scan saw (type, size, timestamps), its OS-resolved final path is re-checked,
  and only then deleted through that same handle. A folder swapped for a junction, a file
  rewritten after the scan, or a file in use is skipped and reported, never deleted.
- **Links are never followed.** Junctions and symlinks are reported as skipped.
- **No admin unless needed.** Targets that need administrator rights are skipped with an
  explanation unless you run from an elevated terminal.
- **Dry run everywhere, history always.** Every destructive command supports `--dry-run`;
  every real run is recorded (`oow history`, disable with `OOW_NO_OPLOG=1`).
- **Honest numbers.** Reports show the bytes removed *and* the measured change in free
  space. No "PC health scores", no registry cleaning, no RAM "boosting", no telemetry.

The full model is in [docs/SAFETY.md](docs/SAFETY.md).

## Architecture

Go, single static binary, native Win32 APIs via `golang.org/x/sys/windows` (no shelling out
to localized command output). The TUI uses Bubble Tea and Lip Gloss. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

```text
cmd/oow            entry point
internal/cli       commands, JSON output, exit codes
internal/cleanup   declarative rules, scanner, verified executor
internal/safety    path normalization, protected locations, guard
internal/filesystem walking without following links, handle-verified deletion, fence
internal/config    settings and whitelist      internal/history  operation log
internal/system    version, elevation, CPU/memory/disk, processes
internal/ui        styles, prompts, checklist and home screens
internal/sandbox   simulated Windows layout for safe end-to-end testing
```

## Development

```powershell
go test ./...                                   # all tests, fully sandboxed
$env:OOW_TEST_REAL_SYSTEM = "1"; go test ./...  # also read-only real-system tests
go run ./cmd/oow sandbox init .sandbox\demo     # build a simulated Windows layout
go run ./cmd/oow --sandbox .sandbox\demo clean  # try any command safely against it
```

Tests never touch your real system: fixtures live under the git-ignored `.sandbox/`, and a
process-wide **deletion fence** below all policy code refuses any deletion outside it. CI runs
the suite on disposable Windows Server 2025 and 2022 VMs, plus a real end-to-end cleanup
with canary files. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
