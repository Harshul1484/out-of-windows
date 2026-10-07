# Microsoft Defender false positive

Microsoft Defender may detect `oow.exe` as **Trojan:Win32/LummaStealer.DO!MTB** and quarantine
it. The `!MTB` suffix marks a machine-learning verdict, not a signature written for `oow`. It
is a false positive, tracked in
[#18](https://github.com/Harshul1484/out-of-windows/issues/18); this page explains why it
happens, how to check a download, and what the maintainer does about it.

## Why it happens

`oow` deletes files, so it carries explicit lists of what it must never touch: browser
password, cookie and history databases, crypto wallets, private keys, DPAPI and credential
stores, password managers (see [SAFETY.md](SAFETY.md) and `internal/safety`). It also lists
installed apps and reads Windows' record of programs that ran, to find leftovers after an
uninstall. An information stealer carries a similar inventory for the opposite reason, and
the model appears to weigh those names. `oow` never reads the contents of those files, sends
no telemetry, and its own network access is `oow update` (GitHub's API and release
downloads).

The project does not obfuscate strings, pack the binary or change its safety logic to get
past detection: the lists stay readable so anyone can audit what `oow` protects. The fixes
are the legitimate ones:

- every binary carries Windows version information (product, publisher, version, copyright)
  and an application manifest that runs it as the invoking user (`asInvoker`); CI fails if
  either is missing;
- every release publishes `SHA256SUMS` and a build-provenance attestation per file;
- each release is submitted to Microsoft as a false positive (below);
- code signing is planned
  ([#15](https://github.com/Harshul1484/out-of-windows/issues/15)); a signed publisher builds
  reputation across releases.

## Check a download

Take both checks from the same release as the file:

```powershell
# 1. The SHA-256 must equal the file's line in SHA256SUMS.
(Get-FileHash .\oow-1.2.3-windows-amd64.exe -Algorithm SHA256).Hash.ToLower()

# 2. The attestation proves the file was built by this repository's release workflow.
gh attestation verify .\oow-1.2.3-windows-amd64.exe --repo Harshul1484/out-of-windows
```

The install script and `oow update` check the SHA-256 themselves and stop on a mismatch.
The version information (`(Get-Item .\oow.exe).VersionInfo`) identifies the file, but anyone
can copy it into another program, so it is not proof; the checksum and attestation are.

## If Defender removed oow.exe

- **Do not turn off Microsoft Defender or real-time protection**, and do not add exclusions
  for whole folders such as a drive, `Downloads`, `%TEMP%` or your profile. Avoid "Allow on
  device" too: it allows the detection itself, which can let other files with the same
  detection through.
- Report the file yourself at <https://www.microsoft.com/en-us/wdsi/filesubmission> (choose
  "Home customer" and "Incorrectly detected as malware/malicious"); more reports help.
- Once Microsoft updates its definitions, run `Update-MpSignature` (or Windows Update) and
  install again.
- If you cannot wait, that is your decision to make. Keep any exception to the one file you
  verified, and remove it once the definitions are fixed:
  `Add-MpPreference -ExclusionPath "$env:LOCALAPPDATA\Programs\oow\oow.exe"` (elevated), later
  `Remove-MpPreference -ExclusionPath "$env:LOCALAPPDATA\Programs\oow\oow.exe"`.

**Contributors**: Defender can also delete test binaries that `go test` builds. CI on GitHub's
runners is the reference. An exclusion on a development machine is the machine owner's call;
if you make one, scope it to your clone (test binaries are linked under `go env GOTMPDIR`,
which defaults to `%TEMP%`: point it into the clone rather than excluding `%TEMP%`).

## Submitting a release as a false positive (maintainer)

Do this for every release, after it is published: a machine-learning verdict is per file, so
each new build can be flagged again.

1. Find the exact detection name and the definition version on a machine that flagged it:
   Windows Security > Virus & threat protection > Protection history, or
   `Get-MpThreat | Select-Object ThreatName` and
   `(Get-MpComputerStatus).AntivirusSignatureVersion`.
2. Open <https://www.microsoft.com/en-us/wdsi/filesubmission>, sign in when asked, and choose
   **Software developer**.
3. Product: Microsoft Defender Antivirus. File: the flagged release file, for example
   `oow-1.2.3-windows-amd64.exe` (submit the `arm64` build and the zips too if they are
   flagged; one submission per file).
4. Answer **Incorrectly detected as malware/malicious**, and enter the detection name (e.g.
   `Trojan:Win32/LummaStealer.DO!MTB`) and the definition version.
5. Additional information, for example:

   > Open-source Windows maintenance CLI (MIT). Release:
   > https://github.com/Harshul1484/out-of-windows/releases/tag/v1.2.3. SHA-256 from the
   > release's SHA256SUMS: `<hash>`. Build provenance: `gh attestation verify <file> --repo
   > Harshul1484/out-of-windows`. The binary names browser credential stores, wallets and key
   > files in order to refuse to delete them (internal/safety); it never reads their contents
   > and has no network access other than its own update check.

6. Keep the submission ID (note it in
   [#18](https://github.com/Harshul1484/out-of-windows/issues/18)). When Microsoft reports
   the result, update the definitions and re-check without quarantining the file:

   ```powershell
   Update-MpSignature
   & "$env:ProgramFiles\Windows Defender\MpCmdRun.exe" -Scan -ScanType 3 -File <path> -DisableRemediation
   ```

   Without downloading anything to your own machine: run the **Defender check** workflow
   (Actions → Defender check → Run workflow, with the tag, e.g. `v0.1.0`). It updates Defender
   on a fresh GitHub-hosted VM, scans every file of that release without quarantining it, and
   lists the result in the run's summary. A clean result there is a good sign, not proof: the
   machine-learning and cloud settings of a server runner can differ from a home PC.

Package repositories scan submissions (winget-pkgs runs Defender, Chocolatey runs
VirusTotal), so resolve a detection before publishing a release there; see
[packaging/README.md](../packaging/README.md).
