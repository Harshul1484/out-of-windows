# Package manager templates

> **Not published.** These are templates for winget, Scoop and Chocolatey. Nothing is
> submitted to any package repository before the first release
> ([#12](https://github.com/Harshul1484/out-of-windows/issues/12)). Until then, install with
> [`scripts/install.ps1`](../scripts/install.ps1) (see the [README](../README.md#install)).

## Package identities

The name is final (`oow`, [#11](https://github.com/Harshul1484/out-of-windows/issues/11)):

| Manager | Identity |
|---|---|
| winget | `Harshul1484.oow`, publisher `Harshul1484`, moniker `oow` |
| Scoop | `oow` (the manifest's file name, `oow.json`) |
| Chocolatey | `oow` |

## Placeholders

| Placeholder | Value |
|---|---|
| `<VERSION>` | release version without the `v`, e.g. `1.2.3` |
| `<URL_AMD64>`, `<URL_ARM64>` | download URL of `oow-<VERSION>-windows-amd64.zip` / `-arm64.zip` from the GitHub release |
| `<SHA256_AMD64>`, `<SHA256_ARM64>` | SHA-256 of those zips, as listed in the release's `SHA256SUMS` |
| `<URL>`, `<SHA256>` | Chocolatey: the amd64 zip and its SHA-256 |

Always take checksums from `SHA256SUMS` of the release (and check its build-provenance
attestation with `gh attestation verify <file> --repo Harshul1484/out-of-windows`), never by
hashing a file you downloaded separately.

## Files

| Manager | Files | Installs to |
|---|---|---|
| winget | `winget/oow.yaml`, `oow.installer.yaml`, `oow.locale.en-US.yaml` (manifest 1.10.0, zip with a portable `oow.exe`) | `%LOCALAPPDATA%\Microsoft\WinGet\Packages\Harshul1484.oow_*` |
| Scoop | `scoop/oow.json` (with `checkver` and `autoupdate` from `SHA256SUMS`) | `~\scoop\apps\oow\` |
| Chocolatey | `chocolatey/oow.nuspec`, `chocolatey/tools/chocolateyinstall.ps1` (amd64 zip; ARM64 Windows runs it emulated) | `C:\ProgramData\chocolatey\lib\oow\tools\` |

`oow update` and `oow remove` recognize these locations and tell the user to use the package
manager instead (`winget upgrade`, `scoop update`, `choco upgrade`, and the matching uninstall
commands), so a package-managed copy is never replaced or deleted behind the manager's back.

## Known issue: antivirus false positives

Microsoft Defender may flag `oow.exe` as malware. It is a false positive: `oow` lists the
names of browser credential files, wallets and key stores so that it can protect them, which
machine-learning detection mistakes for an information stealer. Package repositories scan
submissions, so expect this to come up during review. Code signing and false-positive
submissions to Microsoft are the planned fixes.
