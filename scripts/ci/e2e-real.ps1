<#
.SYNOPSIS
  End-to-end test of real cleanup on a disposable CI VM.

.DESCRIPTION
  Plants "canary" files that must never be touched and old junk that must be
  removed, runs a real `oow clean --yes`, and fails if any canary is changed or
  any junk survives. NEVER run this on a machine you care about: it performs a
  real cleanup of the current user's and Windows' Temp folders.
#>
param(
  [Parameter(Mandatory)] [string] $Oow
)

$ErrorActionPreference = 'Stop'
if (-not $env:CI) { throw 'Refusing to run outside CI: this script performs a real cleanup.' }

$Oow = (Resolve-Path $Oow).Path
$old = (Get-Date).AddDays(-10)

function Set-Old([string] $path) {
  $item = Get-Item -LiteralPath $path -Force
  $item.CreationTime = $old
  $item.LastWriteTime = $old
}

function New-File([string] $path, [int] $size = 1000) {
  New-Item -ItemType Directory -Force -Path (Split-Path $path) | Out-Null
  [IO.File]::WriteAllBytes($path, [byte[]]::new($size))
}

# Precious files: user content and application data that must survive.
$docs = [Environment]::GetFolderPath('MyDocuments')
$desktop = [Environment]::GetFolderPath('Desktop')
$canaries = @(
  (Join-Path $docs 'oow-canary.txt'),
  (Join-Path $desktop 'oow-canary.txt'),
  (Join-Path $env:LOCALAPPDATA 'oow-canary-app\settings.json'),
  (Join-Path $env:ProgramData 'oow-canary\data.bin')
)
foreach ($c in $canaries) { New-File $c; Set-Old $c }

# Old junk in the real user Temp (Known Folder based) and Windows Temp.
$userTemp = Join-Path $env:LOCALAPPDATA 'Temp'
$winTemp = Join-Path $env:SystemRoot 'Temp'
$junkDir = Join-Path $userTemp 'oow-ci-junk'
$junk = @(
  (Join-Path $junkDir 'old1.log'),
  (Join-Path $junkDir 'nested\old2.bin'),
  (Join-Path $userTemp 'oow-ci-old.tmp'),
  (Join-Path $winTemp 'oow-ci-old.log')
)
foreach ($j in $junk) { New-File $j 4096; Set-Old $j }
Set-Old (Join-Path $junkDir 'nested')
Set-Old $junkDir

# Recent temp file: must be kept (younger than 24 hours).
$recent = Join-Path $userTemp 'oow-ci-recent.tmp'
New-File $recent

# Junction inside Temp pointing at Documents: must never be followed.
$link = Join-Path $userTemp 'oow-ci-link'
cmd /c mklink /J "$link" "$docs" | Out-Null

function Assert-Canaries {
  foreach ($c in $canaries + $recent) {
    if (-not (Test-Path -LiteralPath $c)) { throw "PRECIOUS FILE REMOVED: $c" }
  }
  if (-not (Test-Path -LiteralPath $link)) { throw 'junction was removed' }
}

Write-Host '::group::oow version'
& $Oow version
Write-Host '::endgroup::'

Write-Host '::group::dry run (text)'
& $Oow clean --dry-run
if ($LASTEXITCODE -ne 0) { throw "dry run exited $LASTEXITCODE" }
Write-Host '::endgroup::'

$dry = & $Oow clean --dry-run --json | ConvertFrom-Json
if (-not $dry.dry_run -or $dry.summary.executed) { throw 'dry run JSON is wrong' }
foreach ($j in $junk) { if (-not (Test-Path -LiteralPath $j)) { throw "dry run removed $j" } }
Assert-Canaries

Write-Host '::group::refuses without confirmation'
& $Oow clean | Out-Null
if ($LASTEXITCODE -ne 4) { throw "expected exit 4 without --yes, got $LASTEXITCODE" }
Write-Host '::endgroup::'

Write-Host '::group::real cleanup (elevated runner)'
$json = & $Oow clean --yes --json
if ($LASTEXITCODE -ne 0) { throw "clean exited $LASTEXITCODE" }
$res = $json | ConvertFrom-Json
$res.rules | Format-Table id, status, files, bytes, @{n = 'removed'; e = { $_.result.removed } }, @{n = 'skipped'; e = { $_.result.skipped } } -AutoSize | Out-String | Write-Host
$res.summary | Format-List | Out-String | Write-Host
Write-Host '::endgroup::'

Assert-Canaries
foreach ($j in $junk) { if (Test-Path -LiteralPath $j) { throw "junk not removed: $j" } }
if (Test-Path -LiteralPath $junkDir) { throw "emptied old folder not removed: $junkDir" }
if ($res.summary.errors -ne 0) { throw "cleanup reported $($res.summary.errors) unexpected errors" }
if (-not $res.summary.executed -or $res.summary.removed -lt $junk.Count) { throw 'cleanup did not run' }

Write-Host '::group::history'
& $Oow history
Write-Host '::endgroup::'

$again = & $Oow clean --dry-run --json | ConvertFrom-Json
Write-Host "Second scan finds $($again.summary.reclaimable_files) files (only items that appeared since)."
Write-Host 'End-to-end cleanup passed: junk removed, every canary intact.'
