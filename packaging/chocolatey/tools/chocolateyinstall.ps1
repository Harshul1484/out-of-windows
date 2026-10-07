# Template, not published: see packaging/README.md.
# Chocolatey verifies the SHA-256 below before unpacking and refuses a mismatch.
$ErrorActionPreference = 'Stop'
$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition

$packageArgs = @{
    packageName    = $env:ChocolateyPackageName
    unzipLocation  = $toolsDir
    url64bit       = '<URL>'
    checksum64     = '<SHA256>'
    checksumType64 = 'sha256'
}
Install-ChocolateyZipPackage @packageArgs
