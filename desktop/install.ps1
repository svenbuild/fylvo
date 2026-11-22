<#
.SYNOPSIS
    Install Fylvo Desktop from the signed MSIX, or move an installation over from Developer
    Mode. After that the app updates itself (desktop/UPDATER.md).

.DESCRIPTION
    Runs as the normal user. Steps:
    1. The package is checked first: signed by the Fylvo Desktop certificate, not changed,
       name Fylvo.Desktop and publisher CN=Fylvo. Nothing is touched before that.
    2. The Fylvo Desktop certificate (fylvo-desktop.cer next to this script) is trusted for
       packages on this PC (LocalMachine\TrustedPeople). Only this step asks for admin rights.
    3. An installation from Developer Mode (install-local.ps1) is replaced: the app is stopped,
       the development registration removed (the data in %USERPROFILE%\.fylvo stays) and the
       signed package installed. If that fails, the old registration is put back. The old
       folder is deleted only after the signed package is installed.

.PARAMETER Msix
    Path to the signed package (default: Fylvo.<arch>.msix next to this script, else dist\).

.PARAMETER ForceUpdateFromAnyVersion
    Install even if the package is older than the installed version (manual repair with the
    last good package).

.EXAMPLE
    .\install.ps1 -Msix .\Fylvo-Desktop-0.3.0.0-x64.msix

.NOTES
    Uninstall: Get-AppxPackage Fylvo.Desktop | Remove-AppxPackage
#>

param(
    [string]$Msix,
    [switch]$ForceUpdateFromAnyVersion
)

$ErrorActionPreference = "Stop"

$PackageName = "Fylvo.Desktop"
$Publisher = "CN=Fylvo"
# SHA-1 thumbprint of the Fylvo Desktop signing certificate (fylvo-desktop.cer).
$Signer = "16A089077B5D78CCAA8996BD51EE523CF0264D79"
$DevFolder = Join-Path $env:LOCALAPPDATA "Programs\FylvoDesktop"

function Find-File([string[]]$Candidates) {
    foreach ($c in $Candidates) {
        if ($c -and (Test-Path -LiteralPath $c)) { return (Resolve-Path -LiteralPath $c).Path }
    }
    return $null
}

$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x64" }
if (-not $Msix) {
    $Msix = Find-File @((Join-Path $PSScriptRoot "Fylvo.$Arch.msix"), (Join-Path $PSScriptRoot "dist\Fylvo.$Arch.msix"))
}
if (-not $Msix -or -not (Test-Path -LiteralPath $Msix)) {
    Write-Error "Package not found. Pass it with -Msix."
    exit 1
}
$Msix = (Resolve-Path -LiteralPath $Msix).Path
$Cer = Find-File @((Join-Path $PSScriptRoot "fylvo-desktop.cer"), (Join-Path $PSScriptRoot "package\fylvo-desktop.cer"))
if (-not $Cer) {
    Write-Error "fylvo-desktop.cer not found next to this script."
    exit 1
}

# Returns the signature of the package if it is signed by the Fylvo Desktop certificate and
# not changed; throws otherwise. Before the certificate is trusted Windows reports the chain
# as not trusted, which is accepted only then (checked by the chain status, not by the
# message, which depends on the language of Windows).
function Assert-FylvoSignature([bool]$Trusted) {
    $Sig = Get-AuthenticodeSignature -LiteralPath $Msix
    if (-not $Sig.SignerCertificate -or $Sig.SignerCertificate.Thumbprint -ne $Signer) {
        throw "$Msix is not signed by the Fylvo Desktop certificate ($($Sig.Status))."
    }
    if ($Sig.Status -eq "Valid") { return }
    if (-not $Trusted -and $Sig.Status -eq "UnknownError") {
        $Chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
        $Chain.ChainPolicy.RevocationMode = "NoCheck"
        [void]$Chain.Build($Sig.SignerCertificate)
        $Problems = @($Chain.ChainStatus | ForEach-Object { $_.Status.ToString() })
        if ($Problems.Count -gt 0 -and -not ($Problems | Where-Object { $_ -ne "UntrustedRoot" })) { return }
    }
    throw "The signature of $Msix is not valid: $($Sig.Status) $($Sig.StatusMessage)"
}

# 1. Check the package before anything is touched.
$Cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($Cer)
if ($Cert.Thumbprint -ne $Signer) {
    Write-Error "$Cer is not the Fylvo Desktop certificate."
    exit 1
}
$Trusted = [bool](Get-ChildItem Cert:\LocalMachine\TrustedPeople | Where-Object Thumbprint -eq $Signer)
Assert-FylvoSignature $Trusted

Add-Type -AssemblyName System.IO.Compression.FileSystem
$Zip = [IO.Compression.ZipFile]::OpenRead($Msix)
try {
    $Entry = $Zip.GetEntry("AppxManifest.xml")
    if (-not $Entry) { throw "$Msix is not an MSIX package." }
    $Reader = New-Object IO.StreamReader($Entry.Open())
    try { $Identity = ([xml]$Reader.ReadToEnd()).Package.Identity } finally { $Reader.Dispose() }
} finally {
    $Zip.Dispose()
}
if ($Identity.Name -ne $PackageName -or $Identity.Publisher -ne $Publisher) {
    Write-Error "$Msix is not a Fylvo Desktop package."
    exit 1
}
Write-Host "Package: Fylvo Desktop $($Identity.Version) ($($Identity.ProcessorArchitecture))" -ForegroundColor Cyan

# 2. Trust the certificate for packages on this PC (admin, once).
if (-not $Trusted) {
    Write-Host "Trusting the Fylvo Desktop certificate on this PC (asks for admin rights) ..." -ForegroundColor Cyan
    $Command = "Import-Certificate -FilePath '$($Cer -replace "'", "''")' -CertStoreLocation Cert:\LocalMachine\TrustedPeople | Out-Null"
    $Process = Start-Process powershell.exe -Verb RunAs -Wait -PassThru -WindowStyle Hidden `
        -ArgumentList "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", $Command
    if ($Process.ExitCode -ne 0 -or -not (Get-ChildItem Cert:\LocalMachine\TrustedPeople | Where-Object Thumbprint -eq $Signer)) {
        Write-Error "The certificate could not be trusted. Nothing was installed."
        exit 1
    }
    Assert-FylvoSignature $true
}

# 3. Install; replace a Developer Mode registration first.
$Installed = Get-AppxPackage -Name $PackageName | Select-Object -First 1
$DevInstall = $Installed -and $Installed.IsDevelopmentMode
$Options = @{ ForceApplicationShutdown = $true }
if ($ForceUpdateFromAnyVersion) { $Options.ForceUpdateFromAnyVersion = $true }

if ($DevInstall) {
    $DevManifest = Join-Path $Installed.InstallLocation "AppxManifest.xml"
    Write-Host "Replacing the Developer Mode installation $($Installed.Version) ..." -ForegroundColor Cyan
    Get-Process fylvo-desktop -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Remove-AppxPackage -Package $Installed.PackageFullName -PreserveApplicationData
    try {
        Add-AppxPackage -Path $Msix @Options
    } catch {
        $InstallError = $_
        Write-Host "Installing the signed package failed, putting the old version back ..." -ForegroundColor Yellow
        try {
            Add-AppxPackage -Register $DevManifest -ForceUpdateFromAnyVersion
            Write-Host "The old version is back." -ForegroundColor Yellow
        } catch {
            Write-Warning "Could not put the old version back: $_. Run install-local.ps1 to install it again."
        }
        throw $InstallError
    }
    # The old folder of install-local.ps1 is not needed any more; only a Fylvo folder is deleted.
    $Item = Get-Item -LiteralPath $DevFolder -Force -ErrorAction SilentlyContinue
    if ($Item -and -not ($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -and
        $Installed.InstallLocation.TrimEnd('\') -eq $DevFolder) {
        try {
            $Old = ([xml](Get-Content -LiteralPath (Join-Path $DevFolder "AppxManifest.xml") -Raw)).Package.Identity
            if ($Old.Name -eq $PackageName -and $Old.Publisher -eq $Publisher) {
                Remove-Item -LiteralPath $DevFolder -Recurse -Force
            }
        } catch {
            Write-Warning "Could not remove the old folder $DevFolder. Delete it by hand."
        }
    }
} else {
    Add-AppxPackage -Path $Msix @Options
}

$Now = Get-AppxPackage -Name $PackageName | Select-Object -First 1
Write-Host "Done: Fylvo Desktop $($Now.Version) is installed. Start it from the Start menu." -ForegroundColor Green
