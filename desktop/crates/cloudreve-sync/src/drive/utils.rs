use std::os::windows::ffi::OsStrExt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use cloudreve_api::models::uri::CrUri;
use url::Url;
use widestring::U16CString;
use windows::Win32::Storage::CloudFilters::{
    CF_SYNC_ROOT_BASIC_INFO, CF_SYNC_ROOT_INFO_BASIC, CfGetSyncRootInfoByPath,
};
use windows::Win32::Storage::FileSystem::{MOVE_FILE_FLAGS, MoveFileExW};
use windows::Win32::UI::Shell::{
    FO_DELETE, FOF_ALLOWUNDO, FOF_NOCONFIRMATION, FOF_SILENT, FOF_WANTNUKEWARNING,
    SHCNE_ID, SHCNF_PATHW, SHChangeNotify, SHFILEOPSTRUCTW, SHFileOperationW,
};
use windows::core::PCWSTR;

use crate::drive::mounts::DriveConfig;

pub fn local_path_to_cr_uri(path: PathBuf, root: PathBuf, remote_base: String) -> Result<CrUri> {
    let mut base = CrUri::new(&remote_base)?;

    // Strip the root from path to get the relative path
    let relative = path.strip_prefix(&root).context("Path is not under root")?;

    // Convert to string with forward slashes (for URI compatibility)
    let relative_str = relative
        .to_str()
        .context("Path contains invalid UTF-8")?
        .replace("\\", "/");

    // Join the relative path to the base URI if not empty
    if !relative_str.is_empty() {
        base.join(&relative_str.split("/").collect::<Vec<&str>>());
    }

    Ok(base)
}

pub fn remote_path_to_local_relative_path(
    remote_path: &CrUri,
    remote_base: &CrUri,
) -> Result<PathBuf> {
    let remote_path_str = remote_path.path().clone();
    let remote_base_str = remote_base.path().clone();

    // 1. add ending slash if not presented to remote_base_str
    let remote_base_str = if !remote_base_str.ends_with('/') {
        remote_base_str + "/"
    } else {
        remote_base_str
    };

    // 2. remove remote_base_str from remote_path_str
    let relative_path = remote_path_str
        .strip_prefix(&remote_base_str)
        .context("Path is not under remote base")?;

    // 3. make sure OS slash is used
    let relative_path = relative_path.replace("/", std::path::MAIN_SEPARATOR_STR);

    Ok(PathBuf::from(relative_path))
}

/// Generate a URL to view a folder or file online.
///
/// For folders: pass the folder path as `folder_path` and None for `open_file`
/// For files: pass the parent folder path as `folder_path` and the file path as `open_file`
pub fn view_online_url(
    folder_path: &str,
    open_file: Option<&str>,
    config: &DriveConfig,
) -> Result<String> {
    let mut base = config.instance_url.parse::<Url>()?;
    base.set_path("/home");

    {
        let mut query = base.query_pairs_mut();
        query.append_pair("path", folder_path);

        if let Some(file) = open_file {
            query.append_pair("open", file);
        }

        query.append_pair("user_hint", config.user_id.as_str());
    }

    Ok(base.to_string())
}

pub fn recycle_bin_url(config: &DriveConfig) -> Result<String> {
    let mut base = config.instance_url.parse::<Url>()?;
    base.set_path("/home");

    {
        let mut query = base.query_pairs_mut();
        query.append_pair("user_hint", config.user_id.as_str());
         query.append_pair("path", "cloudreve://trash");
    }

    Ok(base.to_string())
}

/// Fylvo: whether `path` lies in a cloud sync folder (Fylvo or another provider).
pub fn is_in_cloud_sync_root(path: &Path) -> bool {
    let Ok(wide) = U16CString::from_os_str(path.as_os_str()) else {
        return false;
    };
    let mut info = CF_SYNC_ROOT_BASIC_INFO::default();
    unsafe {
        CfGetSyncRootInfoByPath(
            PCWSTR(wide.as_ptr()),
            CF_SYNC_ROOT_INFO_BASIC,
            &mut info as *mut _ as *mut _,
            std::mem::size_of::<CF_SYNC_ROOT_BASIC_INFO>() as u32,
            None,
        )
    }
    .is_ok()
}

/// Fylvo: move a file or folder to the Windows recycle bin. Fails rather than deleting for
/// good: if Windows cannot recycle it (too big for the bin, drive without a bin), it asks
/// the user, and a "no" leaves the file in place and returns an error.
pub fn move_to_recycle_bin(path: &Path) -> Result<()> {
    // The list of paths ends with two zeros.
    let mut from: Vec<u16> = path.as_os_str().encode_wide().collect();
    from.extend([0, 0]);
    let mut op = SHFILEOPSTRUCTW {
        wFunc: FO_DELETE,
        pFrom: PCWSTR(from.as_ptr()),
        // No FOF_NOERRORUI: it is not documented whether it also hides the "delete for
        // good?" question that FOF_WANTNUKEWARNING asks for.
        fFlags: (FOF_ALLOWUNDO | FOF_NOCONFIRMATION | FOF_WANTNUKEWARNING | FOF_SILENT).0 as u16,
        ..Default::default()
    };
    let code = unsafe { SHFileOperationW(&mut op) };
    if code != 0 || op.fAnyOperationsAborted.as_bool() {
        anyhow::bail!(
            "could not move {} to the recycle bin (code {})",
            path.display(),
            code
        );
    }
    Ok(())
}

/// Fylvo: rename `from` to `to` without ever replacing another file (`std::fs::rename`
/// replaces). A rename that only changes upper and lower case of the same file works.
pub fn move_no_replace(from: &Path, to: &Path) -> Result<()> {
    let from_w = U16CString::from_os_str(from.as_os_str())?;
    let to_w = U16CString::from_os_str(to.as_os_str())?;
    unsafe {
        MoveFileExW(
            PCWSTR(from_w.as_ptr()),
            PCWSTR(to_w.as_ptr()),
            MOVE_FILE_FLAGS(0),
        )
    }
    .with_context(|| format!("could not rename {} to {}", from.display(), to.display()))
}

// notify_shell_change notify the shell to refresh the file or directory
pub fn notify_shell_change(path: &PathBuf, event: SHCNE_ID) -> Result<()> {
    let utf16_path = U16CString::from_os_str(path.as_path())?;
    unsafe {
        SHChangeNotify(
            event,
            SHCNF_PATHW,
            Some(utf16_path.as_ptr() as *const _),
            None,
        );
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normal_folder_is_no_sync_root() {
        let dir = tempfile::tempdir().unwrap();
        assert!(!is_in_cloud_sync_root(dir.path()));
    }

    /// Puts a small temp file into the Windows recycle bin of the PC running the tests, so it
    /// only runs when asked: `cargo test -p cloudreve-sync -- --ignored recycle`.
    #[test]
    fn move_no_replace_changes_case_but_never_replaces() {
        let dir = tempfile::tempdir().unwrap();
        let lower = dir.path().join("foto.jpg");
        std::fs::write(&lower, b"one").unwrap();
        move_no_replace(&lower, &dir.path().join("Foto.jpg")).unwrap();
        let names: Vec<_> = std::fs::read_dir(dir.path())
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert_eq!(names, ["Foto.jpg"]);

        let other = dir.path().join("other.jpg");
        std::fs::write(&other, b"two").unwrap();
        assert!(move_no_replace(&other, &dir.path().join("FOTO.jpg")).is_err());
        assert_eq!(std::fs::read(dir.path().join("Foto.jpg")).unwrap(), b"one");
    }

    #[test]
    #[ignore]
    fn recycle_moves_file_away() {
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("fylvo-recycle-test.txt");
        std::fs::write(&file, b"fylvo").unwrap();
        move_to_recycle_bin(&file).unwrap();
        assert!(!file.exists());
        // A missing file is an error, never a silent success.
        assert!(move_to_recycle_bin(&file).is_err());
    }
}
