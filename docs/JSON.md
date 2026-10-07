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

After a real run (`--yes`) a `recycled` object is added, as in `oow.uninstall/v1`.

- `source`: `uninstalled`, `history`, `broken-entry`, `usage-trace`.
- `confidence`: `high` (registered install folder, or two or more independent signals) or
  `medium` (a single exact-name signal, and everything based on usage traces).
- `needs_admin`: the folder is in Program Files or ProgramData and `oow` is not elevated.

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
