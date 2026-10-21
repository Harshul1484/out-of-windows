<#
.SYNOPSIS
    Plays a short oow demo in a simulated Windows layout, for recording the README's GIF.

.DESCRIPTION
    Creates a new sandbox (a simulated Windows layout with seed files, see `oow sandbox init`)
    and runs oow against it through OOW_SANDBOX, typing each command visibly first so a screen
    recorder such as ScreenToGif captures a clean take:

        oow clean --dry-run     what cleanup would remove
        oow clean --yes         the cleanup, verified item by item
        oow                     the home screen, which you then drive by hand (q quits)

    Every command runs in sandbox mode: only files inside the new sandbox folder are cleaned.
    The script never deletes anything itself; remove old demo folders under %TEMP% when you
    no longer need them.

    Recording: a terminal of about 100 x 30 characters, a dark theme, then start the recorder
    and run this script.

.PARAMETER Oow
    The oow to run: the installed `oow`, or bin\oow.exe from a source build.

.PARAMETER Dir
    The sandbox folder to create. It must not exist yet.

.PARAMETER TypeDelayMs
    Delay between typed characters.

.PARAMETER PauseSeconds
    Pause after each command's output.
#>
[CmdletBinding()]
param(
    [string]$Oow = 'oow',
    [string]$Dir = (Join-Path $env:TEMP ('oow-demo-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))),
    [int]$TypeDelayMs = 45,
    [int]$PauseSeconds = 3
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (Test-Path -LiteralPath $Dir) { throw "$Dir already exists: pass a new -Dir" }
$exe = (Get-Command $Oow -ErrorAction Stop).Source

& $exe sandbox init $Dir | Out-Null
if ($LASTEXITCODE -ne 0) { throw "oow sandbox init $Dir failed (exit $LASTEXITCODE)" }

function Show-Typed([string]$command) {
    Write-Host -NoNewline -ForegroundColor DarkGray '> '
    foreach ($ch in $command.ToCharArray()) {
        Write-Host -NoNewline $ch
        Start-Sleep -Milliseconds $TypeDelayMs
    }
    Start-Sleep -Milliseconds 400
    Write-Host ''
}

$previous = $env:OOW_SANDBOX
$env:OOW_SANDBOX = $Dir
try {
    Clear-Host
    foreach ($step in @(@('clean', '--dry-run'), @('clean', '--yes'))) {
        Show-Typed ('oow ' + ($step -join ' '))
        & $exe @step
        Start-Sleep -Seconds $PauseSeconds
        Write-Host ''
    }
    Show-Typed 'oow'
    & $exe
} finally {
    $env:OOW_SANDBOX = $previous
}
