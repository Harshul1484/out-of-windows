# Roadmap

Each phase ships with tests and is verified in the sandbox and on CI's Windows Server VMs
before the next begins.

## Phase 1 — Foundation ✅

- [x] CLI framework (cobra), help, `--version`, grouped commands, exit codes
- [x] Configuration (`config.json`), whitelist of paths and targets
- [x] Logging (file, `--debug`), history (`history.jsonl`, `oow history`)
- [x] Safety layer: normalization, protected locations, guard, root validation
- [x] Verified, handle-based deletion; links never followed; deletion fence
- [x] `--dry-run`, `--yes`, `--json` with documented schemas
- [x] Interactive home screen (live CPU/memory/disk), checklist, confirmations
- [x] Windows version and elevation detection
- [x] `oow clean` with four extremely safe targets
- [x] Sandbox mode and fixture-based test suite; CI on Windows Server 2025/2022

## Phase 2 — Cleanup engine

- [ ] Browser caches: Chrome, Edge, Brave, Opera, Vivaldi, Firefox (all profiles; cache only)
- [ ] Application caches: Discord, Slack, Spotify, Teams, VS Code, JetBrains, Adobe, launchers…
- [ ] Windows caches: thumbnails, icon cache, Delivery Optimization, Windows Update downloads,
      GPU vendor shader caches, crash dumps, logs
- [ ] Developer caches: npm, yarn, pnpm, pip, NuGet, Go build cache, Gradle, Maven…
- [ ] Recycle Bin (Shell API)
- [ ] Skip caches of running applications
- [ ] Elevation on demand for admin-only targets

## Phase 3 — Uninstaller

- [ ] App discovery: registry (HKLM/HKCU, 32/64-bit), MSI, AppX/MSIX, winget, Scoop, Chocolatey
- [ ] Native uninstaller execution, wait, verify removal
- [ ] Leftover detection with multiple signals and confidence levels (`oow leftovers`)

## Phase 4 — Disk analyzer

- [ ] Parallel scanner with progress and cancellation
- [ ] Interactive explorer (sort, filter, search, drill-down), `--large`, `--min-size`
- [ ] Recycle Bin deletion of selected items

## Phase 5 — Monitoring

- [ ] `oow status`: CPU (per core), GPU (NVIDIA/AMD/Intel where available), memory, disk I/O,
      network, top processes; `--json`, `--watch`
- [ ] `oow processes`

## Phase 6 — Optimize, doctor, startup

- [ ] `oow optimize`: bounded, explained maintenance (DNS flush, caches, pending reboot…)
- [ ] `oow doctor`: diagnostics without automatic fixes
- [ ] `oow startup`: Run keys, Startup folders, scheduled tasks; safe disable

## Phase 7 — Developer tools

- [ ] `oow purge`: node_modules, target, build, .next, .venv… with project/Git safety rules
- [ ] `oow installer`: identifiable installer packages in Downloads/Desktop/…

## Phase 8 — Distribution

- [ ] Release builds with version metadata and signing
- [ ] PowerShell installer, winget, Scoop, Chocolatey
- [ ] `oow update`, `oow remove`
