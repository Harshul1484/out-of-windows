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

## Phase 2 — Cleanup engine ✅

- [x] `{profile}` roots that expand only to real profiles (marker file), never through links
- [x] Browser caches: Chrome (+Beta/Canary), Chromium, Edge, Brave, Vivaldi, Opera, Firefox
- [x] Application caches: Discord, Slack, Teams, VS Code, Cursor, Spotify, Steam, Epic,
      Battle.net, Adobe, JetBrains (opt-in)
- [x] Windows caches: GPU vendor shader caches, Temporary Internet Files, thumbnails (opt-in),
      system error reports, crash dumps and minidumps (opt-in)
- [x] Developer caches by recovery contract: npm, Yarn, pip, NuGet HTTP, Go build, Cargo
      archives, Electron, node-gyp, Composer, TypeScript, golangci-lint, Gradle (opt-in)
- [x] Recycle Bin through the Shell API (opt-in; simulated in sandbox mode)
- [x] Skip caches of running applications
- [x] Sensitive locations and file types never cleaned
- [x] Elevation on demand for admin-only targets (separate elevated window)
- [x] One-screen report: empty and not-installed targets collapsed
- Deferred to Phase 6 (`optimize`, owner APIs only): Delivery Optimization, Windows Update
  download cache, component store

## Phase 3 — Uninstaller ✅

- [x] App discovery: Uninstall registry keys (64-bit, 32-bit, per-user; MSI and EXE),
      Microsoft Store / MSIX, Scoop, Chocolatey; package manager detection (winget, scoop, choco)
- [x] Searchable, sortable, multi-select app picker; `--list`, `--json`, name or `--id` selection
- [x] Native uninstall: `msiexec /x`, registered (quiet) uninstall commands, `Remove-AppxPackage`,
      `scoop uninstall`, `choco uninstall`; UAC only when the uninstaller needs it
- [x] Wait for hand-off uninstallers, verify the app is gone (fail closed), detect cancel and
      restart-required codes
- [x] Leftover detection with evidence and confidence (install folder, exact names, publisher
      folders, executables) and claims (installed apps, processes, services, startup entries)
- [x] `oow leftovers`: evidence from oow history, broken uninstall entries and Windows usage traces
- [x] Leftovers to the Recycle Bin, identity-verified; admin-only locations via an elevated window
- winget is detected but not required: for apps it can see, winget runs the same registered
  uninstaller that oow runs directly

## Phase 4 — Disk analyzer ✅

- [x] Parallel, cancellable scanner with live progress; folder tree with sizes, file counts
      and newest change; links never followed; unreadable folders counted, not fatal
- [x] Interactive explorer: drill down, back, sort (size, name, files, modified), filter,
      search with next match, largest-files view, reveal in File Explorer, rescan
- [x] `--large`, `--min-size`, `--top`; drive picker; `--json` with `--depth`
- [x] Recycle Bin deletion of marked items with in-screen confirmation, guarded as explicit
      user selection (never system, protected or sensitive locations), recorded in history
- Not planned: raw MFT reading (needs administrator rights and raw volume access); hard links
  are counted per link

## Phase 5 — Monitoring ✅

- [x] `oow status`: live dashboard with CPU (total, per core, frequency), memory (used, commit,
      cache), GPU (utilization and memory from performance counters; NVIDIA temperature via
      `nvidia-smi`), disk activity and throughput, volumes, network rates and session totals,
      top processes; `--json` snapshot and `--json --watch` NDJSON stream
- [x] `oow processes`: CPU, private memory, I/O and threads per process, sort and filter
- [x] Native sources only (kernel process table, PDH English counter names, Known APIs);
      unavailable metrics are reported as unavailable, never estimated

## Phase 6 — Optimize, doctor, startup

- [ ] `oow optimize`: bounded, explained maintenance (DNS flush, caches, pending reboot…)
- [ ] `oow doctor`: diagnostics without automatic fixes
- [ ] `oow startup`: Run keys, Startup folders, scheduled tasks; safe disable

## Phase 7 — Developer tools ✅

- [x] `oow purge`: node_modules, .next, .nuxt, .svelte-kit, .turbo, .parcel-cache, .angular,
      dist/build/out, target (Cargo, Maven), Gradle build and .gradle, bin/obj (.NET), Python
      caches, .tox and virtual environments, .dart_tool, CMake build trees; only next to their
      project's marker file, in configured or usual project folders
- [x] Purge safety: Git-tracked content (literal pathspecs, fail closed without Git), nested
      repositories, links, cloud-only and sensitive files kept; recent activity and ambiguous
      folders unselected; permanent, per-file verified deletion under a dedicated guard purpose
- [x] `oow purge --paths`, `oow config purge add|remove`
- [x] `oow installer`: MSI/MSP, MSIX/APPX, setup programs (Inno Setup, NSIS, WiX Burn,
      InstallShield, version resources), ZIP and ISO identified by content in Downloads, Desktop
      and Documents; exact installed-app matching; Recycle Bin
- Not planned: purging folders that contain links (pnpm, npm workspaces) — remove those with
  the package manager; ISO images without an ISO 9660 setup file (UDF-only media) are review-only

## Phase 8 — Distribution

- [ ] Release builds with version metadata and signing
- [ ] PowerShell installer, winget, Scoop, Chocolatey
- [ ] `oow update`, `oow remove`
