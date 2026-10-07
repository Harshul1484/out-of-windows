<#
.SYNOPSIS
    Checks the Windows version information and application manifest embedded in oow.exe.

.DESCRIPTION
    Reads the VERSIONINFO resource (as Explorer's Details tab shows it) and the RT_MANIFEST
    resource of a built executable and fails unless both are present and as expected: product
    and file names, the version given with -Version, and a manifest that runs oow as the
    invoking user (asInvoker), declares Windows 10/11 and long path awareness, and leaves the
    ANSI code page alone. The file is loaded as data only, so builds for another architecture
    (arm64 on an x64 runner) are checked too. Nothing is executed or written.

.PARAMETER Exe
    Path of the executable to check.

.PARAMETER Version
    The version the build should carry, e.g. 1.2.3 or 0.1.0-dev.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string]$Exe,
    [Parameter(Mandatory)] [string]$Version
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$path = (Resolve-Path -LiteralPath $Exe).ProviderPath
$failures = [Collections.Generic.List[string]]::new()
function Expect([bool]$ok, [string]$message) {
    if (-not $ok) { $failures.Add($message) }
}

# Version information.
$vi = [Diagnostics.FileVersionInfo]::GetVersionInfo($path)
$expected = [ordered]@{
    ProductName      = 'out-of-windows'
    CompanyName      = 'Harshul1484'
    InternalName     = 'oow'
    OriginalFilename = 'oow.exe'
    FileVersion      = $Version
    ProductVersion   = $Version
}
foreach ($k in $expected.Keys) {
    Expect ($vi.$k -ceq $expected[$k]) "VersionInfo.$k is '$($vi.$k)', expected '$($expected[$k])'"
}
Expect (-not [string]::IsNullOrWhiteSpace($vi.FileDescription)) 'VersionInfo.FileDescription is empty'
Expect ($vi.LegalCopyright -like 'Copyright (c) * Harshul1484*') "VersionInfo.LegalCopyright is '$($vi.LegalCopyright)'"

# The numeric (fixed) version is the numeric prefix of the version string: 1.2.3-rc.1 -> 1.2.3.0.
if ($Version -match '^(\d+)\.(\d+)\.(\d+)') {
    $want = '{0}.{1}.{2}.0' -f $Matches[1], $Matches[2], $Matches[3]
    $fileFixed = '{0}.{1}.{2}.{3}' -f $vi.FileMajorPart, $vi.FileMinorPart, $vi.FileBuildPart, $vi.FilePrivatePart
    $prodFixed = '{0}.{1}.{2}.{3}' -f $vi.ProductMajorPart, $vi.ProductMinorPart, $vi.ProductBuildPart, $vi.ProductPrivatePart
    Expect ($fileFixed -eq $want) "fixed file version is $fileFixed, expected $want"
    Expect ($prodFixed -eq $want) "fixed product version is $prodFixed, expected $want"
} else {
    $failures.Add("'$Version' does not start with a numeric major.minor.patch version")
}

# Application manifest (RT_MANIFEST = 24, resource ID 1), read from the file mapped as data.
if (-not ('OowCheck.Resources' -as [type])) {
    Add-Type -Namespace OowCheck -Name Resources -MemberDefinition @'
[DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr LoadLibraryExW(string path, IntPtr file, uint flags);
[DllImport("kernel32.dll", SetLastError = true)]
public static extern bool FreeLibrary(IntPtr module);
[DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr FindResourceW(IntPtr module, IntPtr name, IntPtr type);
[DllImport("kernel32.dll", SetLastError = true)]
public static extern uint SizeofResource(IntPtr module, IntPtr resource);
[DllImport("kernel32.dll", SetLastError = true)]
public static extern IntPtr LoadResource(IntPtr module, IntPtr resource);
[DllImport("kernel32.dll")]
public static extern IntPtr LockResource(IntPtr data);
'@
}
$LOAD_LIBRARY_AS_DATAFILE = 0x2
$module = [OowCheck.Resources]::LoadLibraryExW($path, [IntPtr]::Zero, $LOAD_LIBRARY_AS_DATAFILE)
if ($module -eq [IntPtr]::Zero) {
    throw "could not load $path as a data file (Win32 error $([Runtime.InteropServices.Marshal]::GetLastWin32Error()))"
}
$manifestText = $null
try {
    $res = [OowCheck.Resources]::FindResourceW($module, [IntPtr]1, [IntPtr]24)
    if ($res -ne [IntPtr]::Zero) {
        $size = [OowCheck.Resources]::SizeofResource($module, $res)
        $ptr = [OowCheck.Resources]::LockResource([OowCheck.Resources]::LoadResource($module, $res))
        if ($size -gt 0 -and $ptr -ne [IntPtr]::Zero) {
            $bytes = [byte[]]::new($size)
            [Runtime.InteropServices.Marshal]::Copy($ptr, $bytes, 0, [int]$size)
            $manifestText = [Text.Encoding]::UTF8.GetString($bytes).TrimStart([char]0xFEFF)
        }
    }
} finally {
    [void][OowCheck.Resources]::FreeLibrary($module)
}

if (-not $manifestText) {
    $failures.Add('no application manifest (RT_MANIFEST #1) embedded')
} else {
    $xml = [xml]$manifestText
    $ns = [Xml.XmlNamespaceManager]::new($xml.NameTable)
    $ns.AddNamespace('v1', 'urn:schemas-microsoft-com:asm.v1')
    $ns.AddNamespace('v3', 'urn:schemas-microsoft-com:asm.v3')
    $ns.AddNamespace('c', 'urn:schemas-microsoft-com:compatibility.v1')
    $ns.AddNamespace('ws16', 'http://schemas.microsoft.com/SMI/2016/WindowsSettings')
    $ns.AddNamespace('ws19', 'http://schemas.microsoft.com/SMI/2019/WindowsSettings')

    $levels = @($xml.SelectNodes('//v3:requestedExecutionLevel', $ns))
    Expect ($levels.Count -eq 1) "manifest has $($levels.Count) requestedExecutionLevel elements, expected 1"
    foreach ($l in $levels) {
        Expect ($l.GetAttribute('level') -ceq 'asInvoker') "manifest requests execution level '$($l.GetAttribute('level'))', expected asInvoker"
        Expect ($l.GetAttribute('uiAccess') -ceq 'false') "manifest uiAccess is '$($l.GetAttribute('uiAccess'))', expected false"
    }
    $win10 = '{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}'
    $os = @($xml.SelectNodes('//c:supportedOS', $ns) | ForEach-Object { $_.GetAttribute('Id') })
    Expect ($os -contains $win10) "manifest does not declare Windows 10/11 ($win10) as a supported OS"
    $longPath = $xml.SelectSingleNode('//ws16:longPathAware', $ns)
    Expect ($null -ne $longPath -and $longPath.InnerText.Trim() -eq 'true') 'manifest does not declare longPathAware true'
    Expect ($null -eq $xml.SelectSingleNode('//ws19:activeCodePage', $ns)) 'manifest sets activeCodePage; oow relies on the system ANSI code page for .lnk strings'
}

if ($failures.Count -gt 0) {
    foreach ($f in $failures) { Write-Host "::error file=${Exe}::$f" }
    throw "$Exe failed $($failures.Count) metadata check(s)"
}
Write-Host "$Exe carries $($vi.ProductName) $($vi.ProductVersion) ($($vi.FileDescription)), $($vi.LegalCopyright); manifest: asInvoker, Windows 10/11, longPathAware."
