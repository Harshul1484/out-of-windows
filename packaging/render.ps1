<#
.SYNOPSIS
    Renders the winget, Scoop and Chocolatey packages for one release.

.DESCRIPTION
    Fills the templates in packaging/ with the release version, the download URLs of the
    release's zips and their SHA-256, and writes ready-to-submit files to -Out:

        winget/manifests/h/Harshul1484/oow/<version>/   the three manifests, laid out as in
                                                         microsoft/winget-pkgs
        scoop/oow.json                                   the Scoop manifest
        chocolatey/oow.nuspec, chocolatey/tools/         the Chocolatey package source
        chocolatey/oow.<version>.nupkg                   the package, built with `choco pack`

    The SHA-256 values are computed from the zips in -Dist, which must be the exact files the
    release publishes (the release workflow runs this right after building them, before
    SHA256SUMS is written). Nothing is uploaded or submitted anywhere: publishing is a manual
    step described in packaging/README.md. Check the result with packaging/validate.ps1.

.PARAMETER Version
    The release version without the v, e.g. 1.2.3.

.PARAMETER Dist
    The folder holding oow-<Version>-windows-amd64.zip and oow-<Version>-windows-arm64.zip.

.PARAMETER Out
    The output folder. It must not exist yet, or be empty.

.PARAMETER Repo
    The GitHub repository (owner/name) whose release hosts the zips.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Version,
    [Parameter(Mandatory)] [string]$Dist,
    [Parameter(Mandatory)] [string]$Out,
    [Parameter(Mandatory)] [string]$Repo
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ($Version -notmatch '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$') {
    throw "'$Version' is not a semantic version like 1.2.3"
}
if ($Repo -notmatch '^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$') { throw "'$Repo' is not a GitHub repository like owner/name" }
if ((Test-Path -LiteralPath $Out) -and @(Get-ChildItem -LiteralPath $Out -Force).Count -gt 0) {
    throw "$Out already exists and is not empty"
}

# Placeholder values. Every placeholder in a template must be filled.
$values = @{ '<VERSION>' = $Version }
foreach ($arch in 'amd64', 'arm64') {
    $name = "oow-$Version-windows-$arch.zip"
    $zip = Join-Path $Dist $name
    if (-not (Test-Path -LiteralPath $zip -PathType Leaf)) { throw "$zip is missing" }
    $values["<URL_$($arch.ToUpperInvariant())>"] = "https://github.com/$Repo/releases/download/v$Version/$name"
    $values["<SHA256_$($arch.ToUpperInvariant())>"] = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
}
# Chocolatey installs the amd64 build (ARM64 Windows runs it emulated).
$values['<URL>'] = $values['<URL_AMD64>']
$values['<SHA256>'] = $values['<SHA256_AMD64>']

$utf8 = [Text.UTF8Encoding]::new($false)

function Expand-Template {
    param([string]$Template, [string]$Destination, [hashtable]$Override = @{})
    $text = [IO.File]::ReadAllText((Join-Path $PSScriptRoot $Template))
    # Drop the "Template, not published" notice line.
    $text = [regex]::Replace($text, '(?m)^.*Template, not published: see packaging/README\.md\..*\n', '')
    $map = $values.Clone()
    foreach ($k in $Override.Keys) { $map[$k] = $Override[$k] }
    foreach ($k in $map.Keys) { $text = $text.Replace($k, $map[$k]) }
    $left = @([regex]::Matches($text, '<[A-Z][A-Z0-9_]*>') | ForEach-Object { $_.Value } | Sort-Object -Unique)
    if ($left.Count -gt 0) { throw "${Template}: unfilled placeholders $($left -join ', ')" }
    $dir = Split-Path -Parent $Destination
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    [IO.File]::WriteAllText($Destination, $text, $utf8)
    Write-Host "rendered $Destination"
}

# winget: microsoft/winget-pkgs layout, manifests/<first letter>/<Publisher>/<Name>/<version>/.
$versionTemplate = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'winget/oow.yaml'))
if ($versionTemplate -notmatch '(?m)^PackageIdentifier:\s*(\S+)\s*$') { throw 'winget/oow.yaml has no PackageIdentifier' }
$id = $Matches[1]
$wingetDir = Join-Path $Out ('winget/manifests/{0}/{1}/{2}' -f $id.Substring(0, 1).ToLowerInvariant(), ($id -replace '\.', '/'), $Version)
# winget-pkgs writes installer hashes in upper case.
$upper = @{
    '<SHA256_AMD64>' = $values['<SHA256_AMD64>'].ToUpperInvariant()
    '<SHA256_ARM64>' = $values['<SHA256_ARM64>'].ToUpperInvariant()
}
foreach ($t in Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot 'winget') -Filter 'oow*.yaml') {
    Expand-Template "winget/$($t.Name)" (Join-Path $wingetDir ($t.Name -replace '^oow\.', "$id.")) $upper
}

# Scoop: the manifest's file name is the app name.
Expand-Template 'scoop/oow.json' (Join-Path $Out 'scoop/oow.json')

# Chocolatey: package source, then the .nupkg.
$chocoDir = Join-Path $Out 'chocolatey'
Expand-Template 'chocolatey/oow.nuspec' (Join-Path $chocoDir 'oow.nuspec') @{ '<version>0.0.0</version>' = "<version>$Version</version>" }
Expand-Template 'chocolatey/tools/chocolateyinstall.ps1' (Join-Path $chocoDir 'tools/chocolateyinstall.ps1')
if (-not (Get-Command choco -ErrorAction SilentlyContinue)) {
    throw "Chocolatey (choco) is needed to build the .nupkg (GitHub's Windows runners have it); the other files are in $Out"
}
& choco pack (Join-Path $chocoDir 'oow.nuspec') "--outputdirectory=$chocoDir" --limit-output
if ($LASTEXITCODE -ne 0) { throw "choco pack failed (exit code $LASTEXITCODE)" }

Write-Host "Packages for oow $Version are in $Out (nothing was published)."
