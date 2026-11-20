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
    let normalize = |s: &str| s.trim_start_matches(r"\??\").trim_end_matches('\\').to_lowercase();
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

/// A registry value written by Fylvo for a drive icon ends like this.
const ICON_SUFFIX: &str = "fylvo-desktop.exe,0";

pub fn is_our_icon(value: &str) -> bool {
    value.to_lowercase().ends_with(ICON_SUFFIX)
}

impl Mount {
    /// Bring the drive letter in line with the setting. Safe to call again at any time.
    pub async fn apply_drive_letter(&self) -> LetterState {
        let (wish, folder, name) = {
            let config = self.config.read().await;
            (config.drive_letter, config.sync_path.clone(), config.name.clone())
        };
        let Some(letter) = wish else {
            return self.set_letter_state(LetterState::Off);
        };
        // A letter Windows cannot tell about counts as used by someone else.
        let owner = win::targets(letter)
            .map(|t| classify(&t, &folder))
            .unwrap_or(LetterOwner::Other);
        let state = match next_step(owner, win::claimed_elsewhere(letter)) {
            Step::Keep => {
                win::brand(letter, &name);
                LetterState::Active
            }
            Step::Define => match win::define(letter, &folder) {
                Ok(()) if matches!(win::targets(letter), Ok(t) if classify(&t, &folder) == LetterOwner::Ours) => {
                    tracing::info!(target: "drive::letter", id = %self.id, %letter, "Drive letter set");
                    win::brand(letter, &name);
                    win::notify(letter, true);
                    LetterState::Active
                }
                result => {
                    tracing::warn!(target: "drive::letter", id = %self.id, %letter, ?result, "Drive letter could not be set");
                    LetterState::Conflict
                }
            },
            Step::StepAside => {
                tracing::info!(target: "drive::letter", id = %self.id, %letter, "Letter claimed by a volume, stepping aside");
                remove_ours(letter, &folder);
                LetterState::Conflict
            }
            Step::Conflict => LetterState::Conflict,
        };
        if state == LetterState::Conflict
            && !self.drive_letter_notified.swap(true, Ordering::SeqCst)
        {
            toast::send_warning_toast(
                &t!("driveLetterTakenTitle"),
                &t!("driveLetterTakenMessage", "letter" => letter, "drive" => name),
            );
        }
        if state == LetterState::Active {
            self.drive_letter_notified.store(false, Ordering::SeqCst);
        }
        self.set_letter_state(state)
    }

    /// Remove the drive letter if it is ours (switch off, drive removed, app quits).
    pub async fn release_drive_letter(&self) {
        let (wish, folder) = {
            let config = self.config.read().await;
            (config.drive_letter, config.sync_path.clone())
        };
        if let Some(letter) = wish {
            remove_ours(letter, &folder);
        }
        self.set_letter_state(LetterState::Off);
    }

    /// Switch the drive letter on (with `letter`, or the first free one) or off.
    pub async fn set_drive_letter(&self, enabled: bool, letter: Option<char>) -> Result<LetterState> {
        let letter = if enabled {
            let current = self.config.read().await.drive_letter;
            let taken = win::taken_letters() & !current.map(letter_bit).unwrap_or(0);
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
        self.release_drive_letter().await;
        self.config.write().await.drive_letter = letter;
        self.drive_letter_notified.store(false, Ordering::SeqCst);
        Ok(self.apply_drive_letter().await)
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

/// Remove the alias of `folder` at `letter` and its name and icon; anything else at that letter
/// stays untouched.
fn remove_ours(letter: char, folder: &Path) {
    let Ok(targets) = win::targets(letter) else {
        return;
    };
    if classify(&targets, folder) != LetterOwner::Ours {
        return;
    }
    // The exact text Windows holds, so only our definition is removed.
    if let Err(e) = win::remove_exact(letter, &targets[0]) {
        tracing::warn!(target: "drive::letter", %letter, error = %e, "Failed to remove drive letter");
        return;
    }
    win::unbrand(letter);
    win::notify(letter, false);
    tracing::info!(target: "drive::letter", %letter, "Drive letter removed");
}

/// Letters in use for the settings' letter picker: all used ones minus `own`.
pub fn free_letters_now(own: &[char]) -> Vec<char> {
    let own_bits = own.iter().fold(0, |bits, l| bits | letter_bit(*l));
    free_letters(win::taken_letters() & !own_bits)
}

mod win {
    use super::{is_our_icon, letter_bit};
    use anyhow::{Context, Result};
    use std::path::Path;
    use widestring::U16CString;
    use windows::Win32::Foundation::{ERROR_FILE_NOT_FOUND, ERROR_INSUFFICIENT_BUFFER, WIN32_ERROR};
    use windows::Win32::Storage::FileSystem::{
        DDD_EXACT_MATCH_ON_REMOVE, DDD_RAW_TARGET_PATH, DDD_REMOVE_DEFINITION,
        DEFINE_DOS_DEVICE_FLAGS, DefineDosDeviceW, FindFirstVolumeW, FindNextVolumeW,
        FindVolumeClose, GetLogicalDrives, GetVolumePathNamesForVolumeNameW, QueryDosDeviceW,
    };
    use windows::Win32::System::Registry::{
        HKEY, HKEY_CURRENT_USER, KEY_READ, KEY_WRITE, REG_OPTION_NON_VOLATILE, REG_SZ,
        RRF_RT_REG_SZ, RegCloseKey, RegCreateKeyExW, RegDeleteTreeW, RegGetValueW, RegOpenKeyExW,
        RegSetValueExW,
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
            let mut paths = [0u16; 1024];
            let mut len = 0u32;
            let ok = unsafe {
                GetVolumePathNamesForVolumeNameW(PCWSTR(name.as_ptr()), Some(&mut paths), &mut len)
            };
            if ok.is_ok() {
                for path in paths.split(|c| *c == 0).filter(|s| s.len() >= 2) {
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
            .filter(|l| open_key(&format!(r"Network\{l}"), false).is_some())
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

    fn drive_key(letter: char) -> String {
        format!(r"Software\Classes\Applications\Explorer.exe\Drives\{letter}")
    }

    struct Key(HKEY);
    impl Drop for Key {
        fn drop(&mut self) {
            let _ = unsafe { RegCloseKey(self.0) };
        }
    }

    fn open_key(path: &str, create: bool) -> Option<Key> {
        let path = wide(path);
        let mut key = HKEY::default();
        let result: WIN32_ERROR = unsafe {
            if create {
                RegCreateKeyExW(
                    HKEY_CURRENT_USER,
                    PCWSTR(path.as_ptr()),
                    0,
                    PCWSTR::null(),
                    REG_OPTION_NON_VOLATILE,
                    KEY_READ | KEY_WRITE,
                    None,
                    &mut key,
                    None,
                )
            } else {
                RegOpenKeyExW(HKEY_CURRENT_USER, PCWSTR(path.as_ptr()), 0, KEY_READ, &mut key)
            }
        };
        result.is_ok().then_some(Key(key))
    }

    fn read_default(path: &str) -> Option<String> {
        let path = wide(path);
        let mut buf = vec![0u16; 1024];
        let mut size = (buf.len() * 2) as u32;
        let result = unsafe {
            RegGetValueW(
                HKEY_CURRENT_USER,
                PCWSTR(path.as_ptr()),
                PCWSTR::null(),
                RRF_RT_REG_SZ,
                None,
                Some(buf.as_mut_ptr() as *mut _),
                Some(&mut size),
            )
        };
        if result.is_err() {
            return None;
        }
        let len = (size as usize / 2).saturating_sub(1);
        Some(String::from_utf16_lossy(&buf[..len.min(buf.len())]))
    }

    fn write_default(path: &str, value: &str) {
        let Some(key) = open_key(path, true) else {
            return;
        };
        let data: Vec<u8> = wide(value)
            .as_slice_with_nul()
            .iter()
            .flat_map(|c| c.to_le_bytes())
            .collect();
        let _ = unsafe { RegSetValueExW(key.0, PCWSTR::null(), 0, REG_SZ, Some(&data)) };
    }

    /// Give the letter the drive's name and the Fylvo icon, unless someone else set them.
    pub fn brand(letter: char, name: &str) {
        let Ok(exe) = std::env::current_exe() else {
            return;
        };
        let icon = format!("{},0", exe.display());
        let base = drive_key(letter);
        let icon_path = format!(r"{base}\DefaultIcon");
        let label_path = format!(r"{base}\DefaultLabel");
        match read_default(&icon_path) {
            // Someone else's icon: leave this letter's look alone.
            Some(current) if !is_our_icon(&current) => return,
            Some(current) if current == icon && read_default(&label_path).as_deref() == Some(name) => {
                return;
            }
            _ => {}
        }
        write_default(&icon_path, &icon);
        write_default(&label_path, name);
    }

    /// Remove the name and icon if Fylvo set them.
    pub fn unbrand(letter: char) {
        let base = drive_key(letter);
        if read_default(&format!(r"{base}\DefaultIcon")).is_some_and(|v| is_our_icon(&v)) {
            let path = wide(&base);
            let _ = unsafe { RegDeleteTreeW(HKEY_CURRENT_USER, PCWSTR(path.as_ptr())) };
        }
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
    fn only_our_icon_counts_as_ours() {
        assert!(is_our_icon(r"C:\Program Files\WindowsApps\Fylvo.Desktop_1.0.0.0_x64__abc\fylvo-desktop.exe,0"));
        assert!(!is_our_icon(r"C:\Windows\System32\shell32.dll,8"));
    }
}
