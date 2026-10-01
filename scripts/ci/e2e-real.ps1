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

# A Chrome profile: its cache must go, its credentials and data must stay.
$chromeProfile = Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data\OOW CI'
New-File (Join-Path $chromeProfile 'Preferences') 200
$junk += (Join-Path $chromeProfile 'Cache\Cache_Data\f_0ci001')
New-File $junk[-1] 300000
foreach ($name in 'Login Data', 'Cookies', 'Bookmarks') {
  $c = Join-Path $chromeProfile $name
  New-File $c 2000
  $canaries += $c
}
# A folder with a Cache but no Preferences file is not a profile.
$notProfile = Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data\OOW CI not a profile\Cache\keep.bin'
New-File $notProfile 100
$canaries += $notProfile

# npm download cache (default-on) next to npx installs (never touched).
$junk += (Join-Path $env:LOCALAPPDATA 'npm-cache\_cacache\oow-ci\entry')
New-File $junk[-1] 50000
$npx = Join-Path $env:LOCALAPPDATA 'npm-cache\_npx\oow-ci\package.json'
New-File $npx 100
$canaries += $npx

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

Write-Host '::group::Recycle Bin (opt-in, Shell API)'
$recycled = Join-Path $env:TEMP 'oow-ci-recycled.txt'
New-File $recycled 12345
Add-Type -AssemblyName Microsoft.VisualBasic
[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile($recycled, 'OnlyErrorDialogs', 'SendToRecycleBin')
$before = & $Oow clean --rule windows.recycle-bin --dry-run --json | ConvertFrom-Json
if ($before.rules[0].status -ne 'ready' -or $before.rules[0].files -lt 1) { throw "Recycle Bin not detected: $($before.rules[0] | ConvertTo-Json -Compress)" }
$rb = & $Oow clean --rule windows.recycle-bin --yes --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $rb.summary.removed -lt 1 -or $rb.summary.errors -ne 0) { throw "Recycle Bin not emptied: $($rb.summary | ConvertTo-Json -Compress)" }
$after = & $Oow clean --rule windows.recycle-bin --dry-run --json | ConvertFrom-Json
if ($after.rules[0].status -ne 'empty') { throw "Recycle Bin still has items: $($after.rules[0] | ConvertTo-Json -Compress)" }
Assert-Canaries
Write-Host "Recycle Bin emptied: $($rb.summary.removed) item(s), $($rb.summary.reclaimed_bytes) bytes."
Write-Host '::endgroup::'

Write-Host '::group::uninstall a registered app (real registry, Shell, Recycle Bin)'
$appDir = Join-Path $env:LOCALAPPDATA 'Programs\OOW CI App'
$appData = Join-Path $env:APPDATA 'OOW CI App'
$unrelated = Join-Path $env:APPDATA 'OOW CI Unrelated\data.txt'
$regKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\OOWCIApp'
New-File (Join-Path $appDir 'app.exe') 50000
New-File (Join-Path $appDir 'cache\keep.bin') 20000
New-File (Join-Path $appData 'settings.json') 3000
New-File $unrelated 100
$canaries += $unrelated
# The app's own uninstaller removes its program and registry entry but
# leaves a cache folder and its AppData behind, like many real ones.
Set-Content -LiteralPath (Join-Path $appDir 'uninstall.cmd') -Encoding ascii -Value @"
@echo off
del /f /q "%~dp0app.exe"
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\OOWCIApp" /f >nul
"@
New-Item -Path $regKey -Force | Out-Null
New-ItemProperty -Path $regKey -Name DisplayName -Value 'OOW CI App' | Out-Null
New-ItemProperty -Path $regKey -Name DisplayVersion -Value '1.0' | Out-Null
New-ItemProperty -Path $regKey -Name Publisher -Value 'OOW CI' | Out-Null
New-ItemProperty -Path $regKey -Name InstallLocation -Value $appDir | Out-Null
New-ItemProperty -Path $regKey -Name DisplayIcon -Value (Join-Path $appDir 'app.exe') | Out-Null
New-ItemProperty -Path $regKey -Name UninstallString -Value ('"' + (Join-Path $appDir 'uninstall.cmd') + '"') | Out-Null

$list = & $Oow uninstall --list --json | ConvertFrom-Json
Write-Host "Inventory: $($list.apps.Count) apps; package managers: $($list.package_managers | ConvertTo-Json -Compress)"
if (-not ($list.apps | Where-Object { $_.name -eq 'OOW CI App' })) { throw 'test app not found in the inventory' }
$dry = & $Oow uninstall 'OOW CI App' --dry-run --json | ConvertFrom-Json
if (-not $dry.dry_run -or -not (Test-Path (Join-Path $appDir 'app.exe'))) { throw 'uninstall dry run changed something' }

$un = & $Oow uninstall 'OOW CI App' --yes --wait 60s --json | ConvertFrom-Json
$r = $un.results[0]
$r | ConvertTo-Json -Depth 6 | Write-Host
if ($LASTEXITCODE -ne 0 -or -not $r.outcome.removed) { throw "uninstall failed: $($r.error)" }
if (Test-Path $regKey) { throw 'uninstall entry still present' }
if ((Test-Path $appDir) -or (Test-Path $appData)) { throw 'leftovers not moved to the Recycle Bin' }
if ($r.recycled.recycled.Count -ne 2) { throw "expected 2 recycled leftovers, got $($r.recycled.recycled.Count)" }
Assert-Canaries
Write-Host '::endgroup::'

Write-Host '::group::leftovers scan (read-only)'
& $Oow leftovers --dry-run
if ($LASTEXITCODE -ne 0) { throw "leftovers dry run exited $LASTEXITCODE" }
Write-Host '::endgroup::'

Write-Host '::group::history'
& $Oow history
Write-Host '::endgroup::'

$again = & $Oow clean --dry-run --json | ConvertFrom-Json
Write-Host "Second scan finds $($again.summary.reclaimable_files) files (only items that appeared since)."
Write-Host 'End-to-end cleanup passed: junk removed, every canary intact.'
