# out-of-windows (`oow`)

Windows-native system maintenance from the terminal: cleanup of temporary files and caches,
complete app uninstall with the app's own uninstaller, disk analysis, developer-artifact
purge and a live status dashboard. Every destructive action is previewed, explained,
confirmed and verified.

```powershell
npm install -g out-of-windows
oow                     # home screen
oow clean --dry-run     # what cleanup would remove, without removing anything
```

Or run it once without installing: `npx out-of-windows`.

Windows 10 version 1809 or later, or Windows 11, on x64 or ARM64. No administrator rights
needed.

This package contains the release's own `oow.exe` for x64 and ARM64. The npm package is published
from GitHub Actions with provenance, and each executable is checked against the release's
`SHA256SUMS` and its build-provenance attestation before publishing. It has no install
scripts. Update with `npm update -g out-of-windows`, remove with
`npm uninstall -g out-of-windows`; `oow update` and `oow remove` leave an npm copy to npm.

Documentation, safety model and source: https://github.com/Harshul1484/out-of-windows
