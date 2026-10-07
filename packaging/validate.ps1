<#
.SYNOPSIS
    Validates the winget, Scoop and Chocolatey packages that packaging/render.ps1 wrote.

.DESCRIPTION
    Fails unless every file is well-formed, carries the final package identities (winget
    Harshul1484.oow by publisher Harshul1484, Scoop oow, Chocolatey oow), the given version,
    and download URLs and SHA-256 values that match the zips in -Dist:

    - winget: each manifest is validated against the official JSON schema of the manifest
      version it declares (tools/wingetcheck, schemas pinned to a microsoft/winget-cli
      commit), and with `winget validate` too when winget is installed;
    - Scoop: valid JSON with the required fields, and autoupdate URLs that produce the same
      download URLs for this version;
    - Chocolatey: valid nuspec XML, an install script that parses and points at the amd64
      zip with its SHA-256, and a .nupkg built from them.

    Requires Go (for tools/wingetcheck) and network access to fetch the winget schemas.
    Nothing is written outside the system temp folder, and nothing is published.

.PARAMETER Dir
    The folder render.ps1 wrote.

.PARAMETER Version
    The release version without the v, e.g. 1.2.3.

.PARAMETER Dist
    The folder holding the release zips the packages point at.

.PARAMETER Repo
    The GitHub repository (owner/name) whose release hosts the zips.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Dir,
    [Parameter(Mandatory)] [string]$Version,
    [Parameter(Mandatory)] [string]$Dist,
    [Parameter(Mandatory)] [string]$Repo
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# The final package identities (#11).
$wingetId = 'Harshul1484.oow'
$wingetPublisher = 'Harshul1484'
$scoopName = 'oow'
$chocoId = 'oow'

$Dir = (Resolve-Path -LiteralPath $Dir).ProviderPath
$failures = [Collections.Generic.List[string]]::new()
function Expect([bool]$ok, [string]$message) {
    if (-not $ok) { $failures.Add($message) }
}
function Get-Prop($object, [string]$name) {
    if ($null -ne $object -and $object.PSObject.Properties[$name]) { return $object.$name }
    return $null
}

$expected = @{}
foreach ($arch in 'amd64', 'arm64') {
    $name = "oow-$Version-windows-$arch.zip"
    $zip = Join-Path $Dist $name
    if (-not (Test-Path -LiteralPath $zip -PathType Leaf)) { throw "$zip is missing" }
    $expected[$arch] = @{
        Url = "https://github.com/$Repo/releases/download/v$Version/$name"
        Sha = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

# No template leftovers anywhere.
foreach ($f in Get-ChildItem -LiteralPath $Dir -Recurse -File | Where-Object { $_.Extension -ne '.nupkg' }) {
    $text = [IO.File]::ReadAllText($f.FullName)
    Expect ($text -cnotmatch '<[A-Z][A-Z0-9_]*>') "$($f.Name): unfilled placeholder"
    Expect ($text -notmatch 'Template, not published') "$($f.Name): still marked as a template"
}

# --- winget -------------------------------------------------------------------------------
$wingetDir = Join-Path $Dir ('winget/manifests/{0}/{1}/{2}' -f $wingetId.Substring(0, 1).ToLowerInvariant(), ($wingetId -replace '\.', '/'), $Version)
$names = @("$wingetId.yaml", "$wingetId.installer.yaml", "$wingetId.locale.en-US.yaml")
$files = @($names | ForEach-Object { Join-Path $wingetDir $_ })
$present = @(Get-ChildItem -LiteralPath $wingetDir -File -ErrorAction SilentlyContinue | ForEach-Object { $_.Name } | Sort-Object)
Expect (($present -join ',') -eq (($names | Sort-Object) -join ',')) "winget: expected exactly $($names -join ', ') in $wingetDir, found $($present -join ', ')"
if (@($files | Where-Object { -not (Test-Path -LiteralPath $_) }).Count -eq 0) {
    $tools = Join-Path (Split-Path -Parent $PSScriptRoot) 'tools'
    $json = & go run -C $tools ./wingetcheck @files
    if ($LASTEXITCODE -ne 0) {
        $failures.Add('winget: schema validation failed (see above)')
    } else {
        # Assign first: Windows PowerShell 5.1 does not enumerate a JSON array.
        $parsed = ($json -join "`n") | ConvertFrom-Json
        $docs = @($parsed)
        $verDoc, $instDoc, $locDoc = $docs
        foreach ($i in 0..2) {
            $d = $docs[$i]
            Expect ((Get-Prop $d 'PackageIdentifier') -ceq $wingetId) "winget $($names[$i]): PackageIdentifier is '$(Get-Prop $d 'PackageIdentifier')', expected $wingetId"
            Expect ((Get-Prop $d 'PackageVersion') -ceq $Version) "winget $($names[$i]): PackageVersion is '$(Get-Prop $d 'PackageVersion')', expected $Version"
            Expect ((Get-Prop $d 'ManifestVersion') -ceq (Get-Prop $verDoc 'ManifestVersion')) "winget $($names[$i]): ManifestVersion differs between the files"
            # The editor schema hint must name the same type and version as the file.
            $hint = "winget-manifest.$(Get-Prop $d 'ManifestType').$(Get-Prop $d 'ManifestVersion').schema.json"
            Expect ([IO.File]::ReadAllText($files[$i]).Contains($hint)) "winget $($names[$i]): the yaml-language-server schema comment does not reference $hint"
        }
        Expect ((Get-Prop $verDoc 'ManifestType') -ceq 'version') "winget $($names[0]): ManifestType is not version"
        Expect ((Get-Prop $instDoc 'ManifestType') -ceq 'installer') "winget $($names[1]): ManifestType is not installer"
        Expect ((Get-Prop $locDoc 'ManifestType') -ceq 'defaultLocale') "winget $($names[2]): ManifestType is not defaultLocale"
        Expect ((Get-Prop $verDoc 'DefaultLocale') -ceq 'en-US' -and (Get-Prop $locDoc 'PackageLocale') -ceq 'en-US') 'winget: DefaultLocale and PackageLocale must both be en-US'
        Expect ((Get-Prop $locDoc 'Publisher') -ceq $wingetPublisher) "winget: Publisher is '$(Get-Prop $locDoc 'Publisher')', expected $wingetPublisher"
        Expect ((Get-Prop $locDoc 'ReleaseNotesUrl') -ceq "https://github.com/Harshul1484/out-of-windows/releases/tag/v$Version") "winget: ReleaseNotesUrl does not point at v$Version"
        Expect ((Get-Prop $instDoc 'InstallerType') -ceq 'zip' -and (Get-Prop $instDoc 'NestedInstallerType') -ceq 'portable') 'winget: expected a zip with a portable nested installer'
        $nested = @(Get-Prop $instDoc 'NestedInstallerFiles')
        Expect ($nested.Count -eq 1 -and (Get-Prop $nested[0] 'RelativeFilePath') -ceq 'oow.exe' -and (Get-Prop $nested[0] 'PortableCommandAlias') -ceq 'oow') 'winget: the nested installer must be oow.exe with the command alias oow'
        $byArch = @{}
        foreach ($inst in @(Get-Prop $instDoc 'Installers')) { $byArch[[string](Get-Prop $inst 'Architecture')] = $inst }
        Expect ((($byArch.Keys | Sort-Object) -join ',') -eq 'arm64,x64') "winget: installers are for $(($byArch.Keys | Sort-Object) -join ', '), expected arm64 and x64"
        foreach ($pair in @(@('x64', 'amd64'), @('arm64', 'arm64'))) {
            $inst = $byArch[$pair[0]]
            $want = $expected[$pair[1]]
            Expect ((Get-Prop $inst 'InstallerUrl') -ceq $want.Url) "winget $($pair[0]): InstallerUrl is '$(Get-Prop $inst 'InstallerUrl')', expected $($want.Url)"
            Expect ([string](Get-Prop $inst 'InstallerSha256') -eq $want.Sha) "winget $($pair[0]): InstallerSha256 does not match $($want.Url)"
        }
    }
    if (Get-Command winget -ErrorAction SilentlyContinue) {
        & winget validate --manifest $wingetDir --disable-interactivity
        Expect ($LASTEXITCODE -eq 0) "winget validate failed (exit code $LASTEXITCODE)"
    } else {
        Write-Host 'winget is not installed here; the manifests were checked against the JSON schemas only.'
    }
}

# --- Scoop --------------------------------------------------------------------------------
$scoopFile = Join-Path $Dir "scoop/$scoopName.json"
if (-not (Test-Path -LiteralPath $scoopFile)) {
    $failures.Add("scoop: $scoopFile is missing (the file name is the app name, $scoopName)")
} else {
    $scoop = $null
    try { $scoop = [IO.File]::ReadAllText($scoopFile) | ConvertFrom-Json } catch { $failures.Add("scoop: not valid JSON: $($_.Exception.Message)") }
    if ($scoop) {
        Expect ((Get-Prop $scoop 'version') -ceq $Version) "scoop: version is '$(Get-Prop $scoop 'version')', expected $Version"
        foreach ($k in 'description', 'homepage', 'license', 'checkver') {
            Expect (-not [string]::IsNullOrWhiteSpace([string](Get-Prop $scoop $k))) "scoop: $k is missing"
        }
        Expect ((Get-Prop $scoop 'bin') -ceq 'oow.exe') "scoop: bin is '$(Get-Prop $scoop 'bin')', expected oow.exe"
        Expect ($null -eq (Get-Prop $scoop '##')) 'scoop: the template note ("##") is still there'
        $arches = Get-Prop $scoop 'architecture'
        $auto = Get-Prop (Get-Prop $scoop 'autoupdate') 'architecture'
        foreach ($pair in @(@('64bit', 'amd64'), @('arm64', 'arm64'))) {
            $a = Get-Prop $arches $pair[0]
            $want = $expected[$pair[1]]
            Expect ((Get-Prop $a 'url') -ceq $want.Url) "scoop $($pair[0]): url is '$(Get-Prop $a 'url')', expected $($want.Url)"
            Expect ([string](Get-Prop $a 'hash') -cmatch '^[0-9a-f]{64}$') "scoop $($pair[0]): hash is not a lower-case SHA-256"
            Expect ((Get-Prop $a 'hash') -ceq $want.Sha) "scoop $($pair[0]): hash does not match $($want.Url)"
            # autoupdate must produce the same URL for this version.
            $template = [string](Get-Prop (Get-Prop $auto $pair[0]) 'url')
            $fromTemplate = $template.Replace('$version', $Version).Replace('https://github.com/Harshul1484/out-of-windows/', "https://github.com/$Repo/")
            Expect ($fromTemplate -ceq $want.Url) "scoop $($pair[0]): autoupdate url '$template' does not produce $($want.Url)"
        }
        Expect ((Get-Prop (Get-Prop (Get-Prop $scoop 'autoupdate') 'hash') 'url') -ceq '$baseurl/SHA256SUMS') 'scoop: autoupdate hashes must come from the release''s SHA256SUMS'
    }
}

# --- Chocolatey ---------------------------------------------------------------------------
$chocoDir = Join-Path $Dir 'chocolatey'
$nuspecFile = Join-Path $chocoDir "$chocoId.nuspec"
$installFile = Join-Path $chocoDir 'tools/chocolateyinstall.ps1'
$nuspec = $null
try { $nuspec = [xml][IO.File]::ReadAllText($nuspecFile) } catch { $failures.Add("chocolatey: $nuspecFile is not valid XML: $($_.Exception.Message)") }
if ($nuspec) {
    $meta = $nuspec.package.metadata
    Expect ($meta.id -ceq $chocoId) "chocolatey: id is '$($meta.id)', expected $chocoId"
    Expect ($meta.version -ceq $Version) "chocolatey: version is '$($meta.version)', expected $Version"
    foreach ($k in 'title', 'authors', 'description', 'summary', 'projectUrl', 'licenseUrl', 'packageSourceUrl') {
        Expect (-not [string]::IsNullOrWhiteSpace([string](Get-Prop $meta $k))) "chocolatey: $k is missing"
    }
}
if (-not (Test-Path -LiteralPath $installFile)) {
    $failures.Add("chocolatey: $installFile is missing")
} else {
    $tokens = $null; $errors = $null
    [void][Management.Automation.Language.Parser]::ParseFile($installFile, [ref]$tokens, [ref]$errors)
    Expect ($errors.Count -eq 0) "chocolatey: chocolateyinstall.ps1 does not parse: $($errors | ForEach-Object { $_.Message })"
    $installText = [IO.File]::ReadAllText($installFile)
    $want = $expected['amd64']
    Expect ($installText -match "(?m)^\s*url64bit\s*=\s*'([^']*)'" -and $Matches[1] -ceq $want.Url) "chocolatey: url64bit is not $($want.Url)"
    Expect ($installText -match "(?m)^\s*checksum64\s*=\s*'([^']*)'" -and $Matches[1] -ceq $want.Sha) "chocolatey: checksum64 does not match $($want.Url)"
    Expect ($installText -match "(?m)^\s*checksumType64\s*=\s*'sha256'") 'chocolatey: checksumType64 must be sha256'
}
$nupkgs = @(Get-ChildItem -LiteralPath $chocoDir -Filter '*.nupkg' -File -ErrorAction SilentlyContinue)
Expect ($nupkgs.Count -eq 1) "chocolatey: expected one .nupkg in $chocoDir, found $($nupkgs.Count)"
if ($nupkgs.Count -eq 1) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($nupkgs[0].FullName)
    try {
        $entries = @($zip.Entries | ForEach-Object { $_.FullName -replace '\\', '/' })
        Expect ($entries -contains "$chocoId.nuspec") "chocolatey: the .nupkg has no $chocoId.nuspec"
        $packed = $zip.Entries | Where-Object { ($_.FullName -replace '\\', '/') -eq 'tools/chocolateyinstall.ps1' } | Select-Object -First 1
        if (-not $packed) {
            $failures.Add('chocolatey: the .nupkg has no tools/chocolateyinstall.ps1')
        } else {
            $reader = [IO.StreamReader]::new($packed.Open())
            try { $packedText = $reader.ReadToEnd() } finally { $reader.Dispose() }
            Expect ($packedText -ceq [IO.File]::ReadAllText($installFile)) 'chocolatey: the packed chocolateyinstall.ps1 differs from the rendered one'
        }
        $packedSpec = $zip.Entries | Where-Object { $_.FullName -eq "$chocoId.nuspec" } | Select-Object -First 1
        if ($packedSpec) {
            $reader = [IO.StreamReader]::new($packedSpec.Open())
            try { $packedMeta = ([xml]$reader.ReadToEnd()).package.metadata } finally { $reader.Dispose() }
            Expect ($packedMeta.id -ceq $chocoId -and $packedMeta.version -ceq $Version) "chocolatey: the .nupkg is $($packedMeta.id) $($packedMeta.version), expected $chocoId $Version"
        }
    } finally {
        $zip.Dispose()
    }
}

if ($failures.Count -gt 0) {
    foreach ($f in $failures) { Write-Host "::error::$f" }
    throw "$($failures.Count) package check(s) failed"
}
Write-Host "Packages for $Version are valid: winget $wingetId, Scoop $scoopName, Chocolatey $chocoId."
