@echo off
rem Installs oow for the current user from Command Prompt. No administrator rights needed.
rem It runs install.ps1 (next to this file, or the copy published in the repository), which
rem downloads the release, verifies its SHA-256 and installs to %LOCALAPPDATA%\Programs\oow.
rem
rem Usage:  install.cmd               latest release
rem         install.cmd -Version 1.2.3
rem
rem The execution policy is bypassed for this one PowerShell process only.
setlocal
set "OOW_INSTALL_PS1=%~dp0install.ps1"
if exist "%OOW_INSTALL_PS1%" (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%OOW_INSTALL_PS1%" %*
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((Invoke-RestMethod -UseBasicParsing 'https://raw.githubusercontent.com/Harshul1484/out-of-windows/main/scripts/install.ps1'))) %*"
)
exit /b %ERRORLEVEL%
