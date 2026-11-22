//! Installing an update on Windows. The package is a signed MSIX of the same family as the
//! running app. Order (see desktop/UPDATER.md):
//!   1. `check_package`: read the package identity with the Windows packaging API and require
//!      our family, our architecture, the announced version and a higher one than now.
//!   2. `stage`: Windows checks signature, trust and files and unpacks, while sync runs on.
//!   3. (the caller stops sync)
//!   4. `register`: ask Windows to restart the app afterwards, then register exactly the
//!      staged package; Windows closes the app for that.

use crate::Version;
use anyhow::{anyhow, bail, Context, Result};
use std::path::Path;
use windows::core::{HSTRING, PCWSTR};
use windows::ApplicationModel::Package;
use windows::Foundation::Uri;
use windows::Management::Deployment::{DeploymentOptions, PackageManager};
use windows::Win32::Storage::Packaging::Appx::{AppxFactory, IAppxFactory};
use windows::Win32::System::Com::{
    CoCreateInstance, CoInitializeEx, CoTaskMemFree, CoUninitialize, CLSCTX_INPROC_SERVER, COINIT_MULTITHREADED,
    STGM_READ, STGM_SHARE_DENY_WRITE,
};
use windows::Win32::System::Recovery::{RegisterApplicationRestart, REGISTER_APPLICATION_RESTART_FLAGS};
use windows::Win32::UI::Shell::SHCreateStreamOnFileEx;

/// The identity of the running package. `None` when the app runs without one (`cargo tauri
/// dev`): then there are no updates.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Identity {
    pub family_name: String,
    pub full_name: String,
    pub version: Version,
    /// The processor architecture, with the numbers Windows uses in both APIs (x64 = 9, ...).
    pub architecture: i32,
}

pub fn current() -> Option<Identity> {
    let id = Package::Current().ok()?.Id().ok()?;
    let v = id.Version().ok()?;
    Some(Identity {
        family_name: id.FamilyName().ok()?.to_string(),
        full_name: id.FullName().ok()?.to_string(),
        version: Version([v.Major, v.Minor, v.Build, v.Revision]),
        architecture: id.Architecture().ok()?.0,
    })
}

/// Reads the identity of a package file without installing anything.
pub fn read_identity(path: &Path) -> Result<Identity> {
    unsafe {
        // The packaging API is COM; this runs on its own blocking thread.
        let init = CoInitializeEx(None, COINIT_MULTITHREADED);
        let result = read_identity_com(path);
        if init.is_ok() {
            CoUninitialize();
        }
        result
    }
}

unsafe fn read_identity_com(path: &Path) -> Result<Identity> {
    let stream = SHCreateStreamOnFileEx(
        &HSTRING::from(path.as_os_str()),
        (STGM_READ | STGM_SHARE_DENY_WRITE).0,
        0,
        false,
        None,
    )
    .context("failed to open the package")?;
    let factory: IAppxFactory = CoCreateInstance(&AppxFactory, None, CLSCTX_INPROC_SERVER)
        .context("the Windows packaging API is not available")?;
    let reader = factory.CreatePackageReader(&stream).context("the file is not a valid package")?;
    let id = reader
        .GetManifest()
        .and_then(|m| m.GetPackageId())
        .context("the package has no valid manifest")?;
    let family = take_pwstr(id.GetPackageFamilyName().context("the package has no family name")?);
    let full = take_pwstr(id.GetPackageFullName().context("the package has no full name")?);
    let v = id.GetVersion().context("the package has no version")?;
    let arch = id.GetArchitecture().context("the package has no architecture")?;
    Ok(Identity {
        family_name: family,
        full_name: full,
        version: Version([(v >> 48) as u16, (v >> 32) as u16, (v >> 16) as u16, v as u16]),
        architecture: arch.0,
    })
}

unsafe fn take_pwstr(p: windows::core::PWSTR) -> String {
    let s = p.to_string().unwrap_or_default();
    CoTaskMemFree(Some(p.0 as *const _));
    s
}

/// Step 1: the package must be the next version of exactly this app.
pub fn check_package(package: &Identity, running: &Identity, announced: Version) -> Result<()> {
    if package.family_name != running.family_name {
        bail!("the package belongs to another app ({})", package.family_name);
    }
    if package.architecture != running.architecture {
        bail!("the package is for another processor type");
    }
    if package.version != announced {
        bail!("the package has version {}, the server announced {}", package.version, announced);
    }
    if package.version <= running.version {
        bail!("the package is not newer than the installed version");
    }
    Ok(())
}

/// Step 2: Windows checks signature and trust and unpacks the package. Sync keeps running.
pub fn stage(path: &Path) -> Result<()> {
    let uri = file_uri(path)?;
    let manager = PackageManager::new().context("the Windows package manager is not available")?;
    let op = manager
        .StagePackageAsync(&uri, None)
        .context("Windows refused to prepare the update")?;
    wait(op.get(), op.GetResults().ok())
}

/// Step 4: registers the staged package. Windows closes this app to do it and starts it
/// again (RegisterApplicationRestart); when this returns an error the app is still running.
pub fn register(full_name: &str) -> Result<()> {
    unsafe {
        // The app has been running for more than 60 seconds, Windows' condition for a restart.
        RegisterApplicationRestart(PCWSTR::null(), REGISTER_APPLICATION_RESTART_FLAGS(0))
            .context("failed to ask Windows to restart the app")?;
    }
    let manager = PackageManager::new().context("the Windows package manager is not available")?;
    let op = manager
        .RegisterPackageByFullNameAsync(&HSTRING::from(full_name), None, DeploymentOptions::ForceTargetApplicationShutdown)
        .context("Windows refused to install the update")?;
    wait(op.get(), op.GetResults().ok())
}

fn wait(
    result: windows::core::Result<windows::Management::Deployment::DeploymentResult>,
    details: Option<windows::Management::Deployment::DeploymentResult>,
) -> Result<()> {
    match result {
        Ok(_) => Ok(()),
        Err(e) => {
            let text = details
                .and_then(|d| d.ErrorText().ok())
                .map(|t| t.to_string())
                .filter(|t| !t.is_empty())
                .unwrap_or_else(|| e.message().to_string());
            Err(anyhow!("{} (0x{:08X})", text.trim(), e.code().0))
        }
    }
}

fn file_uri(path: &Path) -> Result<Uri> {
    let url = url::Url::from_file_path(path).map_err(|_| anyhow!("invalid package path"))?;
    Uri::CreateUri(&HSTRING::from(url.as_str())).context("invalid package path")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn id(family: &str, version: &str, architecture: i32) -> Identity {
        Identity {
            family_name: family.into(),
            full_name: format!("{family}_{version}"),
            version: Version::parse(version).unwrap(),
            architecture,
        }
    }

    #[test]
    fn only_the_next_version_of_this_app_passes() {
        let running = id("Fylvo.Desktop_s4s912xav2vx4", "0.2.0.0", 9);
        let next = Version::parse("0.3.0").unwrap();
        assert!(check_package(&id("Fylvo.Desktop_s4s912xav2vx4", "0.3.0.0", 9), &running, next).is_ok());
        // Another app (any trusted signer), another processor, another version than announced,
        // the same or an older version.
        assert!(check_package(&id("Other.App_abc", "0.3.0.0", 9), &running, next).is_err());
        assert!(check_package(&id("Fylvo.Desktop_s4s912xav2vx4", "0.3.0.0", 12), &running, next).is_err());
        assert!(check_package(&id("Fylvo.Desktop_s4s912xav2vx4", "0.3.0.1", 9), &running, next).is_err());
        let same = Version::parse("0.2.0").unwrap();
        assert!(check_package(&id("Fylvo.Desktop_s4s912xav2vx4", "0.2.0.0", 9), &running, same).is_err());
    }

    /// Reads a real package when FYLVO_TEST_MSIX names one (not in the normal test run).
    #[test]
    fn reads_the_identity_of_a_real_package() {
        let Ok(path) = std::env::var("FYLVO_TEST_MSIX") else { return };
        let identity = read_identity(Path::new(&path)).unwrap();
        assert!(identity.family_name.starts_with("Fylvo.Desktop_"), "{identity:?}");
        assert_eq!(identity.architecture, 9);
        assert!(read_identity(Path::new("Cargo.toml")).is_err());
    }
}
