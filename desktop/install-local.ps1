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

    On an update the new files are unpacked and checked first. The old folder is kept
    until the new registration succeeds and is put back if it fails.

.PARAMETER Msix
    Path to the package (default: dist\Fylvo.<arch>.msix next to this script).

.PARAMETER InstallDir
    Folder the app runs from. Do not delete it while the app is installed.

.EXAMPLE
    .\install-local.ps1

.NOTES
    Uninstall: Get-AppxPackage Fylvo.Desktop | Remove-AppxPackage
#>

param(
    [string]$Msix,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\FylvoDesktop")
)

$ErrorActionPreference = "Stop"

$PackageName = "Fylvo.Desktop"
$Publisher = "CN=Fylvo"

# Returns true if the folder holds a Fylvo Desktop package.
function Test-FylvoFolder([string]$Dir) {
    $Manifest = Join-Path $Dir "AppxManifest.xml"
    if (-not (Test-Path -LiteralPath $Manifest)) {
        return $false
    }
    $Identity = ([xml](Get-Content -LiteralPath $Manifest -Raw)).Package.Identity
    return $Identity.Name -eq $PackageName -and $Identity.Publisher -eq $Publisher
}

$DevMode = Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock" -ErrorAction SilentlyContinue
if ($DevMode.AllowDevelopmentWithoutDevLicense -ne 1) {
    Write-Error "Developer Mode is off. Turn it on in Settings > System > For developers, then run this script again."
    exit 1
}

if (-not $Msix) {
    $Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x64" }
    $Msix = Join-Path $PSScriptRoot "dist\Fylvo.$Arch.msix"
}
if (-not (Test-Path -LiteralPath $Msix)) {
    Write-Error "Package not found: $Msix. Build it first with .\build-msix.ps1"
    exit 1
}

$InstallDir = [IO.Path]::GetFullPath($InstallDir)
$NewDir = "$InstallDir.new"
$OldDir = "$InstallDir.old"

# Never replace a folder that is not a Fylvo Desktop install.
if ((Test-Path -LiteralPath $InstallDir) -and -not (Test-FylvoFolder $InstallDir)) {
    Write-Error "$InstallDir exists but is not a Fylvo Desktop install. Choose another -InstallDir."
    exit 1
}
foreach ($Dir in @($NewDir, $OldDir)) {
    if (Test-Path -LiteralPath $Dir) {
        if (-not (Test-FylvoFolder $Dir)) {
            Write-Error "$Dir exists and is not a Fylvo Desktop folder. Remove it by hand first."
            exit 1
        }
        Remove-Item -LiteralPath $Dir -Recurse -Force
    }
}

# 1. Unpack into a staging folder and check it. An MSIX is a zip file;
#    Expand-Archive only accepts the .zip extension.
$Zip = Join-Path $env:TEMP "fylvo-desktop-install.zip"
Copy-Item -LiteralPath $Msix -Destination $Zip -Force
Expand-Archive -LiteralPath $Zip -DestinationPath $NewDir
Remove-Item -LiteralPath $Zip -Force

# Packaging files that only belong in the .msix, a registered folder has none of them.
foreach ($Name in @("AppxBlockMap.xml", "AppxSignature.p7x", "[Content_Types].xml")) {
    Remove-Item -LiteralPath (Join-Path $NewDir $Name) -Force -ErrorAction SilentlyContinue
}

if (-not (Test-FylvoFolder $NewDir)) {
    Remove-Item -LiteralPath $NewDir -Recurse -Force
    Write-Error "$Msix is not a Fylvo Desktop package."
    exit 1
}

# 2. Stop the app and swap the folders. Explorer can start the app again or hold a file
#    open for a moment, so the rename is retried.
$Swapped = $false
for ($Try = 1; $Try -le 10 -and -not $Swapped; $Try++) {
    Get-Process fylvo-desktop -ErrorAction SilentlyContinue | Stop-Process -Force
    try {
        if (Test-Path -LiteralPath $InstallDir) {
            Rename-Item -LiteralPath $InstallDir -NewName (Split-Path $OldDir -Leaf)
        }
        $Swapped = $true
    } catch {
        Start-Sleep -Seconds 1
    }
}
if (-not $Swapped) {
    Remove-Item -LiteralPath $NewDir -Recurse -Force
    Write-Error "Could not replace $InstallDir, a file is still in use. Close Explorer windows of the sync folder and try again."
    exit 1
}
Rename-Item -LiteralPath $NewDir -NewName (Split-Path $InstallDir -Leaf)

# 3. Register. On failure put the old files back.
Write-Host "Registering Fylvo Desktop from $InstallDir ..." -ForegroundColor Cyan
try {
    Add-AppxPackage -Register (Join-Path $InstallDir "AppxManifest.xml") -ForceApplicationShutdown -ForceUpdateFromAnyVersion
} catch {
    if (Test-Path -LiteralPath $OldDir) {
        Remove-Item -LiteralPath $InstallDir -Recurse -Force
        Rename-Item -LiteralPath $OldDir -NewName (Split-Path $InstallDir -Leaf)
        Write-Host "Registration failed, the previous version was put back." -ForegroundColor Yellow
    }
    throw
}

if (Test-Path -LiteralPath $OldDir) {
    Remove-Item -LiteralPath $OldDir -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "Done. Start 'Fylvo Desktop' from the Start menu." -ForegroundColor Green
