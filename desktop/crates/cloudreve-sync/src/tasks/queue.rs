use crate::drive::guard::is_under_any_case;
use crate::inventory::{InventoryDb, NewTaskRecord, TaskRecord, TaskStatus, TaskUpdate};
use crate::tasks::download::DownloadTask;
use crate::tasks::types::{TaskKind, TaskPayload, TaskProgress};
use crate::tasks::upload::UploadTask;
use anyhow::{Context, Result, anyhow};
use cloudreve_api::Client;
use dashmap::DashMap;
use serde_json::Value;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use tokio::sync::{
    Mutex, Notify, Semaphore,
    mpsc::{self, UnboundedReceiver, UnboundedSender},
};
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
use tracing::{debug, error, info, warn};
use uuid::Uuid;

#[derive(Debug, Clone)]
pub struct TaskQueueConfig {
    pub max_concurrent: usize,
}

impl Default for TaskQueueConfig {
    fn default() -> Self {
        Self { max_concurrent: 2 }
    }
}

pub struct TaskQueue {
    pub drive_id: String,
    pub cr_client: Arc<Client>,
    pub inventory: Arc<InventoryDb>,
    pub sync_path: PathBuf,
    pub remote_base: String,
    config: TaskQueueConfig,
    semaphore: Arc<Semaphore>,
    command_tx: UnboundedSender<QueueCommand>,
    dispatcher_handle: Mutex<Option<JoinHandle<()>>>,
    inflight: AtomicUsize,
    idle_notify: Notify,
    shutting_down: AtomicBool,
    cancel_requested: AtomicBool,
    progress: Arc<DashMap<String, TaskProgress>>,
    task_handles: DashMap<String, JoinHandle<()>>,
    /// Maps task_id to local_path for running tasks, used for path-based cancellation
    task_paths: DashMap<String, String>,
    /// Fylvo: cancels a running upload including its chunk workers, which aborting the task
    /// handle alone does not stop.
    cancel_tokens: DashMap<String, CancellationToken>,
    /// Fylvo: files with an upload waiting to be queued later (see `upload_later`).
    later: DashMap<String, ()>,
}

impl TaskQueue {
    pub async fn new(
        drive_id: impl Into<String>,
        cr_client: Arc<Client>,
        inventory: Arc<InventoryDb>,
        config: TaskQueueConfig,
        sync_path: PathBuf,
        remote_base: String,
    ) -> Arc<Self> {
        let drive_id = drive_id.into();
        let max_concurrent = config.max_concurrent.max(1);
        let sanitized_config = TaskQueueConfig { max_concurrent };

        let (command_tx, command_rx) = mpsc::unbounded_channel();
        let queue = Arc::new(Self {
            drive_id,
            inventory,
            cr_client,
            sync_path,
            remote_base,
            config: sanitized_config,
            semaphore: Arc::new(Semaphore::new(max_concurrent)),
            command_tx,
            dispatcher_handle: Mutex::new(None),
            inflight: AtomicUsize::new(0),
            idle_notify: Notify::new(),
            shutting_down: AtomicBool::new(false),
            cancel_requested: AtomicBool::new(false),
            progress: Arc::new(DashMap::new()),
            task_handles: DashMap::new(),
            task_paths: DashMap::new(),
            cancel_tokens: DashMap::new(),
            later: DashMap::new(),
        });

        queue.spawn_dispatcher(command_rx).await;
        if let Err(err) = queue.resume_incomplete_tasks().await {
            warn!(
                target: "tasks::queue",
                drive = %queue.drive_id,
                error = %err,
                "Failed to resume pending tasks from inventory"
            );
        }
        queue
    }

    pub fn max_concurrent(&self) -> usize {
        self.config.max_concurrent
    }

    pub fn drive_id(&self) -> &str {
        &self.drive_id
    }

    /// Queue a task, unless one of the same kind is pending or running for the path already.
    pub async fn enqueue(&self, payload: TaskPayload) -> Result<()> {
        if self.shutting_down.load(Ordering::SeqCst) {
            return Err(anyhow!("task queue is shutting down"));
        }

        let task_id = payload
            .task_id
            .clone()
            .unwrap_or_else(|| Uuid::new_v4().to_string());

        let mut record = NewTaskRecord::new(
            task_id.clone(),
            self.drive_id.clone(),
            payload.kind.as_str().to_string(),
            payload.local_path_display(),
        )
        .with_priority(payload.priority);

        match (payload.total_bytes, payload.processed_bytes) {
            (Some(total), Some(processed)) => {
                record = record.with_totals(total, processed);
            }
            (Some(total), None) => {
                record = record.with_totals(total, 0);
            }
            (None, Some(processed)) => {
                record = record.with_totals(0, processed);
            }
            _ => {}
        }

        if let Some(state) = payload.custom_state.clone() {
            record = record.with_custom_state(state);
        }

        let inserted = self
            .inventory
            .insert_task_if_not_exist(&record)
            .with_context(|| format!("Failed to persist task {}", task_id))?;

        if !inserted {
            // Fylvo: queued or running already; that task takes the file as it is then.
            debug!(
                target: "tasks::queue",
                drive = %self.drive_id,
                path = %payload.local_path_display(),
                kind = payload.kind.as_str(),
                "Task already queued"
            );
            return Ok(());
        }

        let payload = payload.with_task_id(task_id.clone());
        self.dispatch_task(task_id, payload)
    }

    pub fn list_active_tasks(&self) -> Result<Vec<TaskRecord>> {
        self.inventory.list_tasks(
            Some(&self.drive_id),
            Some(&[TaskStatus::Pending, TaskStatus::Running]),
        )
    }

    pub async fn ongoing_progress(&self) -> Vec<TaskProgress> {
        self.progress
            .iter()
            .map(|entry| entry.value().clone())
            .collect()
    }

    fn dispatch_task(&self, task_id: String, payload: TaskPayload) -> Result<()> {
        let command = QueueCommand::Enqueue(QueuedTask { task_id, payload });
        self.command_tx
            .send(command)
            .context("Task dispatcher closed")?;
        Ok(())
    }

    pub async fn persist_progress(
        &self,
        task_id: &str,
        progress: f64,
        processed_bytes: Option<i64>,
        total_bytes: Option<i64>,
        custom_state: Option<Value>,
    ) -> Result<()> {
        let clamped = progress.clamp(0.0, 1.0);
        if let Some(mut entry) = self.progress.get_mut(task_id) {
            entry.update(clamped, processed_bytes, total_bytes, custom_state);
            Ok(())
        } else {
            Err(anyhow!("No progress entry for task {}", task_id))
        }
    }

    pub async fn shutdown(&self) {
        if self.shutting_down.swap(true, Ordering::SeqCst) {
            return;
        }

        self.cancel_requested.store(true, Ordering::SeqCst);

        if let Err(err) = self.command_tx.send(QueueCommand::Shutdown) {
            warn!(target: "tasks::queue", error = %err, "Task queue dispatcher already closed");
        }

        if let Some(handle) = self.dispatcher_handle.lock().await.take() {
            handle.abort();
        }

        self.cancel_running_tasks().await;
        self.task_handles.clear();
        self.task_paths.clear();
        self.progress.clear();
    }

    /// Cancel all tasks for a given path or its descendants.
    /// This will:
    /// 1. Mark pending tasks in inventory as cancelled
    /// 2. Abort running tasks that match the path
    /// 3. Tasks in the channel queue will check their status upon scheduling and exit early
    ///
    /// Returns the number of tasks that were cancelled.
    pub async fn cancel_by_path(&self, path: impl AsRef<std::path::Path>) -> Result<usize> {
        let path_str = path.as_ref().to_string_lossy().to_string();

        info!(
            target: "tasks::queue",
            drive = %self.drive_id,
            path = %path_str,
            "Cancelling tasks by path"
        );

        // 1. Cancel pending tasks in inventory (this also marks running tasks as cancelled)
        let cancelled_ids = self
            .inventory
            .cancel_tasks_by_path(&self.drive_id, &path_str)
            .context("Failed to cancel tasks in inventory")?;

        let cancelled_count = cancelled_ids.len();

        // 2. Abort running task handles that match the path (any upper/lower case spelling)
        let roots = [path.as_ref().to_path_buf()];
        let tasks_to_abort: Vec<String> = self
            .task_paths
            .iter()
            .filter(|entry| is_under_any_case(std::path::Path::new(entry.value()), &roots))
            .map(|entry| entry.key().clone())
            .collect();

        for task_id in tasks_to_abort {
            // Abort first, so the task does not take the cancel for a failure; then stop its
            // chunk workers, which run as tasks of their own.
            if let Some((_, handle)) = self.task_handles.remove(&task_id) {
                handle.abort();
                debug!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task_id,
                    "Aborted running task"
                );
            }
            if let Some((_, token)) = self.cancel_tokens.remove(&task_id) {
                token.cancel();
            }
            self.task_paths.remove(&task_id);
            self.progress.remove(&task_id);
        }

        if cancelled_count > 0 {
            info!(
                target: "tasks::queue",
                drive = %self.drive_id,
                path = %path_str,
                count = cancelled_count,
                "Cancelled tasks by path"
            );
        }

        Ok(cancelled_count)
    }

    async fn spawn_dispatcher(self: &Arc<Self>, command_rx: UnboundedReceiver<QueueCommand>) {
        let queue = Arc::clone(self);
        let handle = tokio::spawn(async move {
            queue.run_dispatch_loop(command_rx).await;
        });
        *self.dispatcher_handle.lock().await = Some(handle);
    }

    async fn run_dispatch_loop(self: Arc<Self>, mut command_rx: UnboundedReceiver<QueueCommand>) {
        info!(
            target: "tasks::queue",
            drive = %self.drive_id,
            concurrency = self.config.max_concurrent,
            "Task queue dispatcher started"
        );

        while let Some(command) = command_rx.recv().await {
            match command {
                QueueCommand::Enqueue(task) => {
                    self.launch_task(task).await;
                }
                QueueCommand::Shutdown => {
                    debug!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        "Task queue dispatcher shutting down"
                    );
                    break;
                }
            }
        }

        info!(
            target: "tasks::queue",
            drive = %self.drive_id,
            "Task queue dispatcher stopped"
        );
    }

    async fn launch_task(self: &Arc<Self>, task: QueuedTask) {
        let permit = match self.semaphore.clone().acquire_owned().await {
            Ok(permit) => permit,
            Err(err) => {
                error!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    error = %err,
                    "Failed to acquire semaphore permit"
                );
                if let Err(update_err) = self.inventory.update_task(
                    &task.task_id,
                    TaskUpdate {
                        status: Some(TaskStatus::Failed),
                        error: Some(Some("Failed to schedule task".to_string())),
                        ..Default::default()
                    },
                ) {
                    warn!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        error = %update_err,
                        "Failed to persist scheduling failure"
                    );
                }
                return;
            }
        };

        self.inflight.fetch_add(1, Ordering::SeqCst);
        let queue_for_execute = Arc::clone(self);
        let queue_for_notify = Arc::clone(self);
        let task_id = task.task_id.clone();
        let handle_task_id = task_id.clone();

        let handle = tokio::spawn(async move {
            queue_for_execute.execute_task(task).await;
            drop(permit);
            queue_for_notify.inflight.fetch_sub(1, Ordering::SeqCst);
            queue_for_notify.idle_notify.notify_waiters();
            queue_for_notify.task_handles.remove(&handle_task_id);
        });

        self.task_handles.insert(task_id, handle);
    }

    async fn execute_task(self: Arc<Self>, task: QueuedTask) {
        // Check if task was cancelled while in the channel queue
        match self.inventory.get_task_status(&task.task_id) {
            Ok(Some(TaskStatus::Cancelled)) => {
                debug!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task.task_id,
                    "Task was cancelled before execution, skipping"
                );
                return;
            }
            Ok(Some(status)) if !status.is_active() => {
                debug!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task.task_id,
                    status = ?status,
                    "Task is no longer active, skipping"
                );
                return;
            }
            Err(err) => {
                warn!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task.task_id,
                    error = %err,
                    "Failed to check task status, proceeding with execution"
                );
            }
            _ => {}
        }

        // Fylvo: claim the task only while it is still pending, so a cancel that came in since
        // the check above wins (a case rename relies on it, see `drive/case.rs`).
        match self.inventory.change_task_status_if(
            &task.task_id,
            TaskStatus::Pending,
            TaskStatus::Running,
        ) {
            Ok(true) => {}
            Ok(false) => {
                debug!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task.task_id,
                    "Task is no longer pending, skipping"
                );
                return;
            }
            Err(err) => {
                error!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task.task_id,
                    error = %err,
                    "Failed to mark task as running"
                );
                return;
            }
        }

        // Register task path for path-based cancellation
        self.task_paths
            .insert(task.task_id.clone(), task.payload.local_path_display());
        // Fylvo: a cancel by path marks the row first and then looks for the path. One that came
        // in between the claim and the line above found no path: it is seen here.
        if !matches!(
            self.inventory.get_task_status(&task.task_id),
            Ok(Some(TaskStatus::Running))
        ) {
            self.cleanup_task_entry(&task.task_id).await;
            return;
        }

        self.register_progress_entry(&task).await;

        match self.run_placeholder_task(&task).await {
            Ok(TaskRunState::Completed) => {
                if let Err(err) = self.inventory.update_task(
                    &task.task_id,
                    TaskUpdate {
                        status: Some(TaskStatus::Completed),
                        ..Default::default()
                    },
                ) {
                    warn!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        task_id = %task.task_id,
                        error = %err,
                        "Failed to mark task as completed"
                    );
                }
            }
            Ok(TaskRunState::UploadAgain(path, delay)) => {
                // Fylvo: the file changed during the upload (or a download kept a local
                // change). This task is done; a new one uploads the newer content, unless the
                // task was cancelled meanwhile (the file was renamed, deleted or is downloaded:
                // what happens to it is decided there).
                let completed = self.inventory.change_task_status_if(
                    &task.task_id,
                    TaskStatus::Running,
                    TaskStatus::Completed,
                );
                self.cleanup_task_entry(&task.task_id).await;
                if matches!(completed, Ok(true)) {
                    self.upload_later(path, delay);
                }
                return;
            }
            // Fylvo: stopped by a shutdown: the row stays Running, so the next start resumes it.
            Ok(TaskRunState::Cancelled) if self.shutting_down.load(Ordering::SeqCst) => {
                self.cleanup_task_entry(&task.task_id).await;
                return;
            }
            Ok(TaskRunState::Cancelled) => {
                if let Err(err) = self.inventory.update_task(
                    &task.task_id,
                    TaskUpdate {
                        status: Some(TaskStatus::Cancelled),
                        ..Default::default()
                    },
                ) {
                    warn!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        task_id = %task.task_id,
                        error = %err,
                        "Failed to mark task as cancelled"
                    );
                }
                self.cleanup_task_entry(&task.task_id).await;
                return;
            }
            Err(err) => {
                error!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %task.task_id,
                    error = ?err,
                    "Task execution failed"
                );
                // Fylvo: refused with 40905 while a case rename may be on its way (see
                // `drive/case.rs`): tried again a little later, a few times. Not after a cancel.
                let cancelled = matches!(
                    self.inventory.get_task_status(&task.task_id),
                    Ok(Some(TaskStatus::Cancelled))
                );
                if task.payload.kind == TaskKind::Upload
                    && !cancelled
                    && crate::drive::case::is_name_case_taken(&err)
                {
                    let path = &task.payload.local_path;
                    if let Some(delay) = crate::tasks::upload::retry_name_taken_after(path) {
                        self.upload_later(path.clone(), delay);
                    }
                }
                if let Err(update_err) = self.inventory.update_task(
                    &task.task_id,
                    TaskUpdate {
                        status: Some(TaskStatus::Failed),
                        error: Some(Some(format!("{:?}", err))),
                        ..Default::default()
                    },
                ) {
                    warn!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        task_id = %task.task_id,
                        error = %update_err,
                        "Failed to persist task failure state"
                    );
                }
                self.cleanup_task_entry(&task.task_id).await;
                return;
            }
        }

        self.cleanup_task_entry(&task.task_id).await;
    }

    /// Fylvo: queue an upload of `path` after `delay` (at once for none). A task for the path
    /// that is queued by then takes its place.
    fn upload_later(self: &Arc<Self>, path: PathBuf, delay: Duration) {
        // One waiting upload per file, however many uploads ask for one meanwhile.
        let key = crate::drive::case::ntfs_fold(&path.to_string_lossy());
        if !delay.is_zero() && self.later.insert(key.clone(), ()).is_some() {
            return;
        }
        let queue = Arc::clone(self);
        // Fylvo: a waiting retry counts as work for the updater (activity.rs) from now until
        // it is queued.
        let busy = crate::activity::enter_now();
        tokio::spawn(async move {
            let _busy = busy;
            if !delay.is_zero() {
                tokio::time::sleep(delay).await;
                queue.later.remove(&key);
            }
            if let Err(err) = queue.enqueue(TaskPayload::upload(path.clone())).await {
                warn!(target: "tasks::queue", drive = %queue.drive_id, path = %path.display(), error = %err, "Failed to queue the upload again");
            }
        });
    }

    async fn run_placeholder_task(&self, task: &QueuedTask) -> Result<TaskRunState> {
        info!(
            target: "tasks::queue",
            drive = %self.drive_id,
            task_id = %task.task_id,
            kind = task.payload.kind.as_str(),
            path = %task.payload.local_path_display(),
            "Executing task"
        );

        match &task.payload.kind {
            TaskKind::Upload => {
                // Fylvo: a task queued before the file or a folder above it got another
                // upper/lower case spelling (see `drive/case.rs`) uploads under the spelling on
                // disk, the one the server has.
                let spelled =
                    crate::drive::case::disk_spelling(&task.payload.local_path, &self.sync_path);
                let mut respelled = QueuedTask {
                    task_id: task.task_id.clone(),
                    payload: task.payload.clone(),
                };
                if spelled != task.payload.local_path {
                    info!(
                        target: "tasks::queue",
                        task_id = %task.task_id,
                        path = %task.payload.local_path_display(),
                        on_disk = %spelled.display(),
                        "Spelled differently on disk, uploading under that spelling"
                    );
                    respelled.payload.local_path = spelled;
                }
                let token = CancellationToken::new();
                self.cancel_tokens.insert(task.task_id.clone(), token.clone());
                // Fylvo: a shutdown that swept the tasks before this one was registered.
                if self.shutting_down.load(Ordering::SeqCst) {
                    return Ok(TaskRunState::Cancelled);
                }
                let mut task_executor = UploadTask::new(
                    self.inventory.clone(),
                    self.cr_client.clone(),
                    self.drive_id.as_str(),
                    &respelled,
                    self.sync_path.clone(),
                    self.remote_base.clone(),
                    Arc::clone(&self.progress),
                )
                .with_cancel_token(token);

                task_executor.execute().await?;
                if task_executor.changed_meanwhile() {
                    let delay = crate::tasks::upload::upload_again_after(&task.payload.local_path);
                    return Ok(TaskRunState::UploadAgain(respelled.payload.local_path, delay));
                }
            }
            TaskKind::Download => {
                let mut task_executor = DownloadTask::new(
                    self.inventory.clone(),
                    self.cr_client.clone(),
                    self.drive_id.as_str(),
                    &task,
                    self.sync_path.clone(),
                    self.remote_base.clone(),
                    Arc::clone(&self.progress),
                );

                task_executor.execute().await?;
                // Fylvo: the local change it kept goes to the server (and meets the newer
                // version there as a conflict).
                if task_executor.kept_local_change() {
                    return Ok(TaskRunState::UploadAgain(
                        task.payload.local_path.clone(),
                        Duration::ZERO,
                    ));
                }
            }
        }

        // for step in 0..PLACEHOLDER_STEPS {
        //     if self.cancel_requested.load(Ordering::SeqCst) {
        //         return Ok(TaskRunState::Cancelled);
        //     }
        //     sleep(Duration::from_millis(250)).await;
        //     let progress = (step + 1) as f64 / PLACEHOLDER_STEPS as f64;
        //     let direction = match task.payload.kind {
        //         TaskKind::Upload => "upload",
        //         TaskKind::Download => "download",
        //     };
        //     let state = json!({
        //         "step": step + 1,
        //         "total_steps": PLACEHOLDER_STEPS,
        //         "local_path": task.payload.local_path_display(),
        //         "kind": direction,
        //     });
        //     if let Err(err) = self
        //         .persist_progress(
        //             &task.task_id,
        //             progress,
        //             task.payload.processed_bytes,
        //             task.payload.total_bytes,
        //             Some(state),
        //         )
        //         .await
        //     {
        //         warn!(
        //             target: "tasks::queue",
        //             drive = %self.drive_id,
        //             task_id = %task.task_id,
        //             error = %err,
        //             "Failed to persist placeholder progress"
        //         );
        //     }
        // }

        Ok(TaskRunState::Completed)
    }

    #[allow(dead_code)]
    async fn wait_for_idle(&self) {
        while self.inflight.load(Ordering::SeqCst) > 0 {
            self.idle_notify.notified().await;
        }
    }

    async fn register_progress_entry(&self, task: &QueuedTask) {
        self.progress.insert(
            task.task_id.clone(),
            TaskProgress::from_payload(&task.task_id, &task.payload),
        );
    }

    #[allow(dead_code)]
    async fn clear_progress_entry(&self, task_id: &str) {
        self.progress.remove(task_id);
    }

    async fn cleanup_task_entry(&self, task_id: &str) {
        self.progress.remove(task_id);
        self.task_paths.remove(task_id);
        self.cancel_tokens.remove(task_id);
    }

    async fn resume_incomplete_tasks(self: &Arc<Self>) -> Result<()> {
        let records = self.inventory.list_tasks(
            Some(&self.drive_id),
            Some(&[TaskStatus::Pending, TaskStatus::Running]),
        )?;

        if records.is_empty() {
            return Ok(());
        }

        let mut resumed = 0usize;
        for record in records {
            if record.status == TaskStatus::Running {
                if let Err(err) = self.inventory.update_task(
                    &record.id,
                    TaskUpdate {
                        status: Some(TaskStatus::Pending),
                        ..Default::default()
                    },
                ) {
                    warn!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        task_id = %record.id,
                        error = ?err,
                        "Failed to reset task status during resume"
                    );
                    continue;
                }
            }

            let payload = match Self::payload_from_record(&record) {
                Ok(payload) => payload,
                Err(err) => {
                    warn!(
                        target: "tasks::queue",
                        drive = %self.drive_id,
                        task_id = %record.id,
                        error = %err,
                        "Failed to build payload for resumed task"
                    );
                    continue;
                }
            };

            if let Err(err) = self.dispatch_task(record.id.clone(), payload) {
                warn!(
                    target: "tasks::queue",
                    drive = %self.drive_id,
                    task_id = %record.id,
                    error = ?err,
                    "Failed to dispatch resumed task"
                );
                continue;
            }

            resumed += 1;
        }

        if resumed > 0 {
            info!(
                target: "tasks::queue",
                drive = %self.drive_id,
                count = resumed,
                "Resumed pending tasks from inventory"
            );
        }

        Ok(())
    }

    async fn cancel_running_tasks(&self) {
        // Fylvo: every spawned task, also one that has not registered its progress yet.
        let running: Vec<String> = self
            .task_handles
            .iter()
            .map(|entry| entry.key().clone())
            .collect();

        for task_id in running {
            if let Some((_, handle)) = self.task_handles.remove(&task_id) {
                handle.abort();
            }
            if let Some((_, token)) = self.cancel_tokens.remove(&task_id) {
                token.cancel();
            }

            self.progress.remove(&task_id);
            self.task_paths.remove(&task_id);
        }
        // And every upload that registered its token but not yet its handle.
        for entry in self.cancel_tokens.iter() {
            entry.value().cancel();
        }
        self.cancel_tokens.clear();
    }

    fn payload_from_record(record: &TaskRecord) -> Result<TaskPayload> {
        let kind = TaskKind::from_str(&record.task_type)
            .ok_or_else(|| anyhow!("Unknown task type {}", record.task_type))?;

        let mut payload = TaskPayload::new(kind, PathBuf::from(&record.local_path))
            .with_priority(record.priority)
            .with_task_id(record.id.clone());

        let total_bytes = record.total_bytes;
        let processed_bytes = record.processed_bytes;
        if total_bytes != 0 || processed_bytes != 0 {
            payload = payload.with_totals(processed_bytes, total_bytes);
        }

        if let Some(state) = &record.custom_state {
            payload = payload.with_custom_state(state.clone());
        }

        Ok(payload)
    }
}

#[allow(dead_code)]
pub enum TaskRunState {
    Completed,
    Cancelled,
    /// Fylvo: the path holds content the server does not have yet: upload it after the delay.
    UploadAgain(PathBuf, Duration),
}

enum QueueCommand {
    Enqueue(QueuedTask),
    Shutdown,
}

pub struct QueuedTask {
    pub task_id: String,
    pub payload: TaskPayload,
}
