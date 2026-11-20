//! Fylvo: names that are one file on Windows.
//!
//! NTFS ignores upper and lower case: `foto.jpg` and `Foto.jpg` are the same file. The server
//! and the rest of the client compare names byte by byte. Without this module a case-only
//! rename on the server looked like "delete `foto.jpg`, create `Foto.jpg`", and the delete hit
//! the very file the new name points to. Two server names that differ only in case cannot
//! both exist in one folder here.
//!
//! Unicode forms (composed `ü` and `u` + combining mark) are not touched: NTFS keeps them apart
//! as two names, and so does the client.

use crate::{
    cfapi::placeholder::LocalFileInfo,
    drive::{commands::MountCommand, mounts::Mount, sync::SyncMode, utils::move_no_replace},
    tasks::TaskPayload,
};
use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use cloudreve_api::models::explorer::{FileResponse, file_type};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
use windows::Win32::{
    Foundation::LPARAM,
    Globalization::{LCMAP_UPPERCASE, LCMapStringEx, LOCALE_NAME_INVARIANT},
};

/// A name as Windows compares file names: upper case with the system's invariant table (one
/// UTF-16 unit to one, so `ü` = `Ü` but `ß` stays `ß`, not `SS`). No Unicode normalisation.
pub fn ntfs_fold(name: &str) -> String {
    let wide: Vec<u16> = name.encode_utf16().collect();
    if wide.is_empty() {
        return String::new();
    }
    let mut upper = vec![0u16; wide.len()];
    let written = unsafe {
        LCMapStringEx(
            LOCALE_NAME_INVARIANT,
            LCMAP_UPPERCASE,
            &wide,
            Some(&mut upper),
            None,
            None,
            LPARAM(0),
        )
    };
    if written <= 0 {
        // Not expected; compare exactly then, as before this module.
        return name.to_string();
    }
    String::from_utf16_lossy(&upper[..written as usize])
}

/// Whether two paths are the same file on NTFS.
pub fn same_on_ntfs(a: &Path, b: &Path) -> bool {
    ntfs_fold(&a.to_string_lossy()) == ntfs_fold(&b.to_string_lossy())
}

/// Folded names that occur more than once among `names` (server entries of one folder).
pub fn case_twins<'a>(names: impl Iterator<Item = &'a str>) -> HashSet<String> {
    let mut seen = HashSet::new();
    let mut twins = HashSet::new();
    for name in names {
        let key = ntfs_fold(name);
        if !seen.insert(key.clone()) {
            twins.insert(key);
        }
    }
    twins
}

/// The first folder between `parent` (included) and `sync_root` (excluded) that exists on disk
/// spelled differently. A sync under it would read the server folder of one spelling and
/// change the local folder of the other.
pub(crate) fn case_mismatched_ancestor(
    parent: &Path,
    sync_root: &Path,
    on_disk: impl Fn(&Path) -> Option<PathBuf>,
) -> Option<PathBuf> {
    let mut current = parent;
    while current.starts_with(sync_root) && current != sync_root {
        if let Some(disk) = on_disk(current) {
            if disk != current {
                return Some(current.to_path_buf());
            }
        }
        current = current.parent()?;
    }
    None
}

/// What a sync pass does about names that are one file here.
#[derive(Debug, Default, PartialEq)]
pub(crate) struct CasePlan {
    /// The paths to plan, at most one per local file.
    pub paths: Vec<PathBuf>,
    /// Local renames (on-disk name, server name) to do before planning.
    pub renames: Vec<(PathBuf, PathBuf)>,
    /// Server entries left on the server: another server name with the same local file is
    /// synced instead.
    pub hidden: Vec<PathBuf>,
    /// Names where the local file may be a different object than the server entry with that
    /// name. Nothing is done for them (no rename, no delete, no upload, no download).
    pub held: Vec<PathBuf>,
}

/// What the inventory knows about a path: folder or not, the server's creation time, the etag.
pub(crate) type KnownEntry = (bool, i64, String);

/// Decide the case questions of one folder. `requested` are the paths a sync pass was asked for,
/// `remote` the server entries found for them (keyed by the server's own name), `on_disk`
/// returns the path as it is spelled on disk if something exists there, `known` the
/// inventory row stored under exactly that path.
pub(crate) fn plan_case(
    requested: &[PathBuf],
    remote: &HashMap<PathBuf, FileResponse>,
    on_disk: impl Fn(&Path) -> Option<PathBuf>,
    known: impl Fn(&Path) -> Option<KnownEntry>,
) -> CasePlan {
    #[derive(Default)]
    struct Group {
        requested: Vec<PathBuf>,
        remote: Vec<PathBuf>,
    }
    let mut groups: BTreeMap<String, Group> = BTreeMap::new();
    for path in requested {
        let group = groups.entry(ntfs_fold(&path.to_string_lossy())).or_default();
        if !group.requested.contains(path) {
            group.requested.push(path.clone());
        }
    }
    for path in remote.keys() {
        groups
            .entry(ntfs_fold(&path.to_string_lossy()))
            .or_default()
            .remote
            .push(path.clone());
    }

    let mut plan = CasePlan::default();
    for (_, mut group) in groups {
        group.remote.sort();
        let any = group.requested.first().or(group.remote.first()).unwrap();
        let disk = on_disk(any);

        if group.remote.is_empty() {
            // Nothing on the server: plan the file as it is on disk.
            plan.paths.push(disk.unwrap_or_else(|| any.clone()));
            continue;
        }

        let keeper = match &disk {
            // Nothing here yet: the first server name gets the local file.
            None => group.remote[0].clone(),
            // The disk already has one of the server names: that one stays.
            Some(disk) if group.remote.contains(disk) => disk.clone(),
            // The disk spells it differently: rename only if it is surely the same object.
            Some(disk) => {
                let server = &group.remote[0];
                if group.remote.len() == 1 && is_same_object(disk, server, &remote[server], &known)
                {
                    plan.renames.push((disk.clone(), server.clone()));
                    server.clone()
                } else {
                    plan.held.extend(group.remote.iter().cloned());
                    continue;
                }
            }
        };
        plan.hidden
            .extend(group.remote.iter().filter(|p| **p != keeper).cloned());
        plan.paths.push(keeper);
    }
    plan
}

/// Whether the local file at `disk` represents the server entry `server` (same folder, other
/// spelling). The inventory row of exactly one of the two names must match the server entry's
/// kind and creation time, which a rename does not change, and for a file also its content
/// version (etag).
fn is_same_object(
    disk: &Path,
    server: &Path,
    remote: &FileResponse,
    known: &impl Fn(&Path) -> Option<KnownEntry>,
) -> bool {
    let (is_folder, created_at, etag) = match (known(disk), known(server)) {
        (Some(row), None) | (None, Some(row)) => row,
        _ => return false,
    };
    let remote_is_folder = remote.file_type == file_type::FOLDER;
    let created = remote
        .created_at
        .parse::<DateTime<Utc>>()
        .map(|time| time.timestamp());
    let same_content = remote_is_folder
        || (!etag.is_empty() && remote.primary_entity.as_deref() == Some(etag.as_str()));
    is_folder == remote_is_folder && created.is_ok_and(|time| time == created_at) && same_content
}

/// How long a registered own rename waits for its callbacks.
const OWN_RENAME_TTL: Duration = Duration::from_secs(60);

/// Renames the app makes itself (to the server's spelling of a name). The cfapi rename
/// callbacks pass them without telling the server: the server has the new name already.
#[derive(Clone, Default)]
pub struct OwnRenames(Arc<Mutex<Vec<(PathBuf, PathBuf, Instant)>>>);

impl OwnRenames {
    pub fn register(&self, from: &Path, to: &Path) {
        let mut renames = self.0.lock().unwrap();
        renames.retain(|(_, _, at)| at.elapsed() < OWN_RENAME_TTL);
        renames.push((from.to_path_buf(), to.to_path_buf(), Instant::now()));
    }

    /// Whether renaming `source` to `target` is a registered own rename. `consume` removes it
    /// (the rename completed or failed).
    pub fn matches(&self, source: &Path, target: &Path, consume: bool) -> bool {
        let mut renames = self.0.lock().unwrap();
        let found = renames.iter().position(|(from, to, at)| {
            at.elapsed() < OWN_RENAME_TTL
                && same_on_ntfs(source, from)
                && same_on_ntfs(target, to)
                && target.file_name() == to.file_name()
        });
        match found {
            Some(index) if consume => {
                renames.remove(index);
                true
            }
            Some(_) => true,
            None => false,
        }
    }
}

impl Mount {
    /// Apply the case decisions of one folder before it is planned. Returns the paths to plan;
    /// paths whose rename failed are left out of this pass.
    pub(crate) async fn resolve_case(
        &self,
        paths: &[PathBuf],
        remote: &HashMap<PathBuf, FileResponse>,
    ) -> (Vec<PathBuf>, Vec<(PathBuf, anyhow::Error)>) {
        let inventory = self.inventory.clone();
        let plan = plan_case(paths, remote, LocalFileInfo::on_disk_path, |path| {
            let row = inventory.query_by_path(path.to_str()?).ok()??;
            Some((row.is_folder, row.created_at, row.etag))
        });

        let mut errors = Vec::new();
        let mut failed: HashSet<PathBuf> = HashSet::new();
        for (from, to) in &plan.renames {
            if let Err(err) = self.rename_local_case(from, to).await {
                tracing::warn!(target: "drive::case", id = %self.id, from = %from.display(), to = %to.display(), error = ?err, "Case rename failed, retried on the next sync");
                failed.insert(to.clone());
                errors.push((from.clone(), err));
            } else {
                tracing::info!(target: "drive::case", id = %self.id, from = %from.display(), to = %to.display(), "Renamed local file to the server's spelling");
            }
        }
        self.report_case_clash(&plan.hidden, "caseClashMessage");
        self.report_case_clash(&plan.held, "caseHeldMessage");

        let paths = plan
            .paths
            .into_iter()
            .filter(|path| !failed.contains(path))
            .collect();
        (paths, errors)
    }

    /// Rename a local file or folder to the server's spelling of the same name, with its
    /// inventory rows. Never replaces another file.
    async fn rename_local_case(&self, from: &Path, to: &Path) -> Result<()> {
        let from_str = from.to_str().context("path is not UTF-8")?;
        let to_str = to.to_str().context("path is not UTF-8")?;
        let info = LocalFileInfo::from_path(from)?;
        let was_in_sync = info.is_placeholder() && info.in_sync();

        self.own_renames.register(from, to);
        if let Err(err) = move_no_replace(from, to) {
            self.own_renames.matches(from, to, true);
            return Err(err);
        }
        // The inventory refuses when rows under the new spelling exist already: then the disk
        // gets its old name back and nothing has changed.
        if let Err(err) = self.inventory.rename_path(from_str, to_str) {
            self.own_renames.register(to, from);
            if let Err(undo) = move_no_replace(to, from) {
                self.own_renames.matches(to, from, true);
                tracing::error!(target: "drive::case", id = %self.id, path = %to.display(), error = ?undo, "Could not undo the case rename");
            }
            return Err(err.context("inventory rows under the new name exist"));
        }

        // Tasks under the old spelling would reach the server under the old name.
        let cancelled = self
            .task_queue
            .cancel_by_path(from.to_path_buf())
            .await
            .unwrap_or(0);
        if was_in_sync && !LocalFileInfo::from_path(to)?.in_sync() {
            crate::drive::commands::mark_path_in_sync(to).await?;
        }
        // A local change waiting for upload: the plan sees no server change, so queue it here.
        if !was_in_sync && !info.is_directory {
            self.enqueue_upload(TaskPayload::upload(to.to_path_buf()))
                .await?;
        }
        if cancelled > 0 {
            self.command_tx
                .send(MountCommand::Sync {
                    local_paths: vec![to.to_path_buf()],
                    mode: SyncMode::FullHierarchy,
                    user_initiated: false,
                })
                .context("failed to send sync command")?;
        }
        Ok(())
    }

    /// Tell the user once per run about names that cannot be synced as they are.
    pub(crate) fn report_case_clash(&self, paths: &[PathBuf], message_key: &str) {
        let first = {
            let mut notified = self.case_clash_notified.lock().unwrap();
            let fresh: Vec<&PathBuf> = paths
                .iter()
                .filter(|path| notified.insert((*path).clone()))
                .collect();
            for path in &fresh {
                tracing::warn!(target: "drive::case", id = %self.id, path = %path.display(), kind = message_key, "Name differs only in case, not synced");
            }
            match fresh.first() {
                Some(first) => (*first).clone(),
                None => return,
            }
        };
        let name = first
            .file_name()
            .map(|name| name.to_string_lossy().to_string())
            .unwrap_or_default();
        crate::utils::toast::send_warning_toast(
            &t!("caseClashTitle"),
            &t!(message_key, "name" => name),
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const CREATED: &str = "2026-11-19T10:00:00Z";
    const CREATED_TS: i64 = 1_795_082_400;
    const ETAG: &str = "e1";

    fn p(name: &str) -> PathBuf {
        PathBuf::from(r"C:\S").join(name)
    }

    fn server(names: &[&str]) -> HashMap<PathBuf, FileResponse> {
        server_with(names, false)
    }

    fn server_with(names: &[&str], folder: bool) -> HashMap<PathBuf, FileResponse> {
        names
            .iter()
            .map(|name| {
                (
                    p(name),
                    FileResponse {
                        name: name.to_string(),
                        file_type: if folder { file_type::FOLDER } else { file_type::FILE },
                        created_at: CREATED.to_string(),
                        primary_entity: (!folder).then(|| ETAG.to_string()),
                        ..Default::default()
                    },
                )
            })
            .collect()
    }

    /// The disk holds `names`; lookups ignore case like NTFS.
    fn disk(names: &'static [&'static str]) -> impl Fn(&Path) -> Option<PathBuf> {
        move |path: &Path| {
            names
                .iter()
                .map(|name| p(name))
                .find(|on_disk| same_on_ntfs(on_disk, path))
        }
    }

    /// The inventory has a row for each of `names`, matching the server entries.
    fn known(names: &'static [&'static str], folder: bool) -> impl Fn(&Path) -> Option<KnownEntry> {
        move |path: &Path| {
            names.iter().any(|name| p(name) == path).then(|| {
                (folder, CREATED_TS, if folder { String::new() } else { ETAG.to_string() })
            })
        }
    }

    fn nothing_known(_: &Path) -> Option<KnownEntry> {
        None
    }

    #[test]
    fn created_constant_matches() {
        assert_eq!(CREATED.parse::<DateTime<Utc>>().unwrap().timestamp(), CREATED_TS);
    }

    #[test]
    fn fold_is_like_windows() {
        assert_eq!(ntfs_fold("foto.JPG"), ntfs_fold("Foto.jpg"));
        assert_eq!(ntfs_fold("Übung"), ntfs_fold("übung"));
        // Greek with a subscript: one name on NTFS, two characters in Rust's full upper case.
        assert_eq!(ntfs_fold("\u{1F80}"), ntfs_fold("\u{1F88}"));
        // Windows does not turn one character into two.
        assert_ne!(ntfs_fold("Straße"), ntfs_fold("STRASSE"));
        // Composed and decomposed umlauts are two names on NTFS.
        assert_ne!(ntfs_fold("M\u{00FC}ller"), ntfs_fold("Mu\u{0308}ller"));
        assert_ne!(ntfs_fold("M\u{00DC}LLER"), ntfs_fold("mu\u{0308}ller"));
    }

    #[test]
    fn fold_matches_the_real_file_system() {
        // The test PC's NTFS decides: a second spelling must open the same file.
        let dir = tempfile::tempdir().unwrap();
        for (created, other) in [("fÖto.txt", "FöTO.TXT"), ("\u{1F80}.txt", "\u{1F88}.txt")] {
            std::fs::write(dir.path().join(created), b"x").unwrap();
            assert!(dir.path().join(other).exists(), "{other}");
            assert!(same_on_ntfs(Path::new(created), Path::new(other)));
            assert_eq!(
                LocalFileInfo::on_disk_path(&dir.path().join(other)),
                Some(dir.path().join(created))
            );
        }
        assert_eq!(LocalFileInfo::on_disk_path(&dir.path().join("missing.txt")), None);
    }

    #[test]
    fn server_case_rename_full_sync_renames_locally() {
        // Disk `foto.jpg` (synced), server renamed it to `Foto.jpg`. The full sync sees both.
        let plan = plan_case(
            &[p("foto.jpg"), p("Foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["foto.jpg"]),
            known(&["foto.jpg"], false),
        );
        assert_eq!(plan.paths, [p("Foto.jpg")]);
        assert_eq!(plan.renames, [(p("foto.jpg"), p("Foto.jpg"))]);
        assert!(plan.hidden.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn server_case_rename_event_renames_locally() {
        // The event syncs only the new name; the disk still has the old one.
        let plan = plan_case(
            &[p("Foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["foto.jpg"]),
            known(&["foto.jpg"], false),
        );
        assert_eq!(plan.paths, [p("Foto.jpg")]);
        assert_eq!(plan.renames, [(p("foto.jpg"), p("Foto.jpg"))]);
    }

    #[test]
    fn server_case_rename_of_folder_renames_locally() {
        let plan = plan_case(
            &[p("Fotos"), p("fotos")],
            &server_with(&["fotos"], true),
            disk(&["Fotos"]),
            known(&["Fotos"], true),
        );
        assert_eq!(plan.paths, [p("fotos")]);
        assert_eq!(plan.renames, [(p("Fotos"), p("fotos"))]);
    }

    #[test]
    fn already_renamed_needs_nothing() {
        let plan = plan_case(
            &[p("Foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["Foto.jpg"]),
            known(&["Foto.jpg"], false),
        );
        assert_eq!(plan, CasePlan { paths: vec![p("Foto.jpg")], ..Default::default() });
    }

    #[test]
    fn local_case_rename_while_off_takes_the_server_name() {
        // The user renamed `foto.jpg` to `Foto.jpg` while the app was closed. The server name
        // wins, like for any rename made while the app was off, but nothing is deleted.
        let plan = plan_case(
            &[p("Foto.jpg"), p("foto.jpg")],
            &server(&["foto.jpg"]),
            disk(&["Foto.jpg"]),
            known(&["foto.jpg"], false),
        );
        assert_eq!(plan.paths, [p("foto.jpg")]);
        assert_eq!(plan.renames, [(p("Foto.jpg"), p("foto.jpg"))]);
    }

    #[test]
    fn unknown_local_file_with_other_case_is_held() {
        // A new local `Foto.jpg` next to a server `foto.jpg` from elsewhere: maybe two different
        // files. Nothing is renamed, uploaded or deleted.
        let plan = plan_case(
            &[p("Foto.jpg")],
            &server(&["foto.jpg"]),
            disk(&["Foto.jpg"]),
            nothing_known,
        );
        assert!(plan.paths.is_empty() && plan.renames.is_empty());
        assert_eq!(plan.held, [p("foto.jpg")]);
    }

    #[test]
    fn other_object_with_other_case_is_held() {
        // Local `a.jpg` was server `a.jpg`; that was deleted and a different `A.jpg` remains.
        let held = |row: KnownEntry| {
            plan_case(&[p("a.jpg")], &server(&["A.jpg"]), disk(&["a.jpg"]), |path: &Path| {
                (path == p("a.jpg")).then(|| row.clone())
            })
        };
        // Other kind, other creation time, other content (same second, same kind).
        for row in [
            (true, CREATED_TS, ETAG.to_string()),
            (false, CREATED_TS + 1, ETAG.to_string()),
            (false, CREATED_TS, "e2".to_string()),
            (false, CREATED_TS, String::new()),
        ] {
            let plan = held(row);
            assert_eq!(plan.held, [p("A.jpg")]);
            assert!(plan.renames.is_empty() && plan.paths.is_empty());
        }
    }

    #[test]
    fn rows_under_both_spellings_are_held() {
        let plan = plan_case(
            &[p("foto.jpg")],
            &server(&["Foto.jpg"]),
            disk(&["foto.jpg"]),
            known(&["foto.jpg", "Foto.jpg"], false),
        );
        assert_eq!(plan.held, [p("Foto.jpg")]);
    }

    #[test]
    fn two_server_names_new_folder_keeps_the_first() {
        let plan = plan_case(&[], &server(&["a.jpg", "A.jpg"]), disk(&[]), nothing_known);
        assert_eq!(plan.paths, [p("A.jpg")]);
        assert_eq!(plan.hidden, [p("a.jpg")]);
    }

    #[test]
    fn two_server_names_keep_the_one_on_disk() {
        let plan = plan_case(
            &[p("a.jpg")],
            &server(&["a.jpg", "A.jpg"]),
            disk(&["a.jpg"]),
            known(&["a.jpg"], false),
        );
        assert_eq!(plan.paths, [p("a.jpg")]);
        assert_eq!(plan.hidden, [p("A.jpg")]);
        assert!(plan.renames.is_empty());
    }

    #[test]
    fn two_server_names_none_on_disk_spelling_is_held() {
        let plan = plan_case(
            &[p("FOTO.jpg")],
            &server(&["foto.jpg", "Foto.jpg"]),
            disk(&["FOTO.jpg"]),
            known(&["FOTO.jpg"], false),
        );
        assert!(plan.paths.is_empty() && plan.renames.is_empty());
        assert_eq!(plan.held.len(), 2);
    }

    #[test]
    fn server_deletes_the_hidden_twin_keeps_local_file() {
        // Server had `a.jpg` (on disk) and `A.jpg` (hidden). `A.jpg` is deleted; its delete
        // event asks for `A.jpg`, which NTFS finds as `a.jpg`. Before, that deleted `a.jpg`.
        let plan = plan_case(
            &[p("A.jpg")],
            &server(&["a.jpg"]),
            disk(&["a.jpg"]),
            known(&["a.jpg"], false),
        );
        assert_eq!(plan.paths, [p("a.jpg")]);
        assert!(plan.renames.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn unicode_forms_stay_two_files() {
        let composed = "M\u{00FC}ller.txt";
        let decomposed = "Mu\u{0308}ller.txt";
        let plan = plan_case(
            &[p(composed)],
            &server(&[composed, decomposed]),
            disk(&["M\u{00FC}ller.txt"]),
            nothing_known,
        );
        let mut paths = plan.paths.clone();
        paths.sort();
        let mut expected = vec![p(composed), p(decomposed)];
        expected.sort();
        assert_eq!(paths, expected);
        assert!(plan.renames.is_empty() && plan.hidden.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn plain_paths_are_untouched() {
        let plan = plan_case(
            &[p("x.txt"), p("gone.txt")],
            &server(&["x.txt", "new.txt"]),
            disk(&["x.txt"]),
            nothing_known,
        );
        let mut paths = plan.paths.clone();
        paths.sort();
        assert_eq!(paths, [p("gone.txt"), p("new.txt"), p("x.txt")]);
        assert!(plan.renames.is_empty() && plan.hidden.is_empty() && plan.held.is_empty());
    }

    #[test]
    fn local_only_requested_twice_is_planned_once() {
        let plan = plan_case(
            &[p("neu.txt"), p("Neu.txt")],
            &HashMap::new(),
            disk(&["Neu.txt"]),
            nothing_known,
        );
        assert_eq!(plan.paths, [p("Neu.txt")]);
    }

    #[test]
    fn twins_are_found_by_folded_name() {
        let twins = case_twins(["a.jpg", "A.jpg", "b.jpg", "M\u{00FC}ller", "Mu\u{0308}ller"].into_iter());
        assert_eq!(twins, HashSet::from([ntfs_fold("a.jpg")]));
    }

    #[test]
    fn ancestor_spelled_differently_on_disk_is_found() {
        let root = PathBuf::from(r"C:\S");
        // Disk: `C:\S\Photos\2025`. A sync under the server's `photos` must not run. Like
        // `on_disk_path`, the lookup returns the disk's spelling of the last name only.
        let on_disk = |path: &Path| {
            [p("Photos"), p(r"Photos\2025")]
                .into_iter()
                .find(|disk| same_on_ntfs(disk, path))
                .map(|disk| path.with_file_name(disk.file_name().unwrap()))
        };
        assert_eq!(
            case_mismatched_ancestor(&p(r"photos\2025"), &root, on_disk),
            Some(p("photos"))
        );
        assert_eq!(
            case_mismatched_ancestor(&p(r"Photos\2025"), &root, on_disk),
            None
        );
        // A folder missing on disk is no mismatch.
        assert_eq!(case_mismatched_ancestor(&p(r"Other\x"), &root, on_disk), None);
        assert_eq!(case_mismatched_ancestor(&root, &root, on_disk), None);
    }

    #[test]
    fn own_renames_match_once_and_only_their_target() {
        let renames = OwnRenames::default();
        renames.register(&p("foto.jpg"), &p("Foto.jpg"));
        assert!(!renames.matches(&p("foto.jpg"), &p("FOTO.jpg"), false));
        assert!(!renames.matches(&p("other.jpg"), &p("Foto.jpg"), false));
        assert!(renames.matches(&p("FOTO.JPG"), &p("Foto.jpg"), false));
        assert!(renames.matches(&p("foto.jpg"), &p("Foto.jpg"), true));
        assert!(!renames.matches(&p("foto.jpg"), &p("Foto.jpg"), false));
    }
}
