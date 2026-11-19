<#
.SYNOPSIS
    Install or update Fylvo Desktop on this PC from the unsigned MSIX.

.DESCRIPTION
    Windows does not install unsigned packages that contain COM servers (the Explorer
    integration). Without a signing certificate the only way is a development registration:
    the package is unpacked into a fixed folder and registered from there. This needs
    Windows Developer Mode (Settings > System > For developers).

    The Cloudreve Store app is not touched. Fylvo Desktop uses its own package name,
    data folder (%USERPROFILE%\.fylvo), deep link (fylvo://) and sync root IDs.

.PARAMETER Msix
    Path to the package (default: dist\Fylvo.x64.msix next to this script).

.PARAMETER InstallDir
    Folder the app runs from. Do not delete it while the app is installed.

.EXAMPLE
    .\install-local.ps1

.NOTES
    Uninstall: Get-AppxPackage Fylvo.Desktop | Remove-AppxPackage
#>

param(
    [string]$Msix = (Join-Path $PSScriptRoot "dist\Fylvo.x64.msix"),
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\FylvoDesktop")
)

$ErrorActionPreference = "Stop"

$DevMode = Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock" -ErrorAction SilentlyContinue
if ($DevMode.AllowDevelopmentWithoutDevLicense -ne 1) {
    Write-Error "Developer Mode is off. Turn it on in Settings > System > For developers, then run this script again."
    exit 1
}

if (-not (Test-Path $Msix)) {
    Write-Error "Package not found: $Msix. Build it first with .\build-msix.ps1 -Arch x64"
    exit 1
}

# The app must not run while its files are replaced.
Get-Process fylvo-desktop -ErrorAction SilentlyContinue | Stop-Process -Force

# An MSIX is a zip file; Expand-Archive only accepts the .zip extension.
$Zip = Join-Path $env:TEMP "fylvo-desktop-install.zip"
Copy-Item $Msix $Zip -Force
if (Test-Path $InstallDir) {
    Remove-Item $InstallDir -Recurse -Force
}
Expand-Archive $Zip -DestinationPath $InstallDir
Remove-Item $Zip -Force

# Packaging files that only belong in the .msix, a registered folder has none of them.
foreach ($Name in @("AppxBlockMap.xml", "AppxSignature.p7x", "[Content_Types].xml")) {
    Remove-Item -LiteralPath (Join-Path $InstallDir $Name) -Force -ErrorAction SilentlyContinue
}

Write-Host "Registering Fylvo Desktop from $InstallDir ..." -ForegroundColor Cyan
Add-AppxPackage -Register (Join-Path $InstallDir "AppxManifest.xml") -ForceApplicationShutdown -ForceUpdateFromAnyVersion

Write-Host "Done. Start 'Fylvo Desktop' from the Start menu." -ForegroundColor Green
