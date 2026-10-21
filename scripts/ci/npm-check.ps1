<#
.SYNOPSIS
    Installs an out-of-windows npm tarball globally and runs oow through its launcher.

.DESCRIPTION
    CI only: it changes the machine's global npm packages, so it refuses to run outside GitHub
    Actions. ci.yml runs it on a package built from fresh builds; publish-npm.yml runs it on the
    exact tarball it is about to publish. It checks that:

    - npm links the `oow` command, and the launcher starts the executable for this CPU from the
      package's vendor folder, with the expected version;
    - exit codes pass through the launcher;
    - `oow update` and `oow remove` recognize the npm copy and name npm's commands, without
      network access and without changing anything (both run with --dry-run).

    The package is uninstalled again at the end.

.PARAMETER Tarball
    The .tgz written by `npm pack`.

.PARAMETER Version
    The version the package and its executables must report.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Tarball,
    [Parameter(Mandatory)] [string]$Version
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ($env:GITHUB_ACTIONS -ne 'true') { throw 'npm-check.ps1 installs a global npm package; it runs on CI only' }

npm install -g (Resolve-Path -LiteralPath $Tarball).Path | Out-Host
if ($LASTEXITCODE -ne 0) { throw "npm install -g $Tarball failed (exit $LASTEXITCODE)" }
try {
    $shim = Get-Command oow -ErrorAction Stop
    Write-Host "oow resolves to $($shim.Source)"

    $info = oow version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $info.version -ne $Version) {
        throw "oow version reported '$($info.version)' (exit $LASTEXITCODE), expected $Version"
    }

    oow no-such-command 2>$null | Out-Null
    if ($LASTEXITCODE -ne 2) { throw "an unknown command exited $LASTEXITCODE through the launcher, expected 2 (usage)" }

    $arch = @{ AMD64 = 'win32-x64'; ARM64 = 'win32-arm64' }[$env:PROCESSOR_ARCHITECTURE]
    $want = @{ update = 'npm update -g out-of-windows'; remove = 'npm uninstall -g out-of-windows' }
    foreach ($command in 'update', 'remove') {
        $doc = oow $command --dry-run --json | ConvertFrom-Json
        $code = $LASTEXITCODE
        if ($code -ne 1 -or $null -eq $doc.managed_by -or $doc.managed_by.manager -ne 'npm' -or $doc.error -notlike "*$($want[$command])*") {
            throw "oow $command did not leave the npm copy to npm (exit $code): $($doc | ConvertTo-Json -Compress -Depth 5)"
        }
        if ($command -eq 'update' -and $doc.executable -notlike "*\node_modules\out-of-windows\vendor\$arch\oow.exe") {
            throw "the launcher started $($doc.executable), expected the package's vendor\$arch\oow.exe"
        }
    }
    Write-Host "npm package ${Version}: installed, launched, exit codes passed through, update and remove left to npm."
} finally {
    npm uninstall -g out-of-windows | Out-Host
}
