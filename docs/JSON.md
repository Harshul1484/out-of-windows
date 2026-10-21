# JSON output and exit codes

Every command accepts `--json`. In JSON mode `oow` writes exactly one JSON document to
stdout and nothing else: no colors, prompts, spinners or decoration. Logs go to the log
file (and stderr with `--debug`).

Each document has a `schema` field such as `oow.clean/v1`. Within a schema version fields
are only ever **added**; renaming, removing or changing the meaning of a field requires a new
version. Sizes are bytes, durations milliseconds, times RFC 3339.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success (including "nothing to clean") |
| 1 | error |
| 2 | usage error (unknown command, flag or target) |
| 3 | command not available in this build yet |
| 4 | refused: a destructive action needs `--yes` (or interactive confirmation) |
| 130 | cancelled with Ctrl+C |

On failure in JSON mode stdout contains `{"error": "<message>", "exit_code": <code>}`.

## `oow clean --json` — `oow.clean/v1`

```json
{
  "schema": "oow.clean/v1",
  "dry_run": true,
  "sandbox": false,
  "elevated": false,
  "rules": [
    {
      "id": "temp.user",
      "name": "User temporary files",
      "category": "temp",
      "status": "ready",
      "reason": "",
      "selected": true,
      "roots": ["C:\\Users\\me\\AppData\\Local\\Temp"],
      "files": 4212,
      "bytes": 1288490188,
      "kept_recent": 23,
      "skipped": 1,
      "skip_reasons": [{ "reason": "link or junction (not followed)", "count": 1 }],
      "items": [{ "path": "C:\\Users\\me\\AppData\\Local\\Temp\\setup.log", "size": 18000 }],
      "result": {
        "removed": 4100, "dirs_removed": 12, "reclaimed_bytes": 1200000000,
        "skipped": 112, "skip_reasons": [], "errors": 0
      }
    }
  ],
  "summary": {
    "reclaimable_files": 4344, "reclaimable_bytes": 1700000000,
    "selected_files": 4344, "selected_bytes": 1700000000,
    "executed": false, "removed": 0, "reclaimed_bytes": 0, "freed_on_disk_bytes": 0,
    "skipped": 0, "errors": 0, "cancelled": false, "scan_ms": 812, "clean_ms": 0
  }
}
```

- `status`: `ready` (has items), `empty` (nothing to clean), `skipped` (not scanned; see
  `reason`, e.g. whitelisted or requires administrator), `review` (could not be confirmed
  safe, left alone).
- `items` appears only with `--details`.
- `result` appears only for targets that were actually cleaned (not with `--dry-run`).
- `freed_on_disk_bytes` is the measured change in free space on the affected volumes.
  It can differ from `reclaimed_bytes` because of compression, hard links, or other
  programs writing at the same time.

Selection without a terminal: targets matched by `--rule`, otherwise targets selected by
default (`--all` selects every ready target). Real deletion requires `--yes`.

## `oow clean --list --json` — `oow.rules/v1`

`{"schema": "oow.rules/v1", "rules": [{"id", "name", "category", "roots", "min_age_hours",
"what", "why_safe", "impact", "requires_admin", "default_selected", "whitelisted"}]}`

## `oow history --json` — `oow.history/v1`

```json
{
  "schema": "oow.history/v1",
  "records": [
    {
      "time": "2026-10-01T14:32:05+05:30", "command": "clean",
      "removed": 147, "reclaimed_bytes": 9040000000, "skipped": 13, "errors": 0,
      "duration_ms": 5120,
      "targets": [{ "id": "temp.user", "name": "User temporary files", "removed": 120,
                    "reclaimed_bytes": 800000000, "skipped": 13, "errors": 0,
                    "skip_reasons": [{ "reason": "in use by another program", "count": 13 }] }]
    }
  ]
}
```

Newest first. `sandbox: true` marks runs made in sandbox mode; `cancelled: true` marks runs
stopped with Ctrl+C. `recycled_bytes` is what was moved to the Recycle Bin (as opposed to
`reclaimed_bytes`, which was deleted). Uninstall records carry
`apps: [{"name", "version", "publisher", "install_location", "exes"}]`, which later leftover
scans use as evidence.

## `oow uninstall --list --json` — `oow.apps/v1`

```json
{
  "schema": "oow.apps/v1",
  "apps": [
    {
      "id": "reg:hklm64:ContosoStudio", "name": "Contoso Studio", "version": "4.2.0",
      "publisher": "Contoso Ltd.", "source": "exe", "scope": "machine",
      "install_location": "C:\\Program Files\\Contoso\\Studio", "size_bytes": 7900000,
      "uninstall_string": "\"C:\\Program Files\\Contoso\\Studio\\uninstall.exe\" /S",
      "registry_key": "HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\ContosoStudio",
      "problems": []
    }
  ],
  "package_managers": { "winget": "C:\\...\\winget.exe" },
  "warnings": []
}
```

- `source`: `msi`, `exe`, `appx`, `scoop`, `choco`. `scope`: `machine` or `user`.
- `id` is stable for a given installation and is what `--id` accepts. Prefixes: `reg:hklm64:`,
  `reg:hklm32:`, `reg:hkcu:` (followed by the Uninstall key name), `appx:<PackageFullName>`,
  `scoop:<scope>:<name>`, `choco:<id>`.
- `problems`: why the app cannot be uninstalled normally; the first entry
  `its uninstaller is missing` marks a broken entry.
- `size_bytes` is the size the installer registered (0 when unknown).

## `oow uninstall --json` — `oow.uninstall/v1`

`{"schema", "dry_run", "sandbox", "results": [...]}`, one result per app:

| Field | Meaning |
|---|---|
| `app` | the app, as in `oow.apps/v1` |
| `plan` | `{"method", "exe", "args", "command", "elevate", "quiet", "notes"}`: exactly what runs |
| `outcome` | `{"started", "exit_code", "removed", "restart_required", "message"}` (absent in dry runs) |
| `leftovers` | `{"candidates": [...], "kept": [...]}` as in `oow.leftovers/v1` |
| `recycled` | `{"recycled": [...], "bytes", "skipped": [{"path", "reason"}], "errors"}` |
| `error` | why this app was not uninstalled |

In a dry run `leftovers` lists folders related to the app *now*; after a real uninstall it lists
what was actually left. With `--yes`, only `high` confidence leftovers are moved.

## `oow leftovers --json` — `oow.leftovers/v1`

```json
{
  "schema": "oow.leftovers/v1", "dry_run": true, "sandbox": false, "elevated": false,
  "evidence": [
    { "name": "Old Editor", "publisher": "Proseware", "install_location": "C:\\Program Files\\OldEditor",
      "exes": ["oldeditor.exe"], "source": "usage-trace" }
  ],
  "result": {
    "candidates": [
      { "path": "C:\\Program Files\\OldEditor", "app": "Old Editor", "source": "usage-trace",
        "location": "program files", "confidence": "medium",
        "reasons": ["Old Editor ran from this folder; its program file is gone"],
        "bytes": 1500000, "files": 1, "newest_change": "2026-03-16T10:00:00Z", "needs_admin": true }
    ],
    "kept": [ { "path": "...", "app": "Wingtip Toys", "reason": "still used by Wingtip Toys" } ]
  }
}
```

After a real run (`--yes`) a `recycled` object is added, as in `oow.uninstall/v1`, with
`recycled_shortcuts` (the broken shortcuts moved with their folders; `[]` when none) and
`removed_empty_folders` (Start Menu folders those shortcuts left empty, removed; `[]` when none).

- `source`: `uninstalled`, `history`, `broken-entry`, `usage-trace`, `shortcut`.
- `shortcut` evidence carries `shortcuts`: the shortcut files whose program (in `exes`) is gone.
  Its `install_location` is the exact folder the program lived in (its parent when that folder is
  `bin`, `x64` and the like); the shortcut's name is shown as `name` but never matched.
- `confidence`: `high` (registered install folder, or two or more independent signals) or
  `medium` (a single exact-name signal, and everything based only on usage traces or shortcuts).
- `needs_admin`: the folder is in Program Files or ProgramData and `oow` is not elevated.
- A candidate's `shortcuts` (present when there are any) are broken shortcuts whose program lived
  in it: `{"path", "target", "location", "removable", "note"}`. Only `removable` ones (the user's
  own Start Menu and Desktop) go to the Recycle Bin, together with the folder; `note` says why
  the others stay (the Start Menu and Desktop for all users are never changed).

## `oow analyze --json` — `oow.analyze/v1`

```json
{
  "schema": "oow.analyze/v1",
  "root": {
    "name": "C:\\", "path": "C:\\", "size": 742000000000, "files": 1203344, "percent_of_parent": 0,
    "children": [
      { "name": "Users", "path": "C:\\Users", "size": 286000000000, "files": 400000, "percent_of_parent": 38.5 },
      { "name": "Documents and Settings", "path": "C:\\Documents and Settings", "size": 0, "files": 0,
        "percent_of_parent": 0, "link": true },
      { "name": "System Volume Information", "path": "...", "size": 0, "files": 0, "percent_of_parent": 0,
        "error": "Access is denied." }
    ]
  },
  "disk_total": 1000000000000,
  "scan_status": "complete",
  "scan_errors": 0,
  "links": 1,
  "duration_ms": 41000,
  "largest": [ { "path": "C:\\Users\\me\\Videos\\recording.mp4", "size": 7194139648, "modified": "2026-09-30T18:02:11+05:30" } ]
}
```

- `children` nest `--depth` levels (default 1), largest first.
- `scan_status` is `partial` when some folders could not be read (`scan_errors`); sizes then
  cover only what was readable. `link: true` marks links and junctions, which are not followed.
- Sizes are logical file sizes; hard-linked files are counted once per link.
- `largest` holds up to `--top` files (default 50).

## `oow analyze --large --json` — `oow.large/v1`

`{"schema", "root", "min_size", "files": [{"path", "size", "modified"}], "scan_errors", "duration_ms"}`,
largest first, filtered by `--min-size`, at most `--top` entries.

## `oow status --json` — `oow.status/v1`

```json
{
  "schema": "oow.status/v1",
  "time": "2026-10-05T12:00:02Z",
  "cpu": { "percent": 35, "cores_percent": [80, 20, 20, 20], "frequency_mhz": 3400, "max_frequency_mhz": 3800 },
  "memory": { "total_bytes": 17179869184, "used_bytes": 10737418240, "available_bytes": 6442450944,
              "used_percent": 62.5, "commit_used_bytes": 15032385536, "commit_limit_bytes": 25769803776,
              "cached_bytes": 3221225472 },
  "disk": { "read_bytes_per_sec": 12582912, "write_bytes_per_sec": 3145728, "active_percent": 28,
            "volumes": [ { "root": "C:\\", "total_bytes": 1073741824000, "free_bytes": 268435456000 } ] },
  "network": { "recv_bytes_per_sec": 18874368, "sent_bytes_per_sec": 2097152,
               "interfaces": [ { "name": "Ethernet", "recv_bytes_per_sec": 18874368, "sent_bytes_per_sec": 2097152 } ] },
  "gpus": [ { "name": "NVIDIA GeForce RTX 3060", "utilization_percent": 39, "memory_used_bytes": 2147483648,
              "memory_total_bytes": 12884901888, "temperature_c": 61 } ],
  "processes": { "count": 214, "threads": 3120, "handles": 98000,
                 "top_by_cpu": [ { "pid": 1200, "ppid": 4, "name": "compiler.exe", "cpu_percent": 20,
                                   "memory_bytes": 943718400, "io_bytes_per_sec": 20971520, "threads": 12, "handles": 300 } ] },
  "uptime_seconds": 273600
}
```

- Rates are measured over `--interval` (default 1 s) between two readings.
- Process `cpu_percent` is a share of all logical processors (one busy core of eight is 12.5%),
  as in Task Manager. `memory_bytes` is the private working set.
- `active_percent` is `-1` and GPU `utilization_percent` / `temperature_c` are `-1` when
  Windows does not provide them; `gpus` may be empty.
- `--watch` prints one such object per line (NDJSON) every `--interval` until Ctrl+C or
  `--count` objects.

## `oow processes --json` — `oow.processes/v1`

`{"schema", "time", "sort", "total", "processes": [...]}` with process objects as in
`top_by_cpu` above, sorted by `--sort` (`cpu`, `memory`, `io`, `name`) and limited by `--top`.

## `oow update --json` — `oow.update/v1`

```json
{
  "schema": "oow.update/v1",
  "check_only": false, "dry_run": false, "sandbox": false,
  "repo": "Harshul1484/out-of-windows",
  "current_version": "1.2.0", "dev_build": false,
  "latest_version": "1.3.0", "update_available": true,
  "managed_by": null,
  "release": { "tag": "v1.3.0", "url": "https://github.com/Harshul1484/out-of-windows/releases/tag/v1.3.0",
               "published_at": "2026-10-01T12:00:00Z" },
  "asset": { "name": "oow-1.3.0-windows-amd64.exe", "size": 9830400, "sha256": "4f2a…" },
  "executable": "C:\\Users\\me\\AppData\\Local\\Programs\\oow\\oow.exe",
  "backup": "C:\\Users\\me\\AppData\\Local\\Programs\\oow\\oow.exe.old",
  "updated": true,
  "message": "The previous version stays as oow.exe.old until oow next starts."
}
```

- `dev_build`: a source build (`0.1.0-dev`) cannot order itself against releases; it never
  updates itself (exit 1) and `update_available` stays `false`.
- `managed_by`: `null`, or `{"manager", "package", "update_command", "remove_command"}` with
  `manager` one of `winget`, `scoop`, `chocolatey`. Such installs are not updated (exit 1);
  `--check` still reports.
- `release` is present once GitHub answered; `asset` and `backup` once an update was planned.
  `sha256` comes from the release's `SHA256SUMS`; a missing file, a missing entry or a
  mismatch stops the update (exit 1) with nothing replaced.
- `error` is added (and the exit code is non-zero) when the update did not happen; `message`
  always explains the outcome.

## `oow remove --json` — `oow.remove/v1`

```json
{
  "schema": "oow.remove/v1",
  "dry_run": false, "sandbox": false, "keep_data": false,
  "managed_by": null,
  "executed": true,
  "items": [
    { "kind": "executable", "path": "C:\\Users\\me\\AppData\\Local\\Programs\\oow\\oow.exe",
      "action": "remove", "status": "manual",
      "reason": "it is the running program, and Windows does not let a running program delete its own file", "bytes": 0 },
    { "kind": "install-dir", "path": "C:\\Users\\me\\AppData\\Local\\Programs\\oow",
      "action": "remove-if-empty", "status": "manual", "bytes": 0 },
    { "kind": "path-entry", "path": "C:\\Users\\me\\AppData\\Local\\Programs\\oow",
      "action": "remove-from-path", "status": "done", "bytes": 0 },
    { "kind": "config-dir", "path": "C:\\Users\\me\\AppData\\Roaming\\oow", "action": "recycle", "status": "done", "bytes": 412 },
    { "kind": "data-dir", "path": "C:\\Users\\me\\AppData\\Local\\oow", "action": "recycle", "status": "done", "bytes": 48213 }
  ],
  "manual_steps": [
    { "shell": "powershell", "command": "Remove-Item -LiteralPath '…\\oow.exe'; Remove-Item -LiteralPath '…\\Programs\\oow'" },
    { "shell": "cmd", "command": "del \"…\\oow.exe\" && rmdir \"…\\Programs\\oow\"" }
  ],
  "errors": 0,
  "message": "Almost done: run the command above to delete the program file."
}
```

- `kind`: `executable`, `install-dir`, `path-entry`, `config-dir`, `data-dir` (when settings
  and data share one folder, a single `data-dir` item covers both).
- `action`: `remove`, `remove-if-empty`, `remove-from-path`, `recycle` (to the Recycle Bin),
  or `keep` with a `reason` (outside the installer's folder, chosen with `OOW_CONFIG_DIR` /
  `OOW_DATA_DIR`, whitelisted, `--keep-data`, ...).
- `status`: `planned` (dry run), `done`, `kept`, `manual` (see `manual_steps`), `failed`.
- `manual_steps` lists the commands that delete the running executable after `oow` exits.
- `managed_by` is as in `oow.update/v1`; a package-managed install is not removed (exit 1).

## `oow startup --json` — `oow.startup/v1`

```json
{
  "schema": "oow.startup/v1", "sandbox": false, "elevated": false,
  "entries": [
    {
      "id": "hkcu-run:Contoso Agent", "name": "Contoso Agent", "source": "hkcu-run",
      "source_label": "Registry (this user)", "scope": "user",
      "location": "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
      "command": "\"C:\\Program Files\\Contoso\\Agent\\agent.exe\" --tray",
      "target": "C:\\Program Files\\Contoso\\Agent\\agent.exe", "target_state": "found",
      "state": "disabled", "disabled_at": "2026-09-01T08:00:00Z",
      "approval": { "key": "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\StartupApproved\\Run",
                    "value": "Contoso Agent", "data": "03000000008005e3e739dd01" },
      "toggleable": true, "needs_admin": false
    },
    {
      "id": "task-logon:Tailspin Sync", "name": "Tailspin Sync", "source": "task-logon",
      "source_label": "Scheduled task, at sign-in", "scope": "user", "location": "\\Tailspin Sync",
      "command": "%LOCALAPPDATA%\\Programs\\Tailspin Sync\\tailspin.exe --background",
      "target": "C:\\Users\\me\\AppData\\Local\\Programs\\Tailspin Sync\\tailspin.exe", "target_state": "missing",
      "state": "enabled",
      "task": { "path": "\\Tailspin Sync", "run_as": "S-1-5-21-...-1001", "triggers": ["logon"], "highest_privileges": false },
      "toggleable": true, "needs_admin": false
    }
  ],
  "summary": { "total": 14, "enabled": 11, "disabled": 2, "runs_once": 1, "broken": 4 },
  "warnings": []
}
```

- `id` is `<source>:<name>` and is what `startup enable|disable` accepts (besides names).
  `source`: `hkcu-run`, `hkcu-runonce`, `hklm-run`, `hklm-run32`, `hklm-runonce`,
  `hklm-runonce32`, `startup-folder`, `common-startup-folder`, `task-logon` (scheduled task with a
  sign-in trigger), `task-boot` (scheduled task with a startup trigger and no sign-in trigger).
  `scope`: `user` or `machine`.
- `location` is the registry key, the file in the Startup folder, or the task's path in the Task
  Scheduler library. `command` is the Run value, a shortcut's target and arguments, or the task's
  first program action. A task's `name` is its path without the leading `\`.
- `task` (task entries only): `path`, `run_as` (account or group; `SYSTEM`, `LOCAL SERVICE` and
  `NETWORK SERVICE` by name), `triggers` (enabled triggers: `logon`, `boot`, `time`, `calendar`,
  `idle`, `event`, `registration`, `session`, `other`), `highest_privileges`. Windows' own tasks
  (the `\Microsoft\` folder) are not listed. A task has `scope: user` only when it is the user's
  own: it runs as the user, at the user's sign-in, without highest privileges; every other task
  is `machine` and needs administrator rights to change. A task of another account whose program
  uses a per-user variable (`%LOCALAPPDATA%`, `%APPDATA%`, ...) is `unknown`, never `missing`.
- `target_state`: `found`, `missing`, or `unknown` (bare program names found through PATH at run
  time, network and removable drives, shortcuts to shell items); `target_note` says why. Only a
  verified `missing` makes an entry broken; `summary.broken` counts enabled broken entries.
- `state`: `enabled`, `disabled` or `runs-once` (RunOnce: cannot be toggled). `approval` is the
  StartupApproved value that holds the state; `data` is its hex bytes, `""` when absent (enabled).
  Task entries have no `approval`: their state is the task's own Enabled flag.
- `needs_admin`: the entry starts for all users (or is another account's task), so changing it
  needs administrator rights.

## `oow startup enable|disable --json` — `oow.startup-change/v1`

`{"schema", "action": "enable"|"disable", "dry_run", "sandbox", "elevated", "results": [{"entry",
"status", "reason", "before", "after", "task_enabled_before"}]}` with `entry` as in `oow.startup/v1`
(after the change). `status`: `planned` (dry run), `changed`, `unchanged` (already in that state),
`skipped` (RunOnce, or needs administrator; see `reason`), `failed`. `before`/`after` are the
approval values in hex; task entries have neither and carry `task_enabled_before` (the task's
Enabled flag before the change) instead. Exit code 1 when an entry was skipped or failed, 4
without `--yes` when not interactive.

## `oow doctor --json` — `oow.doctor/v1`

```json
{
  "schema": "oow.doctor/v1", "sandbox": false, "elevated": false,
  "checks": [
    { "id": "disk.free.d", "category": "storage", "title": "D:\\ free space", "status": "warning",
      "summary": "6.0 GB free of 256.0 GB (2%)",
      "details": ["Less than 10 GB or 10% free: updates and large downloads may fail soon."],
      "next_step": "Run `oow clean` to remove temporary files and caches, or `oow analyze D:\\` to see what uses the space.",
      "data": { "root": "D:\\", "free_bytes": 6442450944, "total_bytes": 274877906944 } }
  ],
  "summary": { "checks": 12, "issues": 5, "problems": 0, "warnings": 5, "ok": 7, "info": 0, "unknown": 0 }
}
```

- `status`: `ok`, `warning`, `problem`, `info`, `unknown`. `issues` = `problems` + `warnings`;
  `info` and `unknown` are not issues. A fact that cannot be read is `unknown`, never `ok`.
- Check IDs: `disk.free.<letter>` (warning under 10 GB or 10% free, problem under 2 GB),
  `cleanup.reclaimable`, `reboot.pending`, `windows-update`, `path.user`, `path.machine`,
  `startup.broken`, `network`, `permissions.temp`, `permissions.data`, `package-managers`, and
  `config` when `config.json` cannot be read. `data` holds the measured values of each check.
- doctor exits 0 whatever it finds; it never changes anything.

## `oow optimize --json` — `oow.optimize/v1`

```json
{
  "schema": "oow.optimize/v1", "dry_run": true, "sandbox": false, "elevated": true,
  "tasks": [
    { "task": { "id": "optimize.delivery-optimization", "name": "Clear the Delivery Optimization cache",
                "what": "...", "why": "...", "effect": "...", "requires_admin": true },
      "status": "ready", "selected": true, "bytes_before": 5000000 },
    { "task": { "id": "optimize.ssd-retrim", "...": "..." }, "status": "ready", "selected": true,
      "volumes": [ { "root": "C:\\", "file_system": "NTFS" } ] },
    { "task": { "id": "optimize.component-store", "...": "..." }, "status": "ready", "selected": false,
      "reason": "opt-in: it can take over an hour, and Windows also runs this cleanup on its own schedule; choose it in the list or run `oow optimize --task component-store`",
      "bytes_before": -1 }
  ],
  "notes": [ { "id": "reboot.pending", "status": "warning", "summary": "...", "next_step": "..." } ],
  "results": [
    { "id": "optimize.delivery-optimization", "name": "...", "status": "done", "message": "...",
      "bytes_before": 5000000, "bytes_after": 0, "freed_bytes": 5000000, "duration_ms": 900 }
  ]
}
```

With `--task component-store` (elevated), the task carries DISM's analysis:

```json
{ "task": { "id": "optimize.component-store", "...": "..." }, "status": "ready", "selected": true,
  "bytes_before": 11692944097,
  "component_store": { "actual_bytes": 11692944097, "explorer_bytes": 12047528837, "shared_bytes": 6603512627,
                       "backups_bytes": 5089531576, "cache_bytes": 0, "reclaimable_packages": 3,
                       "cleanup_recommended": true, "last_cleanup": "2024-03-18 09:58:02" } }
```

and a run reports the measured result:

```json
{ "id": "optimize.component-store", "name": "...", "status": "done", "message": "...",
  "bytes_before": 11692944097, "bytes_after": 8213335244, "freed_bytes": 3479608853,
  "component_store": { "...": "the analysis after the cleanup" }, "duration_ms": 1260000 }
```

- Task IDs: `optimize.dns-flush`, `optimize.delivery-optimization`, `optimize.ssd-retrim`,
  `optimize.component-store` (`--task` accepts them with or without the `optimize.` prefix).
- `status`: `ready`, `needs-admin`, `not-applicable` (nothing to do), `unavailable` (this Windows
  cannot run it); `reason` explains every status but `ready`, and also an opt-in task that is
  `ready` but not `selected`. `bytes_before` is the size of what the task cleans: the Delivery
  Optimization cache, or the component store's actual size. It is `-1` when it was not measured
  (both need administrator rights).
- `selected` is what `--yes` runs: every `ready` task except `optimize.component-store`, which is
  opt-in. Unless `--task` names it (`component-store` or `optimize.component-store`; a prefix such
  as `optimize` does not), it is listed as `ready`, `selected: false`, with the `opt-in: ...`
  reason, `bytes_before: -1` and no `component_store`, and DISM is not started at all.
- `component_store` (task `optimize.component-store` named with `--task`, elevated runs only) is
  DISM's `/AnalyzeComponentStore` report: `actual_bytes` (hard links counted once),
  `explorer_bytes` and `shared_bytes` (`-1` when DISM did not print them), `backups_bytes`
  ("Backups and Disabled Features") plus `cache_bytes` ("Cache and Temporary Data") is the overhead
  a cleanup frees part of, `reclaimable_packages`, `cleanup_recommended`, and `last_cleanup` as
  DISM printed it. It is present only when the whole report could be read; otherwise the task is
  `unavailable` with the reason. When named, the task is `ready` and `selected` if
  `cleanup_recommended` is true and `not-applicable` if it is false.
- `notes` are doctor checks shown for information only (`reboot.pending`, `windows-update`,
  `disk.free.*` when not ok); optimize never acts on them.
- `results` appears after a real run; `status` is `done`, `partial`, `failed`, `cancelled` or
  `skipped`. Exit code 1 when a task failed or was partial. The component store is analyzed again
  right before the cleanup, whatever the plan showed: if that analysis does not recommend a cleanup
  the result is `skipped` (not run, with the reason; `component_store` is that analysis), and if it
  fails the result is `failed` and nothing ran. After a cleanup, `bytes_after` and
  `component_store` come from analyzing once more (`bytes_after` is `-1` when that was not
  possible, and nothing is claimed as freed), and `restart_required` is true when DISM needs a
  restart to finish.

## `oow repair --json` — `oow.repair/v1`

```json
{
  "schema": "oow.repair/v1", "dry_run": false, "sandbox": false, "elevated": false,
  "fixes": [
    { "id": "path.user:3", "kind": "user-path-entry", "title": "Remove missing C:\\Old\\bin from your PATH",
      "target": "C:\\Old\\bin", "reason": "the folder does not exist", "selected": true, "needs_admin": false },
    { "id": "path.user:2", "kind": "user-path-entry", "title": "...", "target": "%USERPROFILE%\\go\\bin",
      "reason": "the folder does not exist", "selected": false,
      "review": "it is inside your profile; tools often add a folder like this before creating it", "needs_admin": false },
    { "id": "startup:hkcu-run:Fabrikam Updater", "kind": "startup-entry", "title": "Disable startup entry Fabrikam Updater",
      "target": "hkcu-run:Fabrikam Updater", "reason": "its program is missing: C:\\...\\updater.exe",
      "selected": true, "needs_admin": false }
  ],
  "not_fixed": ["The system PATH has 1 missing, 1 duplicate and 0 empty entries. ..."],
  "outcome": { "results": [ { "fix": { "...": "..." }, "status": "fixed" } ],
               "backup": "C:\\Users\\me\\AppData\\Local\\oow\\backups\\path-user-20261005-120000.reg",
               "fixed": 2, "skipped": 0, "failed": 0 }
}
```

- `selected` fixes are preselected; `--yes` applies exactly those. Unselected fixes carry
  `review` (inside the profile, or needs administrator rights).
- `outcome` appears after a real run. Fix `status`: `fixed`, `skipped` (with `reason`, e.g. the
  PATH changed after it was checked), `failed`. `backup` is a `.reg` file that restores the
  previous user PATH (double-click or `reg import`); `broadcast_error` is set when running programs
  could not be notified.

History records written by `startup`, `optimize` and `repair` carry
`changes: [{"id", "name", "action", "status", "detail", "error"}]` (`action`: `disabled`,
`enabled`, `ran`, `removed-path-entry`; `status`: `changed`, `skipped`, `failed`, `partial`).

## `oow purge --json` — `oow.purge/v1`

```json
{
  "schema": "oow.purge/v1",
  "dry_run": true,
  "sandbox": false,
  "roots": [
    { "path": "C:\\Users\\me\\source\\repos", "source": "default", "status": "ok" },
    { "path": "D:\\old", "source": "config", "status": "missing", "reason": "does not exist" }
  ],
  "projects": [
    {
      "path": "C:\\Users\\me\\source\\repos\\webapp",
      "markers": ["package.json"],
      "repository": "C:\\Users\\me\\source\\repos\\webapp",
      "artifacts": [
        {
          "path": "C:\\Users\\me\\source\\repos\\webapp\\node_modules", "kind": "node_modules",
          "label": "installed npm packages", "ecosystem": "Node.js", "bytes": 412000000, "files": 38201,
          "newest_change": "2026-08-20T10:00:00Z", "status": "ready", "selected": true, "reasons": [],
          "rebuild": "npm install (or yarn / pnpm install)",
          "result": { "method": "deleted", "removed_files": 38201, "removed_dirs": 4100,
                      "reclaimed_bytes": 412000000, "recycled_files": 0, "recycled_bytes": 0,
                      "complete": true, "skipped": 0, "skip_reasons": [], "errors": 0 }
        },
        {
          "path": "C:\\Users\\me\\source\\repos\\webapp\\dist", "kind": "js-output",
          "label": "JavaScript build output", "ecosystem": "Node.js", "bytes": 52000, "files": 3,
          "newest_change": "2026-09-01T10:00:00Z", "status": "kept", "selected": false,
          "reasons": ["contains files tracked by Git"], "rebuild": "your build script (for example npm run build)"
        }
      ]
    }
  ],
  "summary": {
    "projects": 14, "artifacts": 15, "reclaimable_bytes": 2100000000, "selected": 13,
    "selected_bytes": 2000000000, "kept": 4, "scan_errors": 0, "executed": false, "removed": 0,
    "removed_files": 0, "reclaimed_bytes": 0, "recycled": 0, "recycled_bytes": 0,
    "freed_on_disk_bytes": 0, "skipped": 0, "errors": 0, "cancelled": false, "scan_ms": 2100,
    "purge_ms": 0
  }
}
```

- `roots[].source`: `argument`, `config` or `default`; `status`: `ok`, `missing` or `refused`
  (with `reason`). Default folders that do not exist are omitted.
- `kind`: `node_modules`, `next`, `nuxt`, `svelte-kit`, `turbo`, `parcel-cache`, `angular`,
  `js-output` (dist/build/out), `cargo-target`, `maven-target`, `gradle-build`, `gradle-cache`,
  `cmake-build`, `dotnet-bin`, `dotnet-obj`, `pycache`, `pytest-cache`, `mypy-cache`, `ruff-cache`,
  `tox`, `venv`, `dart-tool`.
- `status`: `ready` (selected by default), `review` (offered but not selected: recent activity,
  dist/build/out without Git-ignore evidence, unusual bin/obj layout), `kept` (never deleted:
  Git-tracked files, nested repository, links, cloud-only or sensitive files, unreadable parts,
  Git unavailable, refused by the guard). `reasons` explains every non-ready status.
- `repository` is the folder holding `.git`, absent when the project is not in one.
- `summary.artifacts` and `reclaimable_bytes` count `ready` and `review` artifacts.
- `result` appears only for artifacts that a real run handled; `kept` in it names why the whole
  folder was left alone at deletion time. `method` is `deleted` (preselected artifacts, deleted
  permanently file by file) or `recycled` (artifacts added from review, moved whole to the
  Recycle Bin; `recycled_files`/`recycled_bytes`), absent when kept.
- `summary.removed` counts artifacts deleted completely, `summary.recycled` those moved to the
  Recycle Bin (`recycled_bytes`); `reclaimed_bytes` counts only deleted bytes.
- Without a terminal, `--yes` deletes exactly the artifacts with `selected: true`; review
  artifacts can only be added interactively.
- History records use `"command": "purge"` with one target per artifact (`id` is its path);
  `recycled_bytes` holds what was moved to the Recycle Bin.

## `oow purge --paths --json` / `oow config purge --json` — `oow.purge-paths/v1`

`{"schema", "config_file", "configured": ["D:\\code"], "roots": [{"path", "source", "status",
"reason"}]}`: the configured folders and the folders a plain `oow purge` would scan.
`oow config purge add|remove --json` prints the same document after the change.

## `oow installer --json` — `oow.installer/v1`

```json
{
  "schema": "oow.installer/v1",
  "dry_run": true,
  "sandbox": false,
  "folders": [ { "path": "C:\\Users\\me\\Downloads", "status": "ok" } ],
  "installers": [
    {
      "path": "C:\\Users\\me\\Downloads\\FabrikamPlayerSetup-2.0.1.exe", "name": "FabrikamPlayerSetup-2.0.1.exe",
      "type": "exe", "format": "setup program", "engine": "Inno Setup",
      "product": "Fabrikam Player", "version": "2.0.1", "publisher": "Fabrikam, Inc.",
      "evidence": ["Inno Setup installer data", "version resource FileDescription: Fabrikam Player Setup"],
      "size": 48000000, "modified": "2026-08-27T09:12:00Z", "age_days": 40,
      "installed": { "status": "installed", "app": "Fabrikam Player", "app_id": "reg:hkcu:FabrikamPlayer",
                     "version": "2.0.1", "match": "product name" },
      "status": "ready", "selected": true, "reasons": []
    }
  ],
  "warnings": [],
  "summary": {
    "installers": 8, "bytes": 310000000, "selected": 3, "selected_bytes": 120000000, "scan_errors": 0,
    "executed": false, "recycled": 0, "recycled_bytes": 0, "skipped": 0, "errors": 0,
    "cancelled": false, "scan_ms": 900
  }
}
```

After a real run (`--yes`) a `recycled` object is added:
`{"recycled": [{"path", "size"}], "bytes", "skipped": [{"path", "reason"}], "errors"}`.

- `type`: `msi`, `msp`, `msix` (MSIX/APPX), `msixbundle`, `exe`, `zip` (archive with an installer
  at its root), `iso`. `product_code` (MSI) and `package_identity` (MSIX) appear when known.
- `installed.status`: `installed` (exact match; `match` is `product code`, `package identity` or
  `product name`), `not-found` (no installed app matches exactly) or `unknown` (archives, disc
  images, patches, or the app list could not be read; see `warnings`).
- `status`: `ready` (installed and older than 7 days: selected by default) or `review`
  (`reasons` says why: not installed, unknown, recent, newer than the installed version, disc
  image without setup files).
- History records use `"command": "installer"` and `recycled_bytes`.

## `oow config --json` — `oow.config/v1`

`{"schema", "config_file", "data_dir", "sandbox", "config": {"version", "whitelist":
{"paths", "rules"}, "ui": {"color"}, "purge": {"paths"}}}`.
`oow config whitelist --json` prints just the `whitelist` object.

## `oow config protected --json` — `oow.protected/v1`

`{"schema", "locations": [{"path", "label", "kind"}]}` with `kind` one of `never-remove`,
`system-tree`, `user-content`, `protected`, `sensitive`.

## `oow version --json` — `oow.version/v1`

`{"schema", "name", "version", "commit", "built", "arch", "os": {"name",
"display_version", "build", "ubr", "edition"}, "elevated"}`
