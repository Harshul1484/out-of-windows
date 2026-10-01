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
