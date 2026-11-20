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
};
use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use cloudreve_api::models::explorer::{FileResponse, file_type};
use std::{
    collections::{BTreeMap, HashMap, HashSet},
    path::{Path, PathBuf},
};

/// A name as NTFS compares it: each character in its simple upper case form when that is one
/// character of the Basic Multilingual Plane (like the volume's upcase table: `ü` = `Ü`, but
/// `ß` stays `ß`, not `SS`). No Unicode normalisation.
pub fn ntfs_fold(name: &str) -> String {
    name.chars()
        .map(|c| {
            let mut upper = c.to_uppercase();
            match (upper.next(), upper.next()) {
                (Some(u), None) if (u as u32) <= 0xFFFF && (c as u32) <= 0xFFFF => u,
                _ => c,
            }
        })
        .collect()
}

/// Whether two paths are the same file on NTFS.
pub fn same_on_ntfs(a: &Path, b: &Path) -> bool {
    ntfs_fold(&a.to_string_lossy()) == ntfs_fold(&b.to_string_lossy())
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

/// What the inventory knows about a path: folder or not, and the server's creation time.
pub(crate) type KnownEntry = (bool, i64);

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
/// kind and creation time, which a rename does not change.
fn is_same_object(
    disk: &Path,
    server: &Path,
    remote: &FileResponse,
    known: &impl Fn(&Path) -> Option<KnownEntry>,
) -> bool {
    let row = match (known(disk), known(server)) {
        (Some(row), None) | (None, Some(row)) => row,
        _ => return false,
    };
    let created = remote
        .created_at
        .parse::<DateTime<Utc>>()
        .map(|time| time.timestamp());
    row.0 == (remote.file_type == file_type::FOLDER) && created.is_ok_and(|time| time == row.1)
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
            Some((row.is_folder, row.created_at))
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
        self.report_case_clash(&plan.hidden, &plan.held);

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

        // Tasks under the old spelling would reach the server under the old name.
        let cancelled = self
            .task_queue
            .cancel_by_path(from.to_path_buf())
            .await
            .unwrap_or(0);

        // The inventory first: it refuses when rows under the new spelling exist already, and
        // then nothing has changed on disk.
        self.inventory
            .rename_path(from_str, to_str)
            .context("inventory rows under the new name exist")?;
        if let Err(err) = move_no_replace(from, to) {
            let _ = self.inventory.rename_path(to_str, from_str);
            return Err(err);
        }

        if was_in_sync && !LocalFileInfo::from_path(to)?.in_sync() {
            crate::drive::commands::mark_path_in_sync(to).await?;
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
    pub(crate) fn report_case_clash(&self, hidden: &[PathBuf], held: &[PathBuf]) {
        let mut notified = self.case_clash_notified.lock().unwrap();
        let mut fresh = hidden
            .iter()
            .chain(held)
            .filter(|path| notified.insert((*path).clone()));
        let Some(first) = fresh.next() else {
            return;
        };
        tracing::warn!(target: "drive::case", id = %self.id, path = %first.display(), "Names that differ only in case, not synced");
        let name = first
            .file_name()
            .map(|name| name.to_string_lossy().to_string())
            .unwrap_or_default();
        let others = fresh.count();
        let message = if others == 0 {
            t!("caseClashMessage", "name" => name)
        } else {
            t!("caseClashMessageMore", "name" => name, "count" => others)
        };
        crate::utils::toast::send_warning_toast(&t!("caseClashTitle"), &message);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const CREATED: &str = "2026-11-19T10:00:00Z";
    const CREATED_TS: i64 = 1_795_082_400;

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

    /// The inventory has a row for each of `names`.
    fn known(names: &'static [&'static str], folder: bool) -> impl Fn(&Path) -> Option<KnownEntry> {
        move |path: &Path| {
            names
                .iter()
                .any(|name| p(name) == path)
                .then_some((folder, CREATED_TS))
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
    fn fold_is_like_ntfs() {
        assert_eq!(ntfs_fold("foto.JPG"), ntfs_fold("Foto.jpg"));
        assert_eq!(ntfs_fold("Übung"), ntfs_fold("übung"));
        // NTFS does not turn one character into two.
        assert_ne!(ntfs_fold("Straße"), ntfs_fold("STRASSE"));
        // Composed and decomposed umlauts are two names on NTFS.
        assert_ne!(ntfs_fold("M\u{00FC}ller"), ntfs_fold("Mu\u{0308}ller"));
        assert_ne!(ntfs_fold("M\u{00DC}LLER"), ntfs_fold("mu\u{0308}ller"));
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
        // Local `a.jpg` was server `a.jpg`; that was deleted and a different `A.jpg` remains
        // (other creation time or kind): not the same object, so no rename.
        let other_kind = plan_case(
            &[p("a.jpg")],
            &server(&["A.jpg"]),
            disk(&["a.jpg"]),
            known(&["a.jpg"], true),
        );
        assert_eq!(other_kind.held, [p("A.jpg")]);
        let other_time = plan_case(
            &[p("a.jpg")],
            &server(&["A.jpg"]),
            disk(&["a.jpg"]),
            |path: &Path| (path == p("a.jpg")).then_some((false, CREATED_TS + 1)),
        );
        assert_eq!(other_time.held, [p("A.jpg")]);
        assert!(other_time.renames.is_empty());
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
}
