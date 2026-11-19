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

# Returns true if the folder holds a Fylvo Desktop package. A missing, unreadable or broken
# manifest counts as "not Fylvo", so such a folder is kept rather than deleted. So does a
# junction or symbolic link: deleting through it would delete the files it points to.
function Test-FylvoFolder([string]$Dir) {
    $Item = Get-Item -LiteralPath $Dir -Force -ErrorAction SilentlyContinue
    if (-not $Item -or ($Item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        return $false
    }
    $Manifest = Join-Path $Dir "AppxManifest.xml"
    if (-not (Test-Path -LiteralPath $Manifest)) {
        return $false
    }
    try {
        $Identity = ([xml](Get-Content -LiteralPath $Manifest -Raw)).Package.Identity
    } catch {
        return $false
    }
    return $Identity.Name -eq $PackageName -and $Identity.Publisher -eq $Publisher
}

# Runs $Action, stopping the app before each try. Explorer can start the app again or hold a
# file open for a moment, so it is tried up to 10 times. Returns false if every try failed.
function Invoke-WithAppStopped([scriptblock]$Action) {
    for ($Try = 1; $Try -le 10; $Try++) {
        try {
            Get-Process fylvo-desktop -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
            & $Action
            return $true
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    return $false
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

# Moves a folder out of the way to a new <InstallDir>.trash-* name. Nothing is deleted here.
function Move-Aside([string]$Dir) {
    $Aside = "$InstallDir.trash-$(Get-Date -Format yyyyMMddHHmmssfff)"
    Rename-Item -LiteralPath $Dir -NewName (Split-Path $Aside -Leaf)
    return $Aside
}

# Marker file in a moved-aside folder that may be deleted. It is removed last, so a later run
# can finish a deletion that was cut short.
$DeleteMarker = ".fylvo-delete"

# Deletes a folder that was moved aside, but only if it is a Fylvo folder. Anything else is
# kept with a warning, so files that are not ours are never deleted.
function Remove-Aside([string]$Aside, [bool]$WasFylvo) {
    if (-not $WasFylvo) {
        Write-Warning "$Aside was moved out of the way and kept because it is not a Fylvo Desktop folder. Check it and delete it by hand."
        return
    }
    $Marker = Join-Path $Aside $DeleteMarker
    try {
        if (-not (Test-Path -LiteralPath $Marker)) {
            [IO.File]::WriteAllText($Marker, "")
        }
        Get-ChildItem -LiteralPath $Aside -Force | Where-Object { $_.Name -ne $DeleteMarker } |
            Remove-Item -Recurse -Force
        Remove-Item -LiteralPath $Aside -Recurse -Force
    } catch {
        Write-Warning "Could not fully remove $Aside yet. The next run tries again."
    }
}

# Finish deletions that an earlier run started but could not complete.
$AsidePattern = "$(Split-Path $InstallDir -Leaf).trash-*"
$AsideParent = Split-Path $InstallDir -Parent
if (Test-Path -LiteralPath $AsideParent) {
    Get-ChildItem -LiteralPath $AsideParent -Directory -Filter $AsidePattern |
        Where-Object { -not ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } |
        Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName $DeleteMarker) } |
        ForEach-Object { Remove-Aside $_.FullName $true }
}

# Marker file written into .old once the new version is registered. A .old with it is only a
# backup that could not be removed, not the last working version.
$RetiredMarker = ".fylvo-retired"

# Returns the version in a folder's manifest, or $null.
function Get-FolderVersion([string]$Dir) {
    try {
        return ([xml](Get-Content -LiteralPath (Join-Path $Dir "AppxManifest.xml") -Raw)).Package.Identity.Version
    } catch {
        return $null
    }
}

# A .old is stale, not the last working version, if a run marked it after registering, or if
# Windows has exactly the version in the install folder registered from there (the run was
# cut off between registering and writing the mark).
function Test-StaleBackup {
    if (Test-Path -LiteralPath (Join-Path $OldDir $RetiredMarker)) {
        return $true
    }
    $Registered = Get-AppxPackage -Name $PackageName -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $Registered -or -not (Test-FylvoFolder $InstallDir)) {
        return $false
    }
    $Version = Get-FolderVersion $InstallDir
    return ($Registered.InstallLocation.TrimEnd('\', '/') -eq $InstallDir) -and
        ($Version -and $Registered.Version.ToString() -eq $Version) -and
        ((Get-FolderVersion $OldDir) -ne $Version)
}

# A backup in .old means a previous run did not finish, unless it is stale (see above). For an
# unfinished run .old stays the backup for this run, and whatever is in the install folder
# (maybe a half installed new version) is moved aside instead of being backed up.
$Unfinished = Test-Path -LiteralPath $OldDir
$StaleBackup = $Unfinished -and (Test-FylvoFolder $OldDir) -and (Test-StaleBackup)
if ($StaleBackup) {
    $Unfinished = $false
}
if ($Unfinished -and -not (Test-FylvoFolder $OldDir)) {
    Write-Error "$OldDir exists and is not a Fylvo Desktop folder. Remove it by hand first."
    exit 1
}
if (-not $Unfinished -and (Test-Path -LiteralPath $InstallDir) -and -not (Test-FylvoFolder $InstallDir)) {
    Write-Error "$InstallDir exists but is not a Fylvo Desktop install. Choose another -InstallDir."
    exit 1
}
if (Test-Path -LiteralPath $NewDir) {
    if (-not (Test-FylvoFolder $NewDir)) {
        Write-Error "$NewDir exists and is not a Fylvo Desktop folder. Remove it by hand first."
        exit 1
    }
    Remove-Item -LiteralPath $NewDir -Recurse -Force
}

# 1. Unpack into a staging folder and check it. Nothing installed is touched before this works.
#    An MSIX is a zip file; Expand-Archive only accepts the .zip extension.
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

# A stale backup is removed now that the new package is known to be good.
if ($StaleBackup) {
    $Retired = $null
    if (Invoke-WithAppStopped { $script:Retired = Move-Aside $OldDir }) {
        Remove-Aside $Retired $true
    } else {
        Remove-Item -LiteralPath $NewDir -Recurse -Force
        Write-Error "Could not remove the old backup $OldDir, a file is still in use. Close Explorer windows of the sync folder and try again."
        exit 1
    }
}

# 2. Stop the app and clear the install folder: back it up to .old, or after an unfinished run
#    move it aside because .old already holds the backup.
$Aside = $null
$AsideWasFylvo = $false
$Swapped = Invoke-WithAppStopped {
    if (Test-Path -LiteralPath $InstallDir) {
        if ($Unfinished) {
            $script:AsideWasFylvo = Test-FylvoFolder $InstallDir
            $script:Aside = Move-Aside $InstallDir
        } else {
            Rename-Item -LiteralPath $InstallDir -NewName (Split-Path $OldDir -Leaf)
        }
    }
}
if (-not $Swapped) {
    Remove-Item -LiteralPath $NewDir -Recurse -Force
    Write-Error "Could not replace $InstallDir, a file is still in use. Close Explorer windows of the sync folder and try again."
    exit 1
}
if ($Aside) {
    Remove-Aside $Aside $AsideWasFylvo
}

# 3. Move the new version in and register it. On failure move it aside and put .old back.
$Promoted = $false
try {
    Rename-Item -LiteralPath $NewDir -NewName (Split-Path $InstallDir -Leaf)
    $Promoted = $true
    Write-Host "Registering Fylvo Desktop from $InstallDir ..." -ForegroundColor Cyan
    Add-AppxPackage -Register (Join-Path $InstallDir "AppxManifest.xml") -ForceApplicationShutdown -ForceUpdateFromAnyVersion
} catch {
    $InstallError = $_
    $Failed = $null
    $RolledBack = Invoke-WithAppStopped {
        if ($Promoted -and (Test-Path -LiteralPath $InstallDir)) {
            $script:Failed = Move-Aside $InstallDir
        }
        if (Test-Path -LiteralPath $OldDir) {
            Rename-Item -LiteralPath $OldDir -NewName (Split-Path $InstallDir -Leaf)
        }
    }
    if ($RolledBack) {
        Write-Host "Installation failed, the previous version was put back." -ForegroundColor Yellow
    } else {
        Write-Warning "Could not put the previous version back, it is still in $OldDir. Run this script again: it keeps that version as the backup and tries the installation again."
    }
    if ($Failed) {
        Remove-Aside $Failed $true
    }
    throw $InstallError
}

# 4. Retire the backup: mark it as no longer needed, then move it aside. A leftover .old
#    without the mark always means an unfinished run.
if (Test-Path -LiteralPath $OldDir) {
    try {
        [IO.File]::WriteAllText((Join-Path $OldDir $RetiredMarker), "")
    } catch {
        # Without the mark the next run still recognizes the registered version (Test-StaleBackup).
    }
    $Retired = $null
    $Moved = Invoke-WithAppStopped { $script:Retired = Move-Aside $OldDir }
    if ($Moved) {
        Remove-Aside $Retired $true
    } else {
        if (Test-Path -LiteralPath (Join-Path $OldDir $RetiredMarker)) {
            Write-Warning "Could not remove the old backup $OldDir yet. The next run removes it."
        } else {
            Write-Warning "Could not retire $OldDir. Delete it by hand, otherwise the next run keeps this older version as its backup."
        }
    }
}

Write-Host "Done. Start 'Fylvo Desktop' from the Start menu." -ForegroundColor Green
