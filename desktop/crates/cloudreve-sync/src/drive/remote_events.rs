use crate::{
    drive::{local::LocalFileInfo, case::same_on_ntfs, commands::MountCommand, mounts::Mount, sync::SyncMode},
};
use anyhow::{Context, Result};
use cloudreve_api::{
    api::explorer::FileEventsApi,
    models::explorer::{FileEvent, FileEventData, FileEventType},
};
use std::{
    collections::HashMap,
    path::{Path, PathBuf},
    sync::Arc,
    time::Duration,
};

const MAX_RETRIES: u32 = 5;
const INITIAL_BACKOFF_SECS: u64 = 1;
const MAX_BACKOFF_SECS: u64 = 32;
const LONG_RETRY_DELAY_SECS: u64 = 3600; // 1 hour

struct BackoffState {
    retry_count: u32,
    current_delay: Duration,
}

impl BackoffState {
    fn new() -> Self {
        Self {
            retry_count: 0,
            current_delay: Duration::from_secs(INITIAL_BACKOFF_SECS),
        }
    }

    fn reset(&mut self) {
        self.retry_count = 0;
        self.current_delay = Duration::from_secs(INITIAL_BACKOFF_SECS);
    }

    fn next_delay(&mut self) -> Option<Duration> {
        if self.retry_count >= MAX_RETRIES {
            return None;
        }
        let delay = self.current_delay;
        self.retry_count += 1;
        self.current_delay =
            Duration::from_secs((self.current_delay.as_secs() * 2).min(MAX_BACKOFF_SECS));
        Some(delay)
    }
}

enum ListenResult {
    Error(anyhow::Error),
    ReconnectRequired,
    StreamEnded,
}

impl Mount {
    pub async fn process_remote_events(s: Arc<Self>) {
        tracing::info!(target: "drive::remote_events", "Listening to remote events");
        let mut backoff = BackoffState::new();
        // Fylvo: know the rights (read-only shared folder) before the first full sync.
        s.refresh_capabilities().await;

        let sync_path = {
            let config = s.config.read().await;
            config.sync_path.clone()
        };

        loop {
            let result = s.listen_remote_events().await;
            match result {
                ListenResult::ReconnectRequired => {
                    tracing::info!(target: "drive::remote_events", "Reconnect required, re-subscribing immediately");
                    backoff.reset();
                    continue;
                }
                ListenResult::StreamEnded => {
                    tracing::warn!(target: "drive::remote_events", "Event stream ended unexpectedly, reconnecting");
                    backoff.reset();
                    continue;
                }
                ListenResult::Error(e) => {
                    if let Some(delay) = backoff.next_delay() {
                        tracing::error!(
                            target: "drive::remote_events",
                            error = %e,
                            retry_count = backoff.retry_count,
                            delay_secs = delay.as_secs(),
                            "Failed to listen to remote events, retrying"
                        );
                        tokio::time::sleep(delay).await;
                    } else {
                        tracing::error!(
                            target: "drive::remote_events",
                            error = %e,
                            "Max retries reached, waiting 1 hour before retrying. Triggerring full sync..."
                        );
                        tokio::time::sleep(Duration::from_secs(10)).await;
                        let _ = s.command_tx.send(MountCommand::Sync {
                            local_paths: vec![sync_path.clone()],
                            mode: SyncMode::FullHierarchy,
                            user_initiated: false,
                        });
                        tokio::time::sleep(Duration::from_secs(LONG_RETRY_DELAY_SECS)).await;
                        backoff.reset();
                    }
                }
            }
        }
    }

    async fn listen_remote_events(&self) -> ListenResult {
        let (remote_base, sync_path) = {
            let config = self.config.read().await;
            (config.remote_path.clone(), config.sync_path.clone())
        };

        let mut subscription = match self.cr_client.subscribe_file_events(&remote_base).await {
            Ok(sub) => sub,
            Err(e) => return ListenResult::Error(e.into()),
        };

        loop {
            match subscription.next_event().await {
                Ok(Some(event)) => match event {
                    FileEvent::Event(events) => {
                        // Fylvo: counted for the updater (activity.rs).
                        let _busy = crate::activity::enter().await;
                        tracing::trace!(target: "drive::remote_events", events = ?events, "Handling file events batch");
                        if let Err(e) = self.handle_file_events(sync_path.clone(), events).await {
                            tracing::error!(target: "drive::remote_events", error = ?e, "Failed to handle file events");
                        }
                    }
                    FileEvent::Resumed => {
                        self.set_event_push_subscribed(true).await;
                        tracing::debug!(target: "drive::remote_events", "Subscription resumed");
                    }
                    FileEvent::Subscribed => {
                        self.set_event_push_subscribed(true).await;
                        tracing::info!(target: "drive::remote_events", "New subscribtion, triggger full sync...");
                        let _ = self.command_tx.send(MountCommand::Sync {
                            local_paths: vec![sync_path.clone()],
                            mode: SyncMode::FullHierarchy,
                            user_initiated: false,
                        });
                    }
                    FileEvent::KeepAlive => {
                        tracing::trace!(target: "drive::remote_events", "Keep-alive");
                    }
                    FileEvent::ReconnectRequired => {
                        tracing::debug!(target: "drive::remote_events", "Reconnect required");
                        self.set_event_push_subscribed(false).await;
                        return ListenResult::ReconnectRequired;
                    }
                },
                Ok(None) => {
                    self.set_event_push_subscribed(false).await;
                    return ListenResult::StreamEnded;
                }
                Err(e) => {
                    self.set_event_push_subscribed(false).await;
                    return ListenResult::Error(e.into());
                }
            }
        }
    }

    async fn handle_file_events(
        &self,
        sync_root: PathBuf,
        events: Vec<FileEventData>,
    ) -> Result<()> {
        // Group events by type
        let mut create_update_events: Vec<FileEventData> = Vec::new();
        let mut rename_events: Vec<FileEventData> = Vec::new();
        let mut delete_events: Vec<FileEventData> = Vec::new();

        for event in events {
            match event.event_type {
                FileEventType::Create => create_update_events.push(event),
                FileEventType::Modify => create_update_events.push(event),
                FileEventType::Rename => rename_events.push(event),
                FileEventType::Delete => delete_events.push(event),
            }
        }

        // Handle Create events grouped by parent
        if !create_update_events.is_empty() {
            self.handle_create_update_events(sync_root.clone(), create_update_events)
                .await?;
        }

        // Handle Delete events
        if !delete_events.is_empty() {
            self.handle_delete_events(sync_root.clone(), delete_events)
                .await?;
        }

        // Handle Rename events
        if !rename_events.is_empty() {
            self.handle_rename_events(sync_root.clone(), rename_events)
                .await?;
        }

        Ok(())
    }

    async fn handle_rename_events(
        &self,
        sync_root: PathBuf,
        events: Vec<FileEventData>,
    ) -> Result<()> {
        // Fylvo: a server path with `.` or `..` parts could leave the sync folder.
        let events: Vec<FileEventData> = events
            .into_iter()
            .filter(|event| safe_server_path(&event.from) && safe_server_path(&event.to))
            .collect();
        // Handle rename as a combination of delete (from) and create (to)
        // Group by parent for both from paths (if they exist) and to paths
        let mut from_grouped_by_parent: HashMap<PathBuf, Vec<PathBuf>> = HashMap::new();
        let mut to_grouped_by_parent: HashMap<PathBuf, Vec<PathBuf>> = HashMap::new();

        for event in events {
            // Handle `from` path (like delete) - only if it exists locally
            let from_relative: PathBuf = event.from.trim_start_matches('/').split('/').collect();
            let local_from_path = sync_root.join(&from_relative);
            let to_relative: PathBuf = event.to.trim_start_matches('/').split('/').collect();
            let local_to_path = sync_root.join(&to_relative);

            // Fylvo: Linux keeps every file local; a rename on the server moves the local file
            // or folder along instead of trashing it and downloading it again.
            #[cfg(target_os = "linux")]
            if self
                .follow_server_rename(&local_from_path, &local_to_path, &event.file_id)
                .await
            {
                if let Some(parent) = local_to_path.parent() {
                    to_grouped_by_parent
                        .entry(parent.to_path_buf())
                        .or_default()
                        .push(local_to_path);
                }
                continue;
            }

            // Fylvo: only upper/lower case changed: on this PC both names are the same file.
            // Syncing the new name renames it here (see `case.rs`); the old name must not be
            // handled as a deletion.
            let case_only = local_from_path.parent() == local_to_path.parent()
                && same_on_ntfs(&local_from_path, &local_to_path);

            let from_exists = !case_only && match LocalFileInfo::from_path(&local_from_path) {
                Ok(info) => info.exists,
                Err(e) => {
                    tracing::trace!(
                        target: "drive::remote_events",
                        path = %local_from_path.display(),
                        error = ?e,
                        "Failed to get file info for rename from path, skipping"
                    );
                    false
                }
            };

            if from_exists {
                if let Some(parent) = local_from_path.parent() {
                    from_grouped_by_parent
                        .entry(parent.to_path_buf())
                        .or_default()
                        .push(local_from_path);
                }
            }

            // Handle `to` path (like create) - always process
            if let Some(parent) = local_to_path.parent() {
                to_grouped_by_parent
                    .entry(parent.to_path_buf())
                    .or_default()
                    .push(local_to_path);
            }
        }

        // Process from paths (deletions)
        for (parent, paths) in from_grouped_by_parent {
            // Fylvo: a rename on the server is no mass deletion, the files live on under the
            // new name.
            self.allow_local_deletion(paths.clone());
            if let Err(e) = self
                .sync_last_presented_parent(sync_root.clone(), parent, paths)
                .await
            {
                tracing::error!(
                    target: "drive::remote_events",
                    error = ?e,
                    "Failed to sync parent for rename from paths"
                );
            }
        }

        // Process to paths (creations)
        for (parent, paths) in to_grouped_by_parent {
            if let Err(e) = self
                .sync_last_presented_parent(sync_root.clone(), parent, paths)
                .await
            {
                tracing::error!(
                    target: "drive::remote_events",
                    error = ?e,
                    "Failed to sync parent for rename to paths"
                );
            }
        }

        Ok(())
    }

    /// Fylvo (Linux): move `from` to `to` here after the same rename on the server. Only for the
    /// file the server renamed (the row at `from` carries its file ID), only into a free name in
    /// an existing folder, with the checks of the case rename (`rename_local_case`: synced item,
    /// no held deletion, transfers stopped and queued again). Returns whether it moved.
    #[cfg(target_os = "linux")]
    async fn follow_server_rename(&self, from: &Path, to: &Path, file_id: &str) -> bool {
        let Some(from_text) = from.to_str() else {
            return false;
        };
        let row = match self.inventory.query_by_path(from_text) {
            Ok(Some(row)) => row,
            _ => return false,
        };
        let same_file = row
            .metadata
            .get(crate::drive::case::FILE_ID_KEY)
            .is_some_and(|id| !id.is_empty() && id == file_id);
        let root = self.config.read().await.sync_path.clone();
        if crate::drive::local_linux::is_foreign_below(from, &root)
            || crate::drive::local_linux::is_foreign_below(to, &root)
        {
            return false;
        }
        if self.is_ignored(to).await || self.is_ignored(from).await {
            return false;
        }
        // Only an unchanged synced file or a synced folder, and no rows under the new name:
        // anything else is left to the sync of both names (no notice needed).
        let info = crate::drive::local::LocalFileInfo::from_path(from).unwrap_or_else(|_| crate::drive::local::LocalFileInfo::missing());
        let synced = info.is_placeholder() && (info.is_directory || info.in_sync());
        let rows_at_target = to
            .to_str()
            .is_some_and(|text| self.inventory.has_rows_at_or_under(text).unwrap_or(true));
        if !synced || rows_at_target {
            return false;
        }
        let target_free = std::fs::symlink_metadata(to).is_err();
        let parent_here = to.parent().is_some_and(|parent| parent.is_dir());
        if !same_file || !target_free || !parent_here {
            return false;
        }
        match self.rename_local_case(from, to).await {
            Ok(()) => true,
            Err(err) => {
                tracing::info!(target: "drive::remote_events", from = %from.display(), to = %to.display(), error = ?err, "Server rename not followed here, syncing both names");
                false
            }
        }
    }

    async fn handle_delete_events(
        &self,
        sync_root: PathBuf,
        events: Vec<FileEventData>,
    ) -> Result<()> {
        // Group delete events by parent of `from` path, filtering out non-existent files
        let mut grouped_by_parent: HashMap<PathBuf, Vec<PathBuf>> = HashMap::new();

        for event in events {
            // Remote paths use Unix-style separators, convert to OS-native path
            let relative_path: PathBuf = event.from.trim_start_matches('/').split('/').collect();
            let local_from_path = sync_root.join(&relative_path);

            // Check if file exists locally, skip if not
            let path_info = match LocalFileInfo::from_path(&local_from_path) {
                Ok(info) => info,
                Err(e) => {
                    tracing::trace!(
                        target: "drive::remote_events",
                        path = %local_from_path.display(),
                        error = ?e,
                        "Failed to get file info for delete event, skipping"
                    );
                    continue;
                }
            };

            if !path_info.exists {
                tracing::trace!(
                    target: "drive::remote_events",
                    path = %local_from_path.display(),
                    "File does not exist locally for delete event, skipping"
                );
                continue;
            }

            if let Some(parent) = local_from_path.parent() {
                grouped_by_parent
                    .entry(parent.to_path_buf())
                    .or_default()
                    .push(local_from_path);
            }
        }

        // Process each group
        for (parent, paths) in grouped_by_parent {
            if let Err(e) = self
                .sync_last_presented_parent(sync_root.clone(), parent, paths)
                .await
            {
                tracing::error!(
                    target: "drive::remote_events",
                    error = ?e,
                    "Failed to sync parent for delete events"
                );
            }
        }

        Ok(())
    }

    async fn handle_create_update_events(
        &self,
        sync_root: PathBuf,
        events: Vec<FileEventData>,
    ) -> Result<()> {
        // Group create events by parent of `from` path
        let mut grouped_by_parent: HashMap<PathBuf, Vec<PathBuf>> = HashMap::new();

        for event in events {
            // Remote paths use Unix-style separators, convert to OS-native path
            let relative_path: PathBuf = event.from.trim_start_matches('/').split('/').collect();
            let local_from_path = sync_root.join(&relative_path);

            if let Some(parent) = local_from_path.parent() {
                grouped_by_parent
                    .entry(parent.to_path_buf())
                    .or_default()
                    .push(local_from_path);
            }
        }

        // Process each group
        for (parent, paths) in grouped_by_parent {
            if let Err(e) = self
                .sync_last_presented_parent(sync_root.clone(), parent, paths)
                .await
            {
                tracing::error!(
                    target: "drive::remote_events",
                    error = ?e,
                    "Failed to sync parent for create events"
                );
            }
        }

        Ok(())
    }

    async fn sync_last_presented_parent(
        &self,
        sync_root: PathBuf,
        initial_parent: PathBuf,
        local_paths: Vec<PathBuf>,
    ) -> Result<()> {
        // Walk up from initial_parent to find the first existing & populated parent
        let mut current_path = initial_parent.clone();
        let mut child_of_existing: Option<PathBuf> = None;

        loop {
            // Check if we've gone above the sync root
            if !current_path.starts_with(&sync_root)
                || current_path == sync_root.parent().unwrap_or(Path::new(""))
            {
                tracing::warn!(
                    target: "drive::remote_events",
                    sync_root = %sync_root.display(),
                    initial_parent = %initial_parent.display(),
                    "File event parent is not under sync root, skipping"
                );
                return Ok(());
            }

            let path_info =
                LocalFileInfo::from_path(&current_path).context("failed to get path file info")?;

            if path_info.exists {
                if !path_info.is_placeholder() || path_info.is_folder_populated() {
                    // Found an existing & populated parent, sync from here
                    let (mode, sync_paths) = if let Some(child_path) = child_of_existing {
                        // We walked up, so sync the intermediate child folder
                        tracing::trace!(
                            target: "drive::remote_events",
                            existing_parent = %current_path.display(),
                            child_path = %child_path.display(),
                            "Syncing intermediate child path with PathOnly"
                        );
                        (SyncMode::PathOnly, vec![child_path])
                    } else if local_paths.len() > 1 {
                        // Multiple paths in same parent - sync parent with first layer
                        tracing::trace!(
                            target: "drive::remote_events",
                            parent_path = %current_path.display(),
                            path_count = local_paths.len(),
                            "Syncing parent path with PathAndFirstLayer for multiple new events"
                        );
                        (SyncMode::PathAndFirstLayer, vec![current_path.clone()])
                    } else {
                        // Single path - sync only that path
                        tracing::trace!(
                            target: "drive::remote_events",
                            parent_path = %current_path.display(),
                            "Syncing single path for new event"
                        );
                        (SyncMode::PathOnly, local_paths.clone())
                    };

                    self.command_tx
                        .send(MountCommand::Sync {
                            local_paths: sync_paths,
                            mode,
                            user_initiated: false,
                        })
                        .context("failed to send sync command")?;
                    return Ok(());
                } else {
                    tracing::trace!(
                        target: "drive::remote_events",
                        parent_path = %current_path.display(),
                        "Parent path is a placeholder and not populated, skipping"
                    );
                    return Ok(());
                }
            }

            // Current path doesn't exist, walk up to its parent
            // Remember current_path as the child that will need syncing
            child_of_existing = Some(current_path.clone());

            match current_path.parent() {
                Some(parent) => {
                    current_path = parent.to_path_buf();
                }
                None => {
                    tracing::warn!(
                        target: "drive::remote_events",
                        sync_root = %sync_root.display(),
                        "Reached filesystem root without finding existing parent"
                    );
                    return Ok(());
                }
            }
        }
    }
}

/// Fylvo: whether a server path has no empty, `.` or `..` parts (it then stays inside the sync
/// folder when joined to it).
fn safe_server_path(path: &str) -> bool {
    path.trim_start_matches('/')
        .split('/')
        .all(|part| !matches!(part, "." | ".."))
}
