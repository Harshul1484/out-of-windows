# Package manager templates

> **Status ([#13](https://github.com/Harshul1484/out-of-windows/issues/13)):** Scoop is
> published in [Harshul1484/scoop-bucket](https://github.com/Harshul1484/scoop-bucket) since
> v0.1.0 and follows new releases on its own. Chocolatey published
> [`oow`](https://community.chocolatey.org/packages/oow) 0.1.0 on 2026-10-21. winget is in
> review ([microsoft/winget-pkgs#455243](https://github.com/microsoft/winget-pkgs/pull/455243)).
> Both scan submissions, so each later release waits for its Defender check
> ([#18](https://github.com/Harshul1484/out-of-windows/issues/18)) to pass.

## Package identities

The name is final (`oow`, [#11](https://github.com/Harshul1484/out-of-windows/issues/11)):

| Manager | Identity |
|---|---|
| winget | `Harshul1484.oow`, publisher `Harshul1484`, moniker `oow` |
| Scoop | `oow` (the manifest's file name, `oow.json`) |
| Chocolatey | `oow`, maintained by the author as [H1000](https://community.chocolatey.org/profiles/H1000) |
| npm | `out-of-windows`, command `oow` (the name `oow` is taken on npm) |

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
pre-release version). The release workflow runs both on the real zips and attaches the result
to the release as `oow-<VERSION>-package-manifests.zip`, listed in `SHA256SUMS` and covered by
the attestation, so publishing is a copy step.

## Publishing

Only after the first release ([#12](https://github.com/Harshul1484/out-of-windows/issues/12)),
and only for stable versions (no `-rc` or other pre-release). Package repositories scan
submissions (winget-pkgs with Microsoft Defender, Chocolatey with VirusTotal), so first make
sure the release binaries are not flagged, or that a false-positive submission has been
cleared ([docs/DEFENDER.md](../docs/DEFENDER.md)); signing
([#15](https://github.com/Harshul1484/out-of-windows/issues/15)) helps.

**1. Get the files of the release** (here `1.2.3`) and verify them:

```powershell
$v = '1.2.3'
gh release download "v$v" --repo Harshul1484/out-of-windows --pattern "oow-$v-package-manifests.zip" --pattern SHA256SUMS
(Get-FileHash "oow-$v-package-manifests.zip" -Algorithm SHA256).Hash.ToLower()   # must equal its line in SHA256SUMS
Select-String -Path SHA256SUMS -SimpleMatch "oow-$v-package-manifests.zip"
gh attestation verify "oow-$v-package-manifests.zip" --repo Harshul1484/out-of-windows
Expand-Archive "oow-$v-package-manifests.zip" -DestinationPath packages
```

**2. winget** (a pull request to `microsoft/winget-pkgs`, for the first and every later version):

```powershell
gh repo fork microsoft/winget-pkgs --clone=false            # once
git clone --filter=blob:none --sparse https://github.com/<your-account>/winget-pkgs
cd winget-pkgs
git sparse-checkout set manifests/h/Harshul1484
git switch -c "Harshul1484.oow-$v"
Copy-Item -Recurse "..\packages\winget\manifests\h\Harshul1484\oow\$v" manifests\h\Harshul1484\oow\
winget validate --manifest "manifests\h\Harshul1484\oow\$v"
git add manifests/h/Harshul1484/oow
git commit -m "New package: Harshul1484.oow version $v"      # later: "New version: Harshul1484.oow version $v"
git push -u origin HEAD
gh pr create --repo microsoft/winget-pkgs --web             # same title; complete the PR template's checklist
```

Optionally test first in Windows Sandbox: `winget settings --enable LocalManifestFiles` (as
administrator), then `winget install --manifest <folder>`. The bots validate, install and scan
the package; answer their labels on the pull request. Users then run
`winget install Harshul1484.oow`.

**3. Scoop** (done for v0.1.0: [Harshul1484/scoop-bucket](https://github.com/Harshul1484/scoop-bucket),
default branch `master`; later versions arrive through its Excavator workflow, so this step is
only needed to recreate the bucket. The official Extras bucket only takes well-known apps, so
propose `bucket/oow.json` there later, once it qualifies):

```powershell
gh repo create Harshul1484/scoop-bucket --public --template ScoopInstaller/BucketTemplate --clone   # once
cd scoop-bucket
Copy-Item ..\packages\scoop\oow.json bucket\oow.json
git add bucket/oow.json
git commit -m "oow: Add version $v"
git push origin master
scoop bucket add oow https://github.com/Harshul1484/scoop-bucket   # test
scoop install oow/oow
oow version
```

Once, follow the template's README: allow GitHub Actions with write permission, set the
repository in `bin/auto-pr.ps1`, and add the `scoop-bucket` topic. Its Excavator workflow then
follows new releases on its own through `checkver` and `autoupdate` (hashes from each
release's `SHA256SUMS`), so later versions need no copy step. Users run the same two `scoop`
commands.

**4. Chocolatey** (the community repository; every version is a new push). The usual route
needs no Chocolatey on your machine:

1. Once, create an account at <https://community.chocolatey.org>, copy your API key (Account >
   API Keys) and store it as the repository secret `CHOCOLATEY_API_KEY` (Settings > Secrets
   and variables > Actions > New repository secret). The key never goes into a file or a
   command line.
2. For each stable release, after the Defender check passes for it: Actions > **Publish
   Chocolatey** > Run workflow, with the tag (e.g. `v1.2.3`). It downloads
   `oow-<VERSION>-package-manifests.zip`, verifies it against `SHA256SUMS` and its
   attestation, and pushes the `.nupkg` inside.

By hand instead, from a machine with Chocolatey:

```powershell
# once: create an account at https://community.chocolatey.org and copy your API key
choco apikey add --source https://push.chocolatey.org/ --key <api-key>
# test in a clean Windows (e.g. Windows Sandbox) as administrator:
choco install oow --source "$PWD\packages\chocolatey" -y    # downloads the release zip, checks its SHA-256
oow version
choco uninstall oow -y
# publish:
choco push "packages\chocolatey\oow.$v.nupkg" --source https://push.chocolatey.org/
```

The package then goes through Chocolatey's automated validation and verification and, for new
packages, human moderation; follow up on the package page. Users run `choco install oow`.

**5. npm** ([#24](https://github.com/Harshul1484/out-of-windows/issues/24); every version is a
new publish). [`npm/render.ps1`](npm/render.ps1) builds the package from
[`npm/package.json`](npm/package.json), the launcher [`npm/bin/oow.js`](npm/bin/oow.js) and both
release executables; CI's `npm` job builds it from fresh builds, installs it and runs `oow`
through the launcher on every push.

1. Once, create an account at <https://www.npmjs.com> with two-factor authentication. For the
   first publish, create an access token that can publish packages and store it as the
   repository secret `NPM_TOKEN`.
2. For each stable release, after the Defender check passes for it: Actions > **Publish npm** >
   Run workflow, with the tag. It downloads both executables, checks them against
   `SHA256SUMS` and their attestations, packs the package, installs that tarball and runs
   [`scripts/ci/npm-check.ps1`](../scripts/ci/npm-check.ps1) on it, then runs
   `npm publish --provenance`.
3. After the first publish, on npmjs.com open the package's Settings > Trusted Publisher, add
   GitHub Actions with repository `Harshul1484/out-of-windows` and workflow
   `publish-npm.yml`, then delete the `NPM_TOKEN` secret (and the token on npmjs.com). Later
   publishes authenticate through GitHub's OIDC token.

Users run `npm install -g out-of-windows`, or `npx out-of-windows` without installing.

**6. Afterwards**: add the install commands to the [README](../README.md#install), tick the
boxes in [#13](https://github.com/Harshul1484/out-of-windows/issues/13) or
[#24](https://github.com/Harshul1484/out-of-windows/issues/24), and check that
`oow update --check` on a package-managed copy names the right package-manager command.

## Files

| Manager | Files | Installs to |
|---|---|---|
| winget | `winget/oow.yaml`, `oow.installer.yaml`, `oow.locale.en-US.yaml` (manifest 1.10.0, zip with a portable `oow.exe`) | `%LOCALAPPDATA%\Microsoft\WinGet\Packages\Harshul1484.oow_*` |
| Scoop | `scoop/oow.json` (with `checkver` and `autoupdate` from `SHA256SUMS`) | `~\scoop\apps\oow\` |
| Chocolatey | `chocolatey/oow.nuspec`, `chocolatey/tools/chocolateyinstall.ps1` (amd64 zip; ARM64 Windows runs it emulated) | `C:\ProgramData\chocolatey\lib\oow\tools\` |
| npm | `npm/package.json`, `npm/bin/oow.js`, `npm/README.md`, rendered with both executables under `vendor\win32-x64\` and `vendor\win32-arm64\` (no install scripts) | `%APPDATA%\npm\node_modules\out-of-windows\` (global) |

`oow update` and `oow remove` recognize these locations and tell the user to use the package
manager instead (`winget upgrade`, `scoop update`, `choco upgrade`, `npm update -g`, and the
matching uninstall commands), so a package-managed copy is never replaced or deleted behind the
manager's back. A copy npx runs from npm's cache installs nothing: `update` names
`npx out-of-windows@latest` and `remove` explains there is nothing to remove.

## Known issue: antivirus false positives

Microsoft Defender may flag `oow.exe` as malware. It is a false positive: `oow` lists the
names of browser credential files, wallets and key stores so that it can protect them, which
machine-learning detection mistakes for an information stealer. Package repositories scan
submissions, so expect this to come up during review. Code signing and false-positive
submissions to Microsoft are the planned fixes; see [docs/DEFENDER.md](../docs/DEFENDER.md).
