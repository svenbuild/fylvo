<#
.SYNOPSIS
    Build and register the Fylvo Desktop app for development testing.

.DESCRIPTION
    This script builds the Tauri application, copies package/ and the binary to
    target\dev-package, fills in version and architecture in that copy of AppxManifest.xml
    and registers it for development testing with shell integration. The files in package/
    are never changed. "cargo clean" removes the registered folder; run this script again
    afterwards.

.PARAMETER Version
    Override the version from tauri.conf.json. Format: "X.Y.Z" (will be converted to "X.Y.Z.0")

.PARAMETER SkipBuild
    Skip the cargo build step (useful if binary is already built)

.EXAMPLE
    .\dev-install.ps1
    # Build and register with version from tauri.conf.json

.EXAMPLE
    .\dev-install.ps1 -Version "0.2.0"
    # Build and register with custom version

.EXAMPLE
    .\dev-install.ps1 -SkipBuild
    # Register without rebuilding (use existing binary)
#>

param(
    [string]$Version,
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

# Paths
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$TauriConfigPath = Join-Path $ScriptDir "src-tauri\tauri.conf.json"
$SourcePackageDir = Join-Path $ScriptDir "package"
$PackageDir = Join-Path $ScriptDir "target\dev-package"
$ManifestPath = Join-Path $PackageDir "AppxManifest.xml"

# Detect architecture
$Arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") {
        "arm64"
    } else {
        "x64"
    }
} else {
    Write-Error "32-bit Windows is not supported"
    exit 1
}

Write-Host "Detected architecture: $Arch" -ForegroundColor Cyan

# Get version from tauri.conf.json if not provided
if (-not $Version) {
    Write-Host "Reading version from tauri.conf.json..." -ForegroundColor Cyan
    $TauriConfig = Get-Content $TauriConfigPath -Raw | ConvertFrom-Json
    $Version = $TauriConfig.version
}

# Convert to 4-part version for MSIX
$MsixVersion = if ($Version -match '^\d+\.\d+\.\d+$') {
    "$Version.0"
} elseif ($Version -match '^\d+\.\d+\.\d+\.\d+$') {
    $Version
} else {
    Write-Error "Invalid version format: $Version. Expected X.Y.Z or X.Y.Z.W"
    exit 1
}

Write-Host "Version: $MsixVersion" -ForegroundColor Cyan

# Build the application
if (-not $SkipBuild) {
    Write-Host "`nBuilding Tauri application (release)..." -ForegroundColor Green

    Push-Location $ScriptDir
    try {
        cargo tauri build --no-bundle
        if ($LASTEXITCODE -ne 0) {
            Write-Error "Build failed with exit code $LASTEXITCODE"
            exit $LASTEXITCODE
        }
    } finally {
        Pop-Location
    }
} else {
    Write-Host "`nSkipping build step..." -ForegroundColor Yellow
}

# Determine binary path
$BinaryPath = Join-Path $ScriptDir "target\release\fylvo-desktop.exe"
if (-not (Test-Path $BinaryPath)) {
    Write-Error "Binary not found at: $BinaryPath"
    exit 1
}

# Copy package files and binary to the dev package directory
Write-Host "`nCopying package files and binary to $PackageDir..." -ForegroundColor Green
# Windows registers a package version from one folder only.
$Existing = Get-AppxPackage Fylvo.Desktop
if ($Existing -and $Existing.InstallLocation -ne $PackageDir) {
    Write-Error "Fylvo Desktop is registered from $($Existing.InstallLocation). Remove it first: Get-AppxPackage Fylvo.Desktop | Remove-AppxPackage"
    exit 1
}
Get-Process fylvo-desktop -ErrorAction SilentlyContinue | Stop-Process -Force
if (Test-Path $PackageDir) {
    Remove-Item $PackageDir -Recurse -Force
}
Copy-Item $SourcePackageDir -Destination $PackageDir -Recurse
Copy-Item $BinaryPath -Destination $PackageDir -Force

# Update AppxManifest.xml
Write-Host "Updating AppxManifest.xml..." -ForegroundColor Green

$ManifestContent = Get-Content $ManifestPath -Raw -Encoding UTF8

# Replace placeholders
$ManifestContent = $ManifestContent -replace '__ARCH__', $Arch
$ManifestContent = $ManifestContent -replace '__VERSION__', $MsixVersion

# Write back with UTF-8 BOM (required for AppxManifest.xml)
$Utf8Bom = New-Object System.Text.UTF8Encoding $true
[System.IO.File]::WriteAllText($ManifestPath, $ManifestContent, $Utf8Bom)

# Register the package
Write-Host "`nRegistering package..." -ForegroundColor Green
Write-Host "Running: Add-AppxPackage -Register `"$ManifestPath`"" -ForegroundColor DarkGray

Add-AppxPackage -Register $ManifestPath

Write-Host "`nDone! Package registered successfully." -ForegroundColor Green
Write-Host "You can now test shell integration features." -ForegroundColor Cyan
