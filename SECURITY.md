# Security Policy

`oow` deletes files and can run with administrator rights, so we treat safety defects as
security issues. Thank you for reporting them responsibly.

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Report privately through
GitHub's private vulnerability reporting:

**[Report a vulnerability](https://github.com/Harshul1484/out-of-windows/security/advisories/new)**
(repository → Security → Advisories → Report a vulnerability)

Include:

- the `oow version --json` output (version, Windows build, architecture, elevation),
- the exact command and flags, and whether it ran elevated,
- what was deleted, modified or exposed that should not have been, and what you expected,
- minimal reproduction steps; a directory layout, junction or path spelling that triggers it
  is ideal. Please reproduce in a disposable VM or `oow --sandbox <dir>`, not on a machine
  with data you care about.

## What to expect

This is a volunteer-maintained open-source project. We aim to:

- acknowledge a report within **7 days**,
- give a status update (confirmed, not reproducible, or out of scope) within **30 days**,
- fix confirmed issues that can cause unintended deletion before other work, publish a
  release, and credit the reporter in the advisory unless they prefer otherwise.

Please allow a fix to be released before public disclosure.

## Supported versions

Security fixes are made for:

- the latest published release, and
- the `main` branch.

Older releases do not receive fixes; please update before reporting.

## In scope

Reports are especially welcome for:

- **unintended deletion**: anything removed outside a target's documented scope, or any
  protected location (system folders, profile and AppData roots, user content, credentials,
  keys, wallets, VM disks, the whitelist) being deleted or modified;
- **path validation bypasses**: path spellings, `..` traversal, `\\?\` / device / UNC forms,
  8.3 short names, trailing dots or spaces, alternate data streams, case tricks;
- **link handling**: junctions, symbolic links, mount points or hard links that make `oow`
  delete or follow something outside a cleanup root, including time-of-check/time-of-use
  races between scan and deletion;
- **privilege issues**: elevation used for more than the operation needs, elevated operations
  acting through user-writable locations, or unexpected UAC prompts;
- **data exposure**: logs, history or JSON output leaking secrets or content of private files;
- **dry-run violations**: `--dry-run` or `OOW_DRY_RUN=1` changing anything;
- **release integrity**: tampered binaries, checksum or attestation mismatches, installers that
  do not fail closed.

## Usually out of scope

- cleanup that leaves reclaimable files behind, or a target that is skipped because safety
  could not be established (that is intended behavior);
- requests to delete more aggressively, or to remove a protection;
- cosmetic or output-formatting problems;
- issues requiring an attacker who already has administrator rights or can replace the
  `oow.exe` binary.

If you are unsure whether something is a security issue, report it privately anyway.

See [SECURITY_AUDIT.md](SECURITY_AUDIT.md) for the current security review and
[docs/SAFETY.md](docs/SAFETY.md) for the safety design.
