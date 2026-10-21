<#
.SYNOPSIS
    Renders the npm package `out-of-windows` for one release.

.DESCRIPTION
    Writes the package folder to -Out, ready for `npm pack` or `npm publish`:

        package.json                      the template with the release version
        bin/oow.js                        the launcher npm links as the `oow` command
        vendor/win32-x64/oow.exe          the x64 executable
        vendor/win32-arm64/oow.exe        the ARM64 executable
        README.md, LICENSE

    The executables must be the release's own files: the publish workflow checks them against
    SHA256SUMS and their attestations first. CI renders it with a fresh build to test the
    launcher. Nothing is uploaded anywhere.

.PARAMETER Version
    The release version without the v, e.g. 1.2.3.

.PARAMETER Amd64Exe
    The x64 oow.exe (oow-<Version>-windows-amd64.exe from the release).

.PARAMETER Arm64Exe
    The ARM64 oow.exe (oow-<Version>-windows-arm64.exe from the release).

.PARAMETER Out
    The output folder. It must not exist yet, or be empty.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Version,
    [Parameter(Mandatory)] [string]$Amd64Exe,
    [Parameter(Mandatory)] [string]$Arm64Exe,
    [Parameter(Mandatory)] [string]$Out
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ($Version -notmatch '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$') {
    throw "'$Version' is not a semantic version like 1.2.3"
}
if ((Test-Path -LiteralPath $Out) -and @(Get-ChildItem -LiteralPath $Out -Force).Count -gt 0) {
    throw "$Out already exists and is not empty"
}

$utf8 = [Text.UTF8Encoding]::new($false)
$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
New-Item -ItemType Directory -Force -Path $Out | Out-Null

# package.json: the version is the only placeholder.
$text = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'package.json')).Replace('<VERSION>', $Version)
$left = @([regex]::Matches($text, '<[A-Z][A-Z0-9_]*>') | ForEach-Object { $_.Value } | Sort-Object -Unique)
if ($left.Count -gt 0) { throw "package.json: unfilled placeholders $($left -join ', ')" }
$manifest = $text | ConvertFrom-Json
if ($manifest.name -cne 'out-of-windows' -or $manifest.version -cne $Version) {
    throw "package.json: expected out-of-windows $Version, got $($manifest.name) $($manifest.version)"
}
if ($manifest.bin.oow -cne 'bin/oow.js') { throw 'package.json: the oow command must be bin/oow.js' }
[IO.File]::WriteAllText((Join-Path $Out 'package.json'), $text, $utf8)

New-Item -ItemType Directory -Force -Path (Join-Path $Out 'bin') | Out-Null
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'bin/oow.js') -Destination (Join-Path $Out 'bin/oow.js')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'README.md') -Destination (Join-Path $Out 'README.md')
Copy-Item -LiteralPath (Join-Path $repoRoot 'LICENSE') -Destination (Join-Path $Out 'LICENSE')

# The executables: Windows PE files only (they start with "MZ").
foreach ($pair in @(@('win32-x64', $Amd64Exe), @('win32-arm64', $Arm64Exe))) {
    $arch, $exe = $pair
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "$exe is missing" }
    $head = [byte[]]::new(2)
    $stream = [IO.File]::OpenRead((Resolve-Path -LiteralPath $exe))
    try { [void]$stream.Read($head, 0, 2) } finally { $stream.Dispose() }
    if ($head[0] -ne 0x4D -or $head[1] -ne 0x5A) { throw "$exe is not a Windows executable" }
    $dir = Join-Path $Out "vendor/$arch"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item -LiteralPath $exe -Destination (Join-Path $dir 'oow.exe')
}

Write-Host "npm package out-of-windows $Version is in $Out (nothing was published)."
