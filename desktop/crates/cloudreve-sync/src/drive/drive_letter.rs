//! Fylvo: show a drive's sync folder additionally as its own drive letter (like G: of Google
//! Drive).
//!
//! The letter is a per-user DOS device name, the same thing `subst` creates: an alias of the
//! sync folder in the user's logon session. There is no second copy and no second sync; every
//! access through the letter reaches the same files, so placeholders, hydration, the deletion
//! guard and read-only drives work the same. The alias lives until sign-out, so the app sets it
//! again at start and removes it when the switch goes off, when the drive is removed and when
//! the app quits. A letter that a real volume (a USB stick) or a network drive claims is never
//! fought for: the app steps aside and asks for another letter.
//!
//! Explorer shows the letter with the drive's name and the Fylvo icon through the per-user keys
//! `HKCU\Software\Classes\Applications\Explorer.exe\Drives\<letter>` (excluded from the package's
//! registry virtualization in the manifest).

use crate::drive::mounts::Mount;
use crate::utils::toast;
use anyhow::Result;
use serde::Serialize;
use std::path::Path;
use std::sync::atomic::Ordering;
use std::time::Duration;

/// The letter tried first: F for Fylvo.
pub const PREFERRED_LETTER: char = 'F';
/// How often the app checks that its letters are still free for it.
const CHECK_EVERY: Duration = Duration::from_secs(5);

/// A..C are never used (floppy drives and the system drive).
pub fn is_usable(letter: char) -> bool {
    ('D'..='Z').contains(&letter)
}

pub fn letter_bit(letter: char) -> u32 {
    1 << (letter as u32 - 'A' as u32)
}

/// Usable letters not in `taken` (bit 0 = A), in alphabetical order.
pub fn free_letters(taken: u32) -> Vec<char> {
    ('D'..='Z').filter(|l| taken & letter_bit(*l) == 0).collect()
}

/// The letter to use: the wished one if it is usable and free, else F, else the next free one
/// after F, else D or E.
pub fn choose_letter(taken: u32, wish: Option<char>) -> Option<char> {
    let free = |l: char| is_usable(l) && taken & letter_bit(l) == 0;
    wish.into_iter()
        .chain(PREFERRED_LETTER..='Z')
        .chain('D'..PREFERRED_LETTER)
        .find(|l| free(*l))
}

/// Who a letter belongs to in this logon session.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LetterOwner {
    Free,
    /// Our alias of the sync folder.
    Ours,
    Other,
}

/// `targets` is what Windows lists for the letter (the newest definition first).
pub fn classify(targets: &[String], folder: &Path) -> LetterOwner {
    match targets.first() {
        None => LetterOwner::Free,
        Some(target) if same_folder(target, folder) => LetterOwner::Ours,
        Some(_) => LetterOwner::Other,
    }
}

fn same_folder(target: &str, folder: &Path) -> bool {
    let normalize = |s: &str| {
        s.replace('/', "\\")
            .trim_start_matches(r"\??\")
            .trim_start_matches(r"\\?\")
            .trim_end_matches('\\')
            .to_lowercase()
    };
    normalize(target) == normalize(&folder.to_string_lossy())
}

/// What to do with the wished letter.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Step {
    /// It is ours and nobody else claims it.
    Keep,
    /// Free: create the alias.
    Define,
    /// It is ours, but a real volume or a network drive claims it now: remove ours.
    StepAside,
    /// Someone else has it: leave it alone and ask the user for another letter.
    Conflict,
}

/// `claimed` = a real volume or a (remembered) network drive uses the letter.
pub fn next_step(owner: LetterOwner, claimed: bool) -> Step {
    match (owner, claimed) {
        (LetterOwner::Ours, false) => Step::Keep,
        (LetterOwner::Ours, true) => Step::StepAside,
        (LetterOwner::Free, false) => Step::Define,
        (LetterOwner::Free, true) | (LetterOwner::Other, _) => Step::Conflict,
    }
}

/// What the settings show about a drive's letter.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Default)]
#[serde(rename_all = "snake_case")]
pub enum LetterState {
    /// Switched off.
    #[default]
    Off,
    /// The letter shows the sync folder.
    Active,
    /// The wished letter is used by something else.
    Conflict,
}

/// Name and icon written by Fylvo carry this value (the sync folder), so Fylvo only ever removes
/// its own.
const MARKER: &str = "FylvoSyncFolder";
/// The label Fylvo wrote, so only that one is removed again.
const LABEL_MARKER: &str = "FylvoLabel";

/// One change of drive letters at a time, for all drives: the checks, the settings and the
/// shutdown, so two drives never take the same free letter.
static LETTER_LOCK: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

impl Mount {
    /// Bring the drive letter in line with the setting. Safe to call again at any time.
    pub async fn apply_drive_letter(&self) -> LetterState {
        let _guard = LETTER_LOCK.lock().await;
        self.apply_locked().await
    }

    async fn apply_locked(&self) -> LetterState {
        let (wish, folder, name) = {
            let config = self.config.read().await;
            (config.drive_letter, config.sync_path.clone(), config.name.clone())
        };
        let Some(letter) = wish.filter(|l| is_usable(*l)) else {
            return self.set_letter_state(LetterState::Off);
        };
        if self.drive_letter_closed.load(Ordering::SeqCst) {
            return self.drive_letter_state();
        }
        // A letter Windows cannot tell about counts as used by someone else.
        let targets = win::targets(letter);
        let owner = targets
            .as_ref()
            .map(|t| classify(t, &folder))
            .unwrap_or(LetterOwner::Other);
        let claimed = win::claimed_elsewhere(letter);
        let state = match next_step(owner, claimed) {
            Step::Keep => {
                win::brand(letter, &name, &folder);
                LetterState::Active
            }
            Step::Define => match win::define(letter, &folder) {
                Ok(()) if owner_now(letter, &folder) == LetterOwner::Ours => {
                    tracing::info!(target: "drive::letter", id = %self.id, %letter, "Drive letter set");
                    win::brand(letter, &name, &folder);
                    win::notify(letter, true);
                    LetterState::Active
                }
                result => {
                    tracing::warn!(target: "drive::letter", id = %self.id, %letter, ?result, "Drive letter could not be set");
                    // Whatever of ours got defined must not stay behind.
                    log_failure(remove_ours(letter, &folder));
                    LetterState::Conflict
                }
            },
            Step::StepAside | Step::Conflict => {
                // Also an older definition of ours below someone else's goes, so it cannot
                // come back when the other one is removed.
                if claimed || owner == LetterOwner::Other {
                    log_failure(remove_ours(letter, &folder));
                }
                LetterState::Conflict
            }
        };
        self.set_letter_state(state);
        if state == LetterState::Active {
            self.drive_letter_notified.store(false, Ordering::SeqCst);
        }
        if state == LetterState::Conflict
            && !self.drive_letter_notified.swap(true, Ordering::SeqCst)
        {
            // On its own thread: a failing notification must not stop the checks.
            let title = t!("driveLetterTakenTitle").to_string();
            let message =
                t!("driveLetterTakenMessage", "letter" => letter, "drive" => name).to_string();
            tokio::task::spawn_blocking(move || toast::send_warning_toast(&title, &message));
        }
        state
    }

    /// Remove the drive letter if it is ours. Fails if Windows did not remove it.
    async fn release_locked(&self) -> Result<()> {
        let (wish, folder) = {
            let config = self.config.read().await;
            (config.drive_letter, config.sync_path.clone())
        };
        if let Some(letter) = wish.filter(|l| is_usable(*l)) {
            remove_ours(letter, &folder)?;
        }
        self.set_letter_state(LetterState::Off);
        Ok(())
    }

    /// Switch the drive letter on (with `letter`, or the first free one) or off.
    pub async fn set_drive_letter(&self, enabled: bool, letter: Option<char>) -> Result<LetterState> {
        let _guard = LETTER_LOCK.lock().await;
        if self.drive_letter_closed.load(Ordering::SeqCst) {
            anyhow::bail!("the drive is shutting down");
        }
        let letter = if enabled {
            let current = self.config.read().await.drive_letter;
            let taken = self.taken_for_this_drive().await;
            match letter {
                Some(l) if !is_usable(l) || taken & letter_bit(l) != 0 => {
                    anyhow::bail!("{}", t!("driveLetterNotFree", "letter" => l))
                }
                Some(l) => Some(l),
                None => Some(
                    choose_letter(taken, current)
                        .ok_or_else(|| anyhow::anyhow!("{}", t!("driveLetterNoneFree")))?,
                ),
            }
        } else {
            None
        };
        // The old letter must be gone before the setting changes, else it would stay behind.
        self.release_locked().await?;
        self.config.write().await.drive_letter = letter;
        self.drive_letter_notified.store(false, Ordering::SeqCst);
        Ok(self.apply_locked().await)
    }

    /// Letters this drive may use (its own active letter counts as free).
    pub async fn free_drive_letters(&self) -> Vec<char> {
        free_letters(self.taken_for_this_drive().await)
    }

    async fn taken_for_this_drive(&self) -> u32 {
        let (current, folder) = {
            let config = self.config.read().await;
            (config.drive_letter, config.sync_path.clone())
        };
        let own = current
            .filter(|l| is_usable(*l))
            .filter(|l| owner_now(*l, &folder) == LetterOwner::Ours && !win::claimed_elsewhere(*l))
            .map(letter_bit)
            .unwrap_or(0);
        win::taken_letters() & !own
    }

    /// At app exit or drive removal: stop the check, then remove the letter.
    pub(crate) async fn close_drive_letter(&self) {
        if let Some(handle) = self.drive_letter_handle.lock().await.take() {
            handle.abort();
            let _ = handle.await;
        }
        let _guard = LETTER_LOCK.lock().await;
        self.drive_letter_closed.store(true, Ordering::SeqCst);
        log_failure(self.release_locked().await);
    }

    pub fn drive_letter_state(&self) -> LetterState {
        *self.drive_letter_state.lock().unwrap()
    }

    fn set_letter_state(&self, state: LetterState) -> LetterState {
        *self.drive_letter_state.lock().unwrap() = state;
        state
    }

    /// Check the letter now and every few seconds, so a USB stick that gets the letter later is
    /// not hidden by the alias, and a letter that became free again is taken up.
    pub async fn spawn_drive_letter_task(self: &std::sync::Arc<Self>) {
        let mount = self.clone();
        let handle = tokio::spawn(async move {
            loop {
                if mount.config.read().await.drive_letter.is_some() {
                    mount.apply_drive_letter().await;
                }
                tokio::time::sleep(CHECK_EVERY).await;
            }
        });
        *self.drive_letter_handle.lock().await = Some(handle);
    }
}

fn owner_now(letter: char, folder: &Path) -> LetterOwner {
    win::targets(letter)
        .map(|t| classify(&t, folder))
        .unwrap_or(LetterOwner::Other)
}

fn log_failure(result: Result<()>) {
    if let Err(e) = result {
        tracing::warn!(target: "drive::letter", error = %e, "Failed to remove drive letter");
    }
}

/// Remove every definition of `folder` at `letter` (with the exact text Windows holds) and the
/// name and icon Fylvo gave it; anything else at that letter stays untouched.
fn remove_ours(letter: char, folder: &Path) -> Result<()> {
    let targets = win::targets(letter)?;
    let ours: Vec<&String> = targets.iter().filter(|t| same_folder(t, folder)).collect();
    for target in &ours {
        win::remove_exact(letter, target)?;
    }
    win::unbrand(letter, folder);
    if !ours.is_empty() {
        // The letter may now show something else (a USB stick), then that one appeared.
        let gone = matches!(win::targets(letter), Ok(t) if t.is_empty());
        win::notify(letter, !gone);
        tracing::info!(target: "drive::letter", %letter, "Drive letter removed");
    }
    Ok(())
}

mod win {
    use super::{LABEL_MARKER, MARKER, letter_bit, same_folder};
    use anyhow::{Context, Result};
    use std::path::Path;
    use widestring::U16CString;
    use windows::Win32::Foundation::{
        ERROR_FILE_NOT_FOUND, ERROR_INSUFFICIENT_BUFFER, ERROR_MORE_DATA, WIN32_ERROR,
    };
    use windows::Win32::Storage::FileSystem::{
        DDD_EXACT_MATCH_ON_REMOVE, DDD_RAW_TARGET_PATH, DDD_REMOVE_DEFINITION,
        DEFINE_DOS_DEVICE_FLAGS, DefineDosDeviceW, FindFirstVolumeW, FindNextVolumeW,
        FindVolumeClose, GetLogicalDrives, GetVolumePathNamesForVolumeNameW, QueryDosDeviceW,
    };
    use windows::Win32::System::Registry::{
        HKEY, HKEY_CURRENT_USER, KEY_READ, KEY_WRITE, REG_OPEN_CREATE_OPTIONS,
        REG_OPTION_NON_VOLATILE, REG_OPTION_VOLATILE, REG_SZ,
        RRF_RT_REG_SZ, RegCloseKey, RegCreateKeyExW, RegDeleteKeyValueW, RegDeleteKeyW,
        RegGetValueW, RegOpenKeyExW, RegQueryInfoKeyW, RegSetValueExW,
    };
    use windows::Win32::UI::Shell::{SHCNE_DRIVEADD, SHCNE_DRIVEREMOVED, SHCNF_PATHW, SHChangeNotify};
    use windows::core::PCWSTR;

    fn wide(s: &str) -> U16CString {
        U16CString::from_str_truncate(s)
    }

    fn device(letter: char) -> U16CString {
        wide(&format!("{letter}:"))
    }

    /// What the letter points to in this session, newest first; empty if nothing.
    pub fn targets(letter: char) -> Result<Vec<String>> {
        let name = device(letter);
        let mut buf = vec![0u16; 1024];
        loop {
            let len = unsafe { QueryDosDeviceW(PCWSTR(name.as_ptr()), Some(&mut buf)) };
            if len > 0 {
                return Ok(buf[..len as usize]
                    .split(|c| *c == 0)
                    .filter(|s| !s.is_empty())
                    .map(String::from_utf16_lossy)
                    .collect());
            }
            let error = windows::core::Error::from_win32();
            if error.code() == ERROR_FILE_NOT_FOUND.to_hresult() {
                return Ok(Vec::new());
            }
            if error.code() == ERROR_INSUFFICIENT_BUFFER.to_hresult() && buf.len() < 65536 {
                buf.resize(buf.len() * 4, 0);
                continue;
            }
            return Err(error).context("QueryDosDevice failed");
        }
    }

    pub fn define(letter: char, folder: &Path) -> Result<()> {
        let target = wide(&folder.to_string_lossy());
        unsafe {
            DefineDosDeviceW(
                DEFINE_DOS_DEVICE_FLAGS(0),
                PCWSTR(device(letter).as_ptr()),
                PCWSTR(target.as_ptr()),
            )
        }
        .context("DefineDosDevice failed")
    }

    pub fn remove_exact(letter: char, target: &str) -> Result<()> {
        let target = wide(target);
        unsafe {
            DefineDosDeviceW(
                DDD_REMOVE_DEFINITION | DDD_EXACT_MATCH_ON_REMOVE | DDD_RAW_TARGET_PATH,
                PCWSTR(device(letter).as_ptr()),
                PCWSTR(target.as_ptr()),
            )
        }
        .context("DefineDosDevice (remove) failed")
    }

    /// Letters of real volumes (from the mount manager, also when our alias hides them in this
    /// session).
    fn volume_letters() -> u32 {
        let mut letters = 0;
        let mut name = [0u16; 260];
        let Ok(find) = (unsafe { FindFirstVolumeW(&mut name) }) else {
            return 0;
        };
        loop {
            let mut paths = vec![0u16; 1024];
            let mut len = 0u32;
            let mut ok = unsafe {
                GetVolumePathNamesForVolumeNameW(PCWSTR(name.as_ptr()), Some(&mut paths), &mut len)
            };
            // A volume with many mount points needs a bigger buffer.
            if matches!(&ok, Err(e) if e.code() == ERROR_MORE_DATA.to_hresult()) {
                paths = vec![0u16; len as usize];
                ok = unsafe {
                    GetVolumePathNamesForVolumeNameW(PCWSTR(name.as_ptr()), Some(&mut paths), &mut len)
                };
            }
            if ok.is_ok() {
                for path in paths.split(|c| *c == 0).filter(|s| s.len() == 3) {
                    let path = String::from_utf16_lossy(path);
                    let mut chars = path.chars();
                    if let (Some(l), Some(':')) = (chars.next(), chars.next()) {
                        if l.is_ascii_alphabetic() {
                            letters |= letter_bit(l.to_ascii_uppercase());
                        }
                    }
                }
            }
            if unsafe { FindNextVolumeW(find, &mut name) }.is_err() {
                break;
            }
        }
        let _ = unsafe { FindVolumeClose(find) };
        letters
    }

    /// Network drives remembered for this user, also when not connected.
    fn network_letters() -> u32 {
        ('D'..='Z')
            .filter(|l| open_key(&format!(r"Network\{l}"), None).is_some())
            .fold(0, |bits, l| bits | letter_bit(l))
    }

    /// Whether a real volume or a network drive uses the letter.
    pub fn claimed_elsewhere(letter: char) -> bool {
        (volume_letters() | network_letters()) & letter_bit(letter) != 0
    }

    /// All letters in use: in this session, by volumes and by network drives.
    pub fn taken_letters() -> u32 {
        let session = unsafe { GetLogicalDrives() };
        session | volume_letters() | network_letters()
    }

    // --- Name and icon in Explorer ---

    const DRIVES_KEY: &str = r"Software\Classes\Applications\Explorer.exe\Drives";

    fn drive_key(letter: char) -> String {
        format!(r"{DRIVES_KEY}\{letter}")
    }

    struct Key(HKEY);
    impl Drop for Key {
        fn drop(&mut self) {
            let _ = unsafe { RegCloseKey(self.0) };
        }
    }

    /// Open a key for reading, or create it. A volatile key vanishes at sign-out, like the
    /// drive letter itself.
    fn open_key(path: &str, create: Option<REG_OPEN_CREATE_OPTIONS>) -> Option<Key> {
        let path = wide(path);
        let mut key = HKEY::default();
        let result: WIN32_ERROR = unsafe {
            match create {
                Some(options) => RegCreateKeyExW(
                    HKEY_CURRENT_USER,
                    PCWSTR(path.as_ptr()),
                    0,
                    PCWSTR::null(),
                    options,
                    KEY_READ | KEY_WRITE,
                    None,
                    &mut key,
                    None,
                ),
                None => RegOpenKeyExW(HKEY_CURRENT_USER, PCWSTR(path.as_ptr()), 0, KEY_READ, &mut key),
            }
        };
        result.is_ok().then_some(Key(key))
    }

    fn read_value(path: &str, value: Option<&str>) -> Option<String> {
        let path = wide(path);
        let value = value.map(wide);
        let value_ptr = value.as_ref().map_or(PCWSTR::null(), |v| PCWSTR(v.as_ptr()));
        let mut buf = vec![0u16; 1024];
        let mut size = (buf.len() * 2) as u32;
        let result = unsafe {
            RegGetValueW(
                HKEY_CURRENT_USER,
                PCWSTR(path.as_ptr()),
                value_ptr,
                RRF_RT_REG_SZ,
                None,
                Some(buf.as_mut_ptr() as *mut _),
                Some(&mut size),
            )
        };
        if result.is_err() {
            return None;
        }
        let len = (size as usize / 2).saturating_sub(1).min(buf.len());
        Some(String::from_utf16_lossy(&buf[..len]))
    }

    fn write_value(key: &Key, value: Option<&str>, data: &str) {
        let value = value.map(wide);
        let value_ptr = value.as_ref().map_or(PCWSTR::null(), |v| PCWSTR(v.as_ptr()));
        let bytes: Vec<u8> = wide(data)
            .as_slice_with_nul()
            .iter()
            .flat_map(|c| c.to_le_bytes())
            .collect();
        let _ = unsafe { RegSetValueExW(key.0, value_ptr, 0, REG_SZ, Some(&bytes)) };
    }

    /// Whether the name and icon at `letter` were written by Fylvo for `folder`.
    fn branded_for(letter: char, folder: &Path) -> Option<bool> {
        read_value(&drive_key(letter), Some(MARKER)).map(|m| same_folder(&m, folder))
    }

    /// Give the letter the drive's name and the Fylvo icon, unless someone else gave it a look.
    pub fn brand(letter: char, name: &str, folder: &Path) {
        let base = drive_key(letter);
        match branded_for(letter, folder) {
            Some(true) => {}
            // Another Fylvo drive's, or someone else's look: leave it alone.
            Some(false) => return,
            None if open_key(&base, None).is_some() => return,
            None => {}
        }
        let Ok(exe) = std::env::current_exe() else {
            return;
        };
        let icon = format!("{},0", exe.display());
        let label_path = format!(r"{base}\DefaultLabel");
        let icon_path = format!(r"{base}\DefaultIcon");
        if read_value(&label_path, None).as_deref() == Some(name)
            && read_value(&icon_path, None).as_deref() == Some(icon.as_str())
        {
            return;
        }
        // The parent stays a normal key; Fylvo's keys below it are volatile.
        if open_key(DRIVES_KEY, Some(REG_OPTION_NON_VOLATILE)).is_none() {
            return;
        }
        let Some(letter_key) = open_key(&base, Some(REG_OPTION_VOLATILE)) else {
            return;
        };
        write_value(&letter_key, Some(MARKER), &folder.to_string_lossy());
        write_value(&letter_key, Some(LABEL_MARKER), name);
        if let Some(key) = open_key(&label_path, Some(REG_OPTION_VOLATILE)) {
            write_value(&key, None, name);
        }
        if let Some(key) = open_key(&icon_path, Some(REG_OPTION_VOLATILE)) {
            write_value(&key, None, &icon);
        }
    }

    /// Remove the name and icon if Fylvo set them for `folder`.
    /// Only what Fylvo wrote goes; values another program added stay.
    pub fn unbrand(letter: char, folder: &Path) {
        if branded_for(letter, folder) != Some(true) {
            return;
        }
        let base = drive_key(letter);
        let label = read_value(&base, Some(LABEL_MARKER));
        delete_key_if(&format!(r"{base}\DefaultIcon"), |v| {
            v.to_lowercase().ends_with("fylvo-desktop.exe,0")
        });
        delete_key_if(&format!(r"{base}\DefaultLabel"), |v| Some(v) == label.as_deref());
        if let Some(key) = open_key(&base, Some(REG_OPTION_VOLATILE)) {
            for value in [MARKER, LABEL_MARKER] {
                let value = wide(value);
                let _ = unsafe { RegDeleteKeyValueW(key.0, PCWSTR::null(), PCWSTR(value.as_ptr())) };
            }
        }
        delete_key_if_empty(&base);
    }

    /// Delete the key if its default value passes `check` and it holds nothing else.
    fn delete_key_if(path: &str, check: impl Fn(&str) -> bool) {
        if read_value(path, None).is_some_and(|v| check(&v)) && counts(path) == Some((0, 1)) {
            let path = wide(path);
            let _ = unsafe { RegDeleteKeyW(HKEY_CURRENT_USER, PCWSTR(path.as_ptr())) };
        }
    }

    fn delete_key_if_empty(path: &str) {
        if counts(path) == Some((0, 0)) {
            let path = wide(path);
            let _ = unsafe { RegDeleteKeyW(HKEY_CURRENT_USER, PCWSTR(path.as_ptr())) };
        }
    }

    /// Number of subkeys and values of a key.
    fn counts(path: &str) -> Option<(u32, u32)> {
        let key = open_key(path, None)?;
        let (mut subkeys, mut values) = (0u32, 0u32);
        let result = unsafe {
            RegQueryInfoKeyW(
                key.0,
                windows::core::PWSTR::null(),
                None,
                None,
                Some(&mut subkeys),
                None,
                None,
                Some(&mut values),
                None,
                None,
                None,
                None,
            )
        };
        result.is_ok().then_some((subkeys, values))
    }

    /// Tell Explorer that the drive appeared or went away.
    pub fn notify(letter: char, added: bool) {
        let root = wide(&format!(r"{letter}:\"));
        let event = if added { SHCNE_DRIVEADD } else { SHCNE_DRIVEREMOVED };
        unsafe { SHChangeNotify(event, SHCNF_PATHW, Some(root.as_ptr() as *const _), None) };
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn taken(letters: &str) -> u32 {
        letters.chars().fold(0, |bits, l| bits | letter_bit(l))
    }

    #[test]
    fn prefers_f_then_the_next_free_letter() {
        assert_eq!(choose_letter(taken("C"), None), Some('F'));
        assert_eq!(choose_letter(taken("CDF"), None), Some('G'));
        assert_eq!(choose_letter(taken("CFGH"), None), Some('I'));
        // Only D and E left.
        let all_from_f: String = ('F'..='Z').collect();
        assert_eq!(choose_letter(taken(&format!("C{all_from_f}")), None), Some('D'));
        // Nothing left.
        let all: String = ('A'..='Z').collect();
        assert_eq!(choose_letter(taken(&all), None), None);
    }

    #[test]
    fn a_wished_letter_is_used_only_when_free_and_usable() {
        assert_eq!(choose_letter(taken("C"), Some('X')), Some('X'));
        assert_eq!(choose_letter(taken("CX"), Some('X')), Some('F'));
        // Never A, B or C.
        assert_eq!(choose_letter(0, Some('A')), Some('F'));
        assert_eq!(choose_letter(0, Some('C')), Some('F'));
    }

    #[test]
    fn free_letters_skip_taken_ones_and_a_to_c() {
        let free = free_letters(taken("CDEZ"));
        assert_eq!(free.first(), Some(&'F'));
        assert!(!free.contains(&'Z'));
        assert!(!free.iter().any(|l| ['A', 'B', 'C', 'D', 'E'].contains(l)));
        assert_eq!(free.len(), 20);
    }

    #[test]
    fn letter_owner_from_the_dos_device() {
        let folder = PathBuf::from(r"C:\Users\Alex\Fylvo");
        assert_eq!(classify(&[], &folder), LetterOwner::Free);
        let ours = vec![r"\??\C:\Users\Alex\Fylvo".to_string()];
        assert_eq!(classify(&ours, &folder), LetterOwner::Ours);
        // Case and a trailing backslash do not matter on Windows.
        let ours2 = vec![r"\??\c:\users\Alex\fylvo\".to_string()];
        assert_eq!(classify(&ours2, &folder), LetterOwner::Ours);
        // A USB stick, or another folder (also one inside ours).
        let usb = vec![r"\Device\HarddiskVolume7".to_string()];
        assert_eq!(classify(&usb, &folder), LetterOwner::Other);
        let other = vec![r"\??\C:\Users\Alex\Fylvo\Sub".to_string()];
        assert_eq!(classify(&other, &folder), LetterOwner::Other);
        // Only the newest definition counts.
        let stacked = vec![usb[0].clone(), ours[0].clone()];
        assert_eq!(classify(&stacked, &folder), LetterOwner::Other);
    }

    #[test]
    fn never_fights_for_a_letter() {
        use LetterOwner::*;
        assert_eq!(next_step(Free, false), Step::Define);
        assert_eq!(next_step(Ours, false), Step::Keep);
        // A USB stick got the letter while it was ours: step aside.
        assert_eq!(next_step(Ours, true), Step::StepAside);
        // Taken by someone else, or claimed by a volume or network drive: leave it alone.
        assert_eq!(next_step(Other, false), Step::Conflict);
        assert_eq!(next_step(Other, true), Step::Conflict);
        assert_eq!(next_step(Free, true), Step::Conflict);
    }

    #[test]
    fn folder_spellings_match() {
        let folder = PathBuf::from(r"C:\Users\Alex\Fylvo");
        assert!(same_folder(r"\\?\C:\Users\Alex\Fylvo", &folder));
        assert!(same_folder("C:/Users/Alex/Fylvo/", &folder));
        assert!(!same_folder(r"C:\Users\Alex\Fylvo2", &folder));
    }
}
