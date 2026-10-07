<#
.SYNOPSIS
    Installs oow (out-of-windows) for the current user.

.DESCRIPTION
    Downloads the oow.exe build for this computer's architecture from the project's GitHub
    releases, verifies its SHA-256 against the release's SHA256SUMS file, installs it to
    %LOCALAPPDATA%\Programs\oow and adds that folder to your user PATH if it is missing.

    No administrator rights are needed. Nothing is written unless the checksum matches: the
    download is verified in memory first, and any failure stops the installer (it never falls
    back to an unverified download). It writes only to %LOCALAPPDATA%\Programs\oow (an earlier
    oow.exe there is replaced; a running one is renamed to oow.exe.old, which oow removes when
    it next starts) and to the user PATH. Uninstall later with `oow remove`.

    Run it from PowerShell:
        irm https://raw.githubusercontent.com/Harshul1484/out-of-windows/main/scripts/install.ps1 | iex
    or, for a specific version:
        & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Harshul1484/out-of-windows/main/scripts/install.ps1))) -Version 1.2.3

    For a private repository, set GITHUB_TOKEN or GH_TOKEN to a token that can read it.

.PARAMETER Version
    The release to install, e.g. 1.2.3 (default: the latest release).
#>
[CmdletBinding()]
param(
    [string]$Version = 'latest'
)

function Install-Oow {
    [CmdletBinding()]
    param([string]$Version)

    Set-StrictMode -Version Latest
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # the progress bar slows downloads in Windows PowerShell

    $repo = 'Harshul1484/out-of-windows'
    $name = 'oow'
    $api = 'https://api.github.com'

    # Windows PowerShell 5.1 may not enable TLS 1.2 by default.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    # Architecture of Windows itself, not of this PowerShell process (an x64 PowerShell can
    # run emulated on ARM64).
    $arch = $null
    try {
        $arch = (Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment' -Name PROCESSOR_ARCHITECTURE).PROCESSOR_ARCHITECTURE
    } catch { }
    if (-not $arch) {
        $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    }
    switch ($arch) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "$name supports 64-bit Windows on x64 or ARM64; this computer reports '$arch'." }
    }

    $token = if ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN } elseif ($env:GH_TOKEN) { $env:GH_TOKEN } else { $null }
    $headers = @{ 'Accept' = 'application/vnd.github+json'; 'X-GitHub-Api-Version' = '2022-11-28' }
    if ($token) { $headers['Authorization'] = "Bearer $token" }
    $userAgent = "$name-installer"

    # Resolve the release.
    if ($Version -eq 'latest' -or [string]::IsNullOrWhiteSpace($Version)) {
        $releaseUrl = "$api/repos/$repo/releases/latest"
    } else {
        $Version = $Version.Trim() -replace '^v', ''
        if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') { throw "'$Version' is not a version like 1.2.3." }
        $releaseUrl = "$api/repos/$repo/releases/tags/v$Version"
    }
    try {
        $release = Invoke-RestMethod -Uri $releaseUrl -Headers $headers -UserAgent $userAgent -UseBasicParsing
    } catch {
        $status = $null
        if ($_.Exception.PSObject.Properties['Response'] -and $_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
        if ($status -eq 404) {
            $hint = if ($token) { 'check that the token can read the repository' } else { 'if the repository is private, set GITHUB_TOKEN or GH_TOKEN' }
            throw "No release found at $releaseUrl ($hint)."
        }
        throw "Could not query GitHub for the release: $($_.Exception.Message)"
    }
    $tag = [string]$release.tag_name
    $resolved = $tag -replace '^v', ''
    $assetName = "$name-$resolved-windows-$arch.exe"

    $asset = @($release.assets | Where-Object { $_.name -eq $assetName })
    $sumsAsset = @($release.assets | Where-Object { $_.name -eq 'SHA256SUMS' })
    if ($asset.Count -ne 1) { throw "Release $tag has no $assetName (windows/$arch). Nothing was installed." }
    if ($sumsAsset.Count -ne 1) { throw "Release $tag has no SHA256SUMS file, so its files cannot be verified. Nothing was installed." }

    # Download into memory. With a token, use the API asset URL (the only one that works for
    # private repositories); the Authorization header is not forwarded on the redirect to
    # GitHub's storage host.
    function Get-AssetBytes($a) {
        if ($token) {
            $h = @{ 'Accept' = 'application/octet-stream'; 'Authorization' = "Bearer $token" }
            $r = Invoke-WebRequest -Uri $a.url -Headers $h -UserAgent $userAgent -UseBasicParsing
        } else {
            $r = Invoke-WebRequest -Uri $a.browser_download_url -UserAgent $userAgent -UseBasicParsing
        }
        return , $r.RawContentStream.ToArray()
    }

    Write-Host "Downloading $name $resolved for windows/$arch..."
    $sumsText = [Text.Encoding]::ASCII.GetString((Get-AssetBytes $sumsAsset[0]))
    $expected = $null
    foreach ($line in ($sumsText -split "`n")) {
        if ($line.TrimEnd("`r") -match '^([0-9a-fA-F]{64})\s+\*?(.+)$' -and $Matches[2] -eq $assetName) {
            if ($expected -and $expected -ne $Matches[1].ToLowerInvariant()) { throw "SHA256SUMS lists two different checksums for $assetName. Nothing was installed." }
            $expected = $Matches[1].ToLowerInvariant()
        }
    }
    if (-not $expected) { throw "SHA256SUMS has no entry for $assetName. Nothing was installed." }

    $bytes = Get-AssetBytes $asset[0]
    if ($bytes.Length -ne [int64]$asset[0].size) {
        throw "Downloaded $($bytes.Length) bytes, the release lists $($asset[0].size). Nothing was installed."
    }
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $actual = ([BitConverter]::ToString($sha.ComputeHash($bytes)) -replace '-', '').ToLowerInvariant() } finally { $sha.Dispose() }
    if ($actual -ne $expected) {
        throw "Checksum mismatch for ${assetName}: expected $expected, got $actual. Nothing was installed."
    }
    Write-Host "SHA-256 verified: $actual"

    # Install. LocalApplicationData comes from the Known Folder API, not from %LOCALAPPDATA%.
    $local = [Environment]::GetFolderPath('LocalApplicationData')
    if (-not $local) { throw 'Could not find your local application data folder.' }
    $dir = Join-Path (Join-Path $local 'Programs') $name
    $exe = Join-Path $dir "$name.exe"
    $new = "$exe.new"
    $old = "$exe.old"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    [IO.File]::WriteAllBytes($new, $bytes)
    if (Test-Path -LiteralPath $exe) {
        # A running oow.exe can be renamed but not overwritten. oow removes the .old file the
        # next time it starts.
        if (-not (Test-Path -LiteralPath $old)) { Move-Item -LiteralPath $exe -Destination $old }
    }
    try {
        Move-Item -LiteralPath $new -Destination $exe -Force
    } catch {
        throw "Could not put the new $name.exe in place ($($_.Exception.Message)). Close any running $name and run the installer again."
    }
    if (-not (Test-Path -LiteralPath $exe)) {
        throw "$exe disappeared right after it was written. Microsoft Defender may have flagged it: this is a known false positive (see the README). The SHA-256 above matched the release."
    }
    $check = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($check -ne $expected) { throw "$exe does not match the verified download (got $check). Do not run it; reinstall." }

    # Add the folder to the user PATH (HKCU\Environment), only if it is not there yet. Other
    # entries are kept exactly as they are, including unexpanded %VARIABLES%.
    $pathAdded = $false
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    if (-not $key) { $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment') }
    try {
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kind = if ($key.GetValueNames() -contains 'Path') { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $present = $false
        foreach ($entry in ($raw -split ';')) {
            $e = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"')).TrimEnd('\')
            if ($e -and ($e -ieq $dir.TrimEnd('\'))) { $present = $true }
        }
        if (-not $present) {
            $value = if ([string]::IsNullOrEmpty($raw)) { $dir } elseif ($raw.EndsWith(';')) { $raw + $dir } else { "$raw;$dir" }
            $key.SetValue('Path', $value, $kind)
            $pathAdded = $true
        }
    } finally {
        $key.Close()
    }
    if ($pathAdded) {
        # Tell Explorer and new terminals that the environment changed.
        try {
            if (-not ('OowInstall.Native' -as [type])) {
                Add-Type -Namespace OowInstall -Name Native -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
            }
            $result = [UIntPtr]::Zero
            [void][OowInstall.Native]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
        } catch { }
        if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }
    }

    Write-Host ''
    Write-Host "$name $resolved installed: $exe"
    if ($pathAdded) {
        Write-Host "Added $dir to your user PATH (open a new terminal if '$name' is not found)."
    }
    Write-Host ''
    Write-Host 'Next steps:'
    Write-Host "  $name --help             what it can do"
    Write-Host "  $name clean --dry-run    see what cleanup would remove, without changing anything"
    Write-Host "  $name update             update later"
    Write-Host "  $name remove             uninstall"
}

Install-Oow -Version $Version
