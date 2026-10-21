# Releasing

The steps for a stable release `vX.Y.Z`, in order. The rules behind them are in
[AGENTS.md](../AGENTS.md#release): releases are tag-driven, history reachable from a release tag
is never rewritten, and the maintainer confirms which distribution channels each release touches.

1. **Main is green.** CI passed for the exact commit you tag (`gh run list --json headSha,conclusion`);
   a cancelled run is not a pass.
2. **Tag and push:** `git tag vX.Y.Z` then `git push origin vX.Y.Z`. `release.yml` checks that the
   tag is on `main`, builds both architectures, writes `SHA256SUMS`, attests every file, publishes
   the GitHub release and installs it on a fresh VM with the documented one-liner.
3. **Defender.** Run Actions > **Defender check** (it scans the latest release). Until releases are
   signed ([#15](https://github.com/Harshul1484/out-of-windows/issues/15)) each build is a new
   file that machine-learning detection has never seen. If anything is flagged, follow
   [docs/DEFENDER.md](DEFENDER.md) and do not announce the release or submit it to package
   repositories until the check passes.
4. **Package managers**, once the Defender check passes (see
   [packaging/README.md](../packaging/README.md)):
   - Scoop: the bucket's Excavator workflow picks the release up on its own; check
     [Harshul1484/scoop-bucket](https://github.com/Harshul1484/scoop-bucket).
   - Chocolatey: Actions > **Publish Chocolatey**, with the tag.
   - winget: a pull request `New version: Harshul1484.oow version X.Y.Z` to `microsoft/winget-pkgs`
     with the manifests from the release's `oow-X.Y.Z-package-manifests.zip`.
5. **Announce:** the release link in Discussions > Announcements, and on the project's social
   accounts if any.
