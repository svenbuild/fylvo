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
if ($InstallDir -eq [IO.Path]::GetPathRoot($InstallDir)) {
    Write-Error "-InstallDir must not be a drive root."
    exit 1
}
$InstallDir = $InstallDir.TrimEnd('\', '/')
$NewDir = "$InstallDir.new"
$OldDir = "$InstallDir.old"

# A previous run stopped between the two renames: put the old version back first.
if (-not (Test-Path -LiteralPath $InstallDir) -and (Test-Path -LiteralPath $OldDir) -and (Test-FylvoFolder $OldDir)) {
    Rename-Item -LiteralPath $OldDir -NewName (Split-Path $InstallDir -Leaf)
    Write-Host "Restored the previous version from $OldDir." -ForegroundColor Yellow
}

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
try {
    Copy-Item -LiteralPath $Msix -Destination $Zip -Force
    Expand-Archive -LiteralPath $Zip -DestinationPath $NewDir

    # Packaging files that only belong in the .msix, a registered folder has none of them.
    foreach ($Name in @("AppxBlockMap.xml", "AppxSignature.p7x", "[Content_Types].xml")) {
        Remove-Item -LiteralPath (Join-Path $NewDir $Name) -Force -ErrorAction SilentlyContinue
    }

    if (-not (Test-FylvoFolder $NewDir)) {
        throw "$Msix is not a Fylvo Desktop package."
    }
} catch {
    Remove-Item -LiteralPath $NewDir -Recurse -Force -ErrorAction SilentlyContinue
    throw
} finally {
    Remove-Item -LiteralPath $Zip -Force -ErrorAction SilentlyContinue
}

# 2. Stop the app and move the current version to .old. Explorer can start the app again
#    or hold a file open for a moment, so the rename is retried.
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

# 3. Move the new version in and register it. On failure put the old version back.
$Promoted = $false
try {
    Rename-Item -LiteralPath $NewDir -NewName (Split-Path $InstallDir -Leaf)
    $Promoted = $true
    Write-Host "Registering Fylvo Desktop from $InstallDir ..." -ForegroundColor Cyan
    Add-AppxPackage -Register (Join-Path $InstallDir "AppxManifest.xml") -ForceApplicationShutdown -ForceUpdateFromAnyVersion
} catch {
    $InstallError = $_
    try {
        if ($Promoted) {
            Remove-Item -LiteralPath $InstallDir -Recurse -Force
        }
        if (Test-Path -LiteralPath $OldDir) {
            Rename-Item -LiteralPath $OldDir -NewName (Split-Path $InstallDir -Leaf)
            Write-Host "Installation failed, the previous version was put back." -ForegroundColor Yellow
        }
    } catch {
        Write-Warning "Could not put the previous version back. It is in $OldDir; running this script again restores it."
    }
    throw $InstallError
}

if (Test-Path -LiteralPath $OldDir) {
    Remove-Item -LiteralPath $OldDir -Recurse -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $OldDir) {
        Write-Warning "Could not fully remove $OldDir. Delete it by hand before the next update."
    }
}

Write-Host "Done. Start 'Fylvo Desktop' from the Start menu." -ForegroundColor Green
