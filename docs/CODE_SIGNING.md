# Code signing policy

This page states how `oow` release binaries are built and signed, and who decides what is
signed. It applies once Windows code signing is in place (issue #15); until then releases are
not Authenticode-signed and are verified through `SHA256SUMS` and build-provenance attestations
(see [DEFENDER.md](DEFENDER.md)).

## What is signed

- Only release builds: the `oow-<version>-windows-<arch>.exe` files that the tag-driven
  [release workflow](../.github/workflows/release.yml) builds from a `v*` tag whose commit is on
  `main`. The workflow refuses tags that are not on `main`.
- Builds run on GitHub-hosted runners from this repository's source, with the build steps in
  the workflow file. Nothing built on a developer machine is ever signed.
- Development builds, pull-request builds, manual (dry-run) workflow runs and test binaries are
  never signed.

## Roles

| Role | Who | What they do |
|---|---|---|
| Committers and reviewers | [Harshul1484](https://github.com/Harshul1484) | Write and review every change that reaches `main` |
| Approvers | [Harshul1484](https://github.com/Harshul1484) | Approve each signing request for a release |

Everyone with one of these roles uses multi-factor authentication on GitHub and on the signing
service. Contributions from outside the team are reviewed by a committer before they are
merged; a change is never signed without that review.

## Privacy

`oow` does not transfer any information to other networked systems unless the user
specifically asks for it: `oow update` contacts the GitHub API to look for and download a
release, and `oow doctor` reads the local network adapter configuration without sending
anything. There is no telemetry, no crash reporting and no background service. See
[SECURITY_AUDIT.md](../SECURITY_AUDIT.md) for what each command reads and changes.

## Verifying a download

Every release file is listed in `SHA256SUMS` and has a build-provenance attestation:

```powershell
(Get-FileHash oow-<version>-windows-amd64.exe -Algorithm SHA256).Hash   # compare with SHA256SUMS
gh attestation verify oow-<version>-windows-amd64.exe --repo Harshul1484/out-of-windows
```

Once releases are signed, the Authenticode signature can also be checked with
`Get-AuthenticodeSignature oow.exe`.
