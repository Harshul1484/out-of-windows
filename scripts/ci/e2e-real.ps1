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

Write-Host '::group::analyze (read-only) and scan-speed diagnostic'
# Baseline: how long does the OS itself take to enumerate C:\ (cold cache)?
$sw = [Diagnostics.Stopwatch]::StartNew()
cmd /c "dir /s /a /-c C:\ > nul 2>&1"
$sw.Stop()
Write-Host ("baseline: dir /s C:\ (cold) took {0:n1}s" -f $sw.Elapsed.TotalSeconds)
foreach ($pass in 'warm 1', 'warm 2') {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  $an = & $Oow analyze C:\ --json --top 5 | ConvertFrom-Json
  $sw.Stop()
  if ($LASTEXITCODE -ne 0 -or $an.schema -ne 'oow.analyze/v1' -or $an.root.size -le 0) { throw 'analyze C:\ failed' }
  Write-Host ("oow analyze C:\ ({0}) took {1:n1}s: {2:n1} GB in {3:n0} files; {4} unreadable folders; {5} links not followed" -f `
      $pass, $sw.Elapsed.TotalSeconds, ($an.root.size / 1GB), $an.root.files, $an.scan_errors, $an.links)
}
$an.root.children | Select-Object -First 8 name, @{n = 'GB'; e = { '{0:n2}' -f ($_.size / 1GB) } } | Format-Table | Out-String | Write-Host
$lg = & $Oow analyze $env:USERPROFILE --large --min-size 1MB --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $lg.schema -ne 'oow.large/v1') { throw 'analyze --large failed' }
& $Oow analyze $env:USERPROFILE --large --top 5
Assert-Canaries
Write-Host '::endgroup::'

Write-Host '::group::status and processes (read-only)'
$st = & $Oow status --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $st.schema -ne 'oow.status/v1' -or $st.memory.total_bytes -le 0 -or $st.cpu.cores_percent.Count -lt 1 -or $st.processes.count -lt 10) {
  throw "status JSON incomplete: $($st | ConvertTo-Json -Depth 4 -Compress)"
}
Write-Host ("CPU {0:n1}% on {1} logical processors; memory {2:n1}%; disk active {3}%; {4} processes; GPUs: {5}" -f `
    $st.cpu.percent, $st.cpu.cores_percent.Count, $st.memory.used_percent, $st.disk.active_percent, $st.processes.count, ($st.gpus.name -join ', '))
$lines = & $Oow status --json --watch --count 2 --interval 500ms
if (@($lines).Count -ne 2) { throw "status --watch printed $(@($lines).Count) lines" }
& $Oow status
& $Oow processes --top 8
$ps = & $Oow processes --json --sort memory | ConvertFrom-Json
if ($ps.schema -ne 'oow.processes/v1' -or $ps.total -lt 10) { throw 'processes JSON incomplete' }
Write-Host '::endgroup::'

Write-Host '::group::purge and installer (real projects, Git, Recycle Bin)'
# Everything here is created by this section; oow only gets these folders as arguments.
function Set-OldTree([string] $dir) {
  Get-ChildItem -LiteralPath $dir -Recurse -Force -File | ForEach-Object { $_.CreationTime = $old; $_.LastWriteTime = $old }
}
$proj = Join-Path $env:USERPROFILE 'oow-ci-projects'
$gitc = @('-c', 'user.name=oow ci', '-c', 'user.email=ci@example.invalid', '-c', 'commit.gpgsign=false', '-c', 'core.hooksPath=NUL')
# webapp (Git): node_modules is ignored and old (removed); dist is committed (kept).
$web = Join-Path $proj 'webapp'
New-File (Join-Path $web 'package.json') 100
New-File (Join-Path $web 'src\index.js') 100
New-File (Join-Path $web 'node_modules\left-pad\index.js') 5000
New-File (Join-Path $web 'dist\bundle.js') 3000
Set-Content -LiteralPath (Join-Path $web '.gitignore') -Value 'node_modules/' -Encoding ascii
git -C $web @gitc init -q
git -C $web @gitc add package.json .gitignore src dist
git -C $web @gitc commit -q -m fixture
if ($LASTEXITCODE -ne 0) { throw 'could not create the fixture repository' }
# rustapp: Cargo build output (removed); rustkey: build output holding a signing key (kept).
New-File (Join-Path $proj 'rustapp\Cargo.toml') 100
New-File (Join-Path $proj 'rustapp\target\debug\app.exe') 200000
New-File (Join-Path $proj 'rustkey\Cargo.toml') 100
New-File (Join-Path $proj 'rustkey\target\release\signing.pfx') 2000
# fresh: installed just now (not selected); linked: node_modules holds a junction to Documents (kept).
New-File (Join-Path $proj 'fresh\package.json') 100
New-File (Join-Path $proj 'fresh\node_modules\x\x.js') 1000
New-File (Join-Path $proj 'linked\package.json') 100
New-File (Join-Path $proj 'linked\node_modules\real\r.js') 1000
cmd /c mklink /J (Join-Path $proj 'linked\node_modules\docs-link') "$docs" | Out-Null
foreach ($d in 'webapp\node_modules', 'webapp\dist', 'rustapp\target', 'rustkey\target', 'linked\node_modules\real') { Set-OldTree (Join-Path $proj $d) }
$purged = @((Join-Path $web 'node_modules'), (Join-Path $proj 'rustapp\target'))
$canaries += @((Join-Path $web 'dist\bundle.js'), (Join-Path $web 'src\index.js'), (Join-Path $proj 'rustkey\target\release\signing.pfx'),
  (Join-Path $proj 'fresh\node_modules\x\x.js'), (Join-Path $proj 'linked\node_modules\real\r.js'), (Join-Path $proj 'rustapp\Cargo.toml'))

$dry = & $Oow purge $proj --dry-run --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or -not $dry.dry_run) { throw "purge dry run failed ($LASTEXITCODE)" }
$arts = $dry.projects | ForEach-Object { $_.artifacts }
$arts | Format-Table path, kind, status, selected, bytes -AutoSize | Out-String | Write-Host
foreach ($p in $purged) {
  $a = $arts | Where-Object { $_.path -eq $p }
  if (-not $a -or $a.status -ne 'ready') { throw "expected $p to be selected: $($a | ConvertTo-Json -Compress)" }
}
if ($dry.summary.selected -ne 2) { throw "expected 2 selected artifacts, got $($dry.summary.selected)" }
foreach ($p in $purged) { if (-not (Test-Path -LiteralPath $p)) { throw "purge dry run removed $p" } }
& $Oow purge $proj | Out-Null
if ($LASTEXITCODE -ne 4) { throw "expected exit 4 from purge without --yes, got $LASTEXITCODE" }
$pr = & $Oow purge $proj --yes --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $pr.summary.removed -ne 2 -or $pr.summary.errors -ne 0) { throw "purge failed: $($pr.summary | ConvertTo-Json -Compress)" }
foreach ($p in $purged) { if (Test-Path -LiteralPath $p) { throw "artifact not removed: $p" } }
if (-not (Test-Path -LiteralPath (Join-Path $proj 'linked\node_modules\docs-link'))) { throw 'junction inside node_modules was removed' }
Assert-Canaries
Write-Host "Purged $($pr.summary.removed) folders, $($pr.summary.reclaimed_bytes) bytes."

# Installers: real file formats from the sandbox seed, copied to a folder in Downloads.
$sb = Join-Path $env:USERPROFILE 'oow-ci-sandbox'
& $Oow sandbox init $sb | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'sandbox init failed' }
$downloads = $null
try { $downloads = (New-Object -ComObject Shell.Application).NameSpace('shell:Downloads').Self.Path } catch {}
if (-not $downloads) { $downloads = Join-Path $env:USERPROFILE 'Downloads' }
$inst = Join-Path $downloads 'oow-ci-installers'
New-Item -ItemType Directory -Force -Path $inst | Out-Null
$seeded = Join-Path $sb 'C\Users\sandbox\Downloads'
$installerNames = 'FabrikamPlayerSetup-2.0.1.exe', 'NorthwindSync-3.1.msi'
$lookalikes = 'tailspin-terminal.exe', 'setup.pdf', 'notes.msi', 'old-report.msi', 'vacation-photos.zip'
foreach ($n in $installerNames + $lookalikes) { Copy-Item -LiteralPath (Join-Path $seeded $n) -Destination $inst; Set-Old (Join-Path $inst $n) }
foreach ($n in $lookalikes) { $canaries += (Join-Path $inst $n) }
$canaries += (Join-Path $inst 'NorthwindSync-3.1.msi')
# Fabrikam Player is "installed" (a registration only), so its installer is preselected.
$fabKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\OOWCIFabrikam'
New-Item -Path $fabKey -Force | Out-Null
New-ItemProperty -Path $fabKey -Name DisplayName -Value 'Fabrikam Player' | Out-Null
New-ItemProperty -Path $fabKey -Name DisplayVersion -Value '2.0.1' | Out-Null
New-ItemProperty -Path $fabKey -Name Publisher -Value 'Fabrikam, Inc.' | Out-Null
New-ItemProperty -Path $fabKey -Name UninstallString -Value ('"' + (Join-Path $env:SystemRoot 'System32\cmd.exe') + '" /c exit') | Out-Null

$idry = & $Oow installer $inst --dry-run --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw "installer dry run exited $LASTEXITCODE" }
$idry.installers | Format-Table name, type, product, @{n = 'installed'; e = { $_.installed.status } }, status -AutoSize | Out-String | Write-Host
if (@($idry.installers).Count -ne 2) { throw "expected 2 installers, got $(@($idry.installers).Count)" }
$fab = $idry.installers | Where-Object { $_.name -eq 'FabrikamPlayerSetup-2.0.1.exe' }
if (-not $fab.selected -or $fab.installed.status -ne 'installed') { throw "Fabrikam installer not preselected: $($fab | ConvertTo-Json -Compress)" }
& $Oow installer $inst | Out-Null
if ($LASTEXITCODE -ne 4) { throw "expected exit 4 from installer without --yes, got $LASTEXITCODE" }
$ir = & $Oow installer $inst --yes --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $ir.summary.recycled -ne 1 -or $ir.summary.errors -ne 0) { throw "installer run failed: $($ir.summary | ConvertTo-Json -Compress)" }
if (Test-Path -LiteralPath (Join-Path $inst 'FabrikamPlayerSetup-2.0.1.exe')) { throw 'installer not moved to the Recycle Bin' }
Remove-Item -Path $fabKey -Recurse -Force
Assert-Canaries
Write-Host '::endgroup::'

Write-Host '::group::history'
& $Oow history
Write-Host '::endgroup::'

$again = & $Oow clean --dry-run --json | ConvertFrom-Json
Write-Host "Second scan finds $($again.summary.reclaimable_files) files (only items that appeared since)."
Write-Host 'End-to-end cleanup passed: junk removed, every canary intact.'
