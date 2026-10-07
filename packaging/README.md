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

## Rendering and validating

[`render.ps1`](render.ps1) fills the templates for one release and writes ready-to-submit
files; [`validate.ps1`](validate.ps1) checks them. Nothing is submitted anywhere.

```powershell
./packaging/render.ps1   -Version 1.2.3 -Dist dist -Out packages -Repo Harshul1484/out-of-windows
./packaging/validate.ps1 -Version 1.2.3 -Dist dist -Dir packages -Repo Harshul1484/out-of-windows
```

| Output | Contents |
|---|---|
| `winget/manifests/h/Harshul1484/oow/<VERSION>/` | `Harshul1484.oow.yaml`, `.installer.yaml`, `.locale.en-US.yaml`, laid out as in `microsoft/winget-pkgs` |
| `scoop/oow.json` | the Scoop manifest |
| `chocolatey/` | `oow.nuspec`, `tools/chocolateyinstall.ps1` and `oow.<VERSION>.nupkg` (built with `choco pack`) |

`render.ps1` hashes the zips in `-Dist`. The release workflow runs it on the zips it has just
built, the same files it then lists in `SHA256SUMS`. If you ever render by hand, download the
zips from the release and check them against `SHA256SUMS` and the attestation
(`gh attestation verify <file> --repo Harshul1484/out-of-windows`) first; never hash a file you
downloaded separately and trust it as is. Rendering needs Chocolatey (`choco`) for the
`.nupkg`; validation needs Go and network access.

`validate.ps1` fails unless:

- **winget**: each manifest passes the official JSON schema of the manifest type and version
  it declares (`tools/wingetcheck`, schemas pinned to a `microsoft/winget-cli` commit), and
  `winget validate` where winget is installed (GitHub's runners do not have it); the identity
  is `Harshul1484.oow` by `Harshul1484`, the version matches in all three files, and the x64
  and arm64 installers point at the release zips with their SHA-256;
- **Scoop**: `oow.json` is JSON with the required fields, each architecture's `url` and
  lower-case SHA-256 `hash` match the zips, and `autoupdate` yields the same URLs for this
  version;
- **Chocolatey**: the nuspec is XML with id `oow` and the version, the install script parses
  and points at the amd64 zip and its SHA-256, and the `.nupkg` contains both.

CI's `packages` job renders and validates on every push with stand-in zips (a stable and a
pre-release version).

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
submissions to Microsoft are the planned fixes; see [docs/DEFENDER.md](../docs/DEFENDER.md).
