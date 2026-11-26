use super::InventoryDb;
use crate::drive::{
    case::{ntfs_like_pattern, same_on_ntfs},
    guard::is_under_any_case,
};
use crate::inventory::{NewTaskRecord, TaskRecord, TaskStatus, TaskUpdate};
use anyhow::{Context, Result, anyhow};
use chrono::Utc;
use diesel::prelude::*;
use diesel::sqlite::SqliteConnection;

use crate::inventory::schema::task_queue::{self, dsl as task_queue_dsl};

impl InventoryDb {
    /// Insert a task queue record if no pending/running task with the same type and path exists
    /// (in any upper/lower case spelling: one file on NTFS, see `cancel_tasks_by_path`).
    /// Returns `true` if the task was inserted, `false` if a duplicate was found.
    pub fn insert_task_if_not_exist(&self, task: &NewTaskRecord) -> Result<bool> {
        let mut conn = self.connection()?;

        let active_statuses = vec![
            TaskStatus::Pending.as_str().to_string(),
            TaskStatus::Running.as_str().to_string(),
        ];

        let row = NewTaskRow::try_from(task)?;
        // Fylvo: check and insert in one write transaction, so two tasks queued at the same
        // moment cannot both go in.
        conn.immediate_transaction(|conn| {
            let paths: Vec<String> = task_queue_dsl::task_queue
                .filter(task_queue_dsl::drive_id.eq(&task.drive_id))
                .filter(task_queue_dsl::task_type.eq(&task.task_type))
                .filter(task_queue_dsl::status.eq_any(&active_statuses))
                .filter(
                    task_queue_dsl::local_path
                        .like(ntfs_like_pattern(&task.local_path))
                        .escape('!'),
                )
                .select(task_queue_dsl::local_path)
                .load(conn)
                .context("Failed to check for existing task")?;

            let new_path = std::path::Path::new(&task.local_path);
            if paths
                .iter()
                .any(|path| same_on_ntfs(std::path::Path::new(path), new_path))
            {
                return Ok(false);
            }

            diesel::insert_into(task_queue::table)
                .values(&row)
                .execute(conn)
                .context("Failed to insert task queue record")?;
            Ok(true)
        })
    }

    /// Fylvo: change a task's status only if it still has `from`, in one statement. Returns
    /// whether it changed. Lets a starting task and a case rename (see `drive/case.rs`) claim a
    /// pending task without both winning.
    pub fn change_task_status_if(
        &self,
        task_id: &str,
        from: TaskStatus,
        to: TaskStatus,
    ) -> Result<bool> {
        let mut conn = self.connection()?;
        let changed = diesel::update(
            task_queue_dsl::task_queue
                .filter(task_queue_dsl::id.eq(task_id))
                .filter(task_queue_dsl::status.eq(from.as_str())),
        )
        .set((
            task_queue_dsl::status.eq(to.as_str()),
            task_queue_dsl::updated_at.eq(Utc::now().timestamp()),
        ))
        .execute(&mut conn)
        .context("Failed to change task status")?;
        Ok(changed > 0)
    }

    /// Update task queue record
    pub fn update_task(&self, task_id: &str, update: TaskUpdate) -> Result<()> {
        if update.is_empty() {
            return Ok(());
        }

        let mut conn = self.connection()?;
        let changeset = TaskChangeset::try_from(update)?;
        diesel::update(task_queue_dsl::task_queue.filter(task_queue_dsl::id.eq(task_id)))
            .set(changeset)
            .execute(&mut conn)?;
        Ok(())
    }

    /// List task queue records with optional filters
    pub fn list_tasks(
        &self,
        drive_id: Option<&str>,
        statuses: Option<&[TaskStatus]>,
    ) -> Result<Vec<TaskRecord>> {
        let mut conn = self.connection()?;
        let mut query = task_queue_dsl::task_queue.into_boxed();

        if let Some(drive) = drive_id {
            query = query.filter(task_queue_dsl::drive_id.eq(drive));
        }

        if let Some(status_filter) = statuses {
            let values: Vec<String> = status_filter
                .iter()
                .map(|status| status.as_str().to_string())
                .collect();
            query = query.filter(task_queue_dsl::status.eq_any(values));
        }

        let rows = query
            .order(task_queue_dsl::created_at.asc())
            .load::<TaskRow>(&mut conn)
            .context("Failed to query task queue records")?;

        rows.into_iter()
            .map(TaskRecord::try_from)
            .collect::<Result<Vec<_>>>()
    }

    /// Delete a completed/failed task entry
    pub fn delete_task(&self, task_id: &str) -> Result<()> {
        let mut conn = self.connection()?;
        diesel::delete(task_queue_dsl::task_queue.filter(task_queue_dsl::id.eq(task_id)))
            .execute(&mut conn)
            .context("Failed to delete task queue record")?;
        Ok(())
    }

    /// Cancel all pending/running tasks matching a path or its descendants, in any upper/lower
    /// case spelling (one file on NTFS; a task may run under the spelling on disk).
    /// Returns the list of task IDs that were cancelled.
    pub fn cancel_tasks_by_path(&self, drive_id: &str, path: &str) -> Result<Vec<String>> {
        let mut conn = self.connection()?;

        let active_statuses = vec![
            TaskStatus::Pending.as_str().to_string(),
            TaskStatus::Running.as_str().to_string(),
        ];

        let roots = [std::path::PathBuf::from(path)];
        let pattern = ntfs_like_pattern(path);
        let matching_tasks: Vec<TaskRow> = task_queue_dsl::task_queue
            .filter(task_queue_dsl::drive_id.eq(drive_id))
            .filter(task_queue_dsl::status.eq_any(&active_statuses))
            .filter(
                task_queue_dsl::local_path
                    .like(pattern.clone())
                    .escape('!')
                    .or(task_queue_dsl::local_path
                        .like(format!("{pattern}{}%", std::path::MAIN_SEPARATOR))
                        .escape('!')),
            )
            .load::<TaskRow>(&mut conn)
            .context("Failed to query tasks by path")?
            .into_iter()
            .filter(|task| is_under_any_case(std::path::Path::new(&task.local_path), &roots))
            .collect();

        let task_ids: Vec<String> = matching_tasks.iter().map(|t| t.id.clone()).collect();

        if !task_ids.is_empty() {
            let cancelled_status = TaskStatus::Cancelled.as_str().to_string();
            let now = chrono::Utc::now().timestamp();

            diesel::update(task_queue_dsl::task_queue.filter(task_queue_dsl::id.eq_any(&task_ids)))
                .set((
                    task_queue_dsl::status.eq(&cancelled_status),
                    task_queue_dsl::updated_at.eq(now),
                ))
                .execute(&mut conn)
                .context("Failed to cancel tasks by path")?;
        }

        Ok(task_ids)
    }

    /// Get task status by task ID
    pub fn get_task_status(&self, task_id: &str) -> Result<Option<TaskStatus>> {
        let mut conn = self.connection()?;
        let row: Option<String> = task_queue_dsl::task_queue
            .filter(task_queue_dsl::id.eq(task_id))
            .select(task_queue_dsl::status)
            .first(&mut conn)
            .optional()
            .context("Failed to query task status")?;

        match row {
            Some(status_str) => Ok(TaskStatus::from_str(&status_str)),
            None => Ok(None),
        }
    }

    /// Query recent tasks for status summary.
    /// Returns up to 25 pending/running tasks and up to 25 completed/failed/cancelled tasks,
    /// ordered by updated_at descending.
    pub fn query_recent_tasks(&self, drive_id: Option<&str>) -> Result<RecentTasks> {
        let mut conn = self.connection()?;
        let active = newest_tasks(&mut conn, &[TaskStatus::Pending, TaskStatus::Running], drive_id)
            .context("Failed to query active tasks")?;
        let finished = newest_tasks(
            &mut conn,
            &[TaskStatus::Completed, TaskStatus::Failed, TaskStatus::Cancelled],
            drive_id,
        )
        .context("Failed to query finished tasks")?;
        Ok(RecentTasks { active, finished })
    }
}

/// The 25 tasks with one of `statuses` updated last. One query per status, each answered from
/// the index on (status, updated_at) without reading the other tasks.
fn newest_tasks(
    conn: &mut SqliteConnection,
    statuses: &[TaskStatus],
    drive_id: Option<&str>,
) -> Result<Vec<TaskRecord>> {
    const LIMIT: i64 = 25;
    let mut rows = Vec::new();
    for status in statuses {
        let mut query = task_queue_dsl::task_queue
            .filter(task_queue_dsl::status.eq(status.as_str()))
            .into_boxed();
        if let Some(drive) = drive_id {
            query = query.filter(task_queue_dsl::drive_id.eq(drive));
        }
        rows.extend(
            query
                .order(task_queue_dsl::updated_at.desc())
                .limit(LIMIT)
                .load::<TaskRow>(conn)?,
        );
    }
    rows.sort_by_key(|row| std::cmp::Reverse(row.updated_at));
    rows.truncate(LIMIT as usize);
    rows.into_iter().map(TaskRecord::try_from).collect()
}

/// Result of querying recent tasks
#[derive(Debug, Clone)]
pub struct RecentTasks {
    /// Pending and running tasks (up to 25)
    pub active: Vec<TaskRecord>,
    /// Completed, failed, and cancelled tasks (up to 25)
    pub finished: Vec<TaskRecord>,
}

// =========================================================================
// Row Types
// =========================================================================

#[derive(Queryable)]
struct TaskRow {
    id: String,
    drive_id: String,
    task_type: String,
    local_path: String,
    status: String,
    progress: f64,
    total_bytes: i64,
    processed_bytes: i64,
    priority: i32,
    custom_state: Option<String>,
    error: Option<String>,
    created_at: i64,
    updated_at: i64,
}

impl TryFrom<TaskRow> for TaskRecord {
    type Error = anyhow::Error;

    fn try_from(row: TaskRow) -> Result<Self> {
        let status = TaskStatus::from_str(&row.status)
            .ok_or_else(|| anyhow!("Unknown task status value {}", row.status))?;
        let custom_state = match row.custom_state {
            Some(json) => Some(
                serde_json::from_str(&json).context("Failed to deserialize task custom_state")?,
            ),
            None => None,
        };

        Ok(TaskRecord {
            id: row.id,
            drive_id: row.drive_id,
            task_type: row.task_type,
            local_path: row.local_path,
            status,
            progress: row.progress,
            total_bytes: row.total_bytes,
            processed_bytes: row.processed_bytes,
            priority: row.priority,
            custom_state,
            error: row.error,
            created_at: row.created_at,
            updated_at: row.updated_at,
        })
    }
}

#[derive(Insertable)]
#[diesel(table_name = task_queue)]
struct NewTaskRow {
    id: String,
    drive_id: String,
    task_type: String,
    local_path: String,
    status: String,
    progress: f64,
    total_bytes: i64,
    processed_bytes: i64,
    priority: i32,
    custom_state: Option<String>,
    error: Option<String>,
    created_at: i64,
    updated_at: i64,
}

impl TryFrom<&NewTaskRecord> for NewTaskRow {
    type Error = anyhow::Error;

    fn try_from(record: &NewTaskRecord) -> Result<Self> {
        Ok(Self {
            id: record.id.clone(),
            drive_id: record.drive_id.clone(),
            task_type: record.task_type.clone(),
            local_path: record.local_path.clone(),
            status: record.status.as_str().to_string(),
            progress: record.progress,
            total_bytes: record.total_bytes,
            processed_bytes: record.processed_bytes,
            priority: record.priority,
            custom_state: match &record.custom_state {
                Some(value) => Some(
                    serde_json::to_string(value)
                        .context("Failed to serialize task custom_state")?,
                ),
                None => None,
            },
            error: record.error.clone(),
            created_at: record.created_at,
            updated_at: record.updated_at,
        })
    }
}

#[derive(AsChangeset)]
#[diesel(table_name = task_queue)]
struct TaskChangeset {
    status: Option<String>,
    progress: Option<f64>,
    total_bytes: Option<i64>,
    processed_bytes: Option<i64>,
    custom_state: Option<Option<String>>,
    error: Option<Option<String>>,
    updated_at: i64,
}

impl TaskChangeset {
    fn try_from(update: TaskUpdate) -> Result<Self> {
        let custom_state = match update.custom_state {
            Some(Some(value)) => Some(Some(
                serde_json::to_string(&value).context("Failed to serialize task custom_state")?,
            )),
            Some(None) => Some(None),
            None => None,
        };

        let error_state = match update.error {
            Some(Some(err)) => Some(Some(err)),
            Some(None) => Some(None),
            None => None,
        };

        Ok(Self {
            status: update.status.map(|status| status.as_str().to_string()),
            progress: update.progress,
            total_bytes: update.total_bytes,
            processed_bytes: update.processed_bytes,
            custom_state,
            error: error_state,
            updated_at: Utc::now().timestamp(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::native;

    #[test]
    fn status_changes_only_from_the_expected_status() {
        let dir = tempfile::tempdir().unwrap();
        let db = InventoryDb::with_path(dir.path().join("meta.db")).unwrap();
        let task = NewTaskRecord {
            id: "t1".into(),
            drive_id: "d".into(),
            task_type: "upload".into(),
            local_path: native(r"C:\S\a.txt").into(),
            status: TaskStatus::Pending,
            progress: 0.0,
            total_bytes: 0,
            processed_bytes: 0,
            priority: 0,
            custom_state: None,
            error: None,
            created_at: 0,
            updated_at: 0,
        };
        assert!(db.insert_task_if_not_exist(&task).unwrap());
        // A case rename cancels the pending task: the queue can no longer start it.
        assert!(db.change_task_status_if("t1", TaskStatus::Pending, TaskStatus::Cancelled).unwrap());
        assert!(!db.change_task_status_if("t1", TaskStatus::Pending, TaskStatus::Running).unwrap());
        assert_eq!(db.get_task_status("t1").unwrap(), Some(TaskStatus::Cancelled));
    }

    #[test]
    fn recent_tasks_are_the_newest_of_all_their_statuses() {
        let dir = tempfile::tempdir().unwrap();
        let db = InventoryDb::with_path(dir.path().join("meta.db")).unwrap();
        let statuses = [TaskStatus::Completed, TaskStatus::Failed, TaskStatus::Cancelled, TaskStatus::Pending];
        for i in 0..80 {
            let mut task = NewTaskRecord::new(format!("t{i}"), if i % 5 == 0 { "e" } else { "d" }, "upload", format!("/S/{i}"));
            task.status = statuses[i % 4];
            task.updated_at = i as i64;
            assert!(db.insert_task_if_not_exist(&task).unwrap());
        }
        let recent = db.query_recent_tasks(None).unwrap();
        let finished: Vec<i64> = recent.finished.iter().map(|t| t.updated_at).collect();
        let expected: Vec<i64> = (0..80).rev().filter(|i| i % 4 != 3).take(25).collect();
        assert_eq!(finished, expected);
        assert_eq!(recent.active.len(), 20);
        let of_e = db.query_recent_tasks(Some("e")).unwrap();
        assert!(of_e.finished.iter().chain(&of_e.active).all(|t| t.drive_id == "e"));
        assert_eq!(of_e.finished.len() + of_e.active.len(), 16);
    }

    #[cfg_attr(not(windows), allow(dead_code))]
    fn task(id: &str, path: &str) -> NewTaskRecord {
        NewTaskRecord::new(id, "d", "upload", path)
    }

    #[test]
    #[cfg(windows)]
    fn cancel_by_path_finds_tasks_in_any_case_spelling() {
        let dir = tempfile::tempdir().unwrap();
        let db = InventoryDb::with_path(dir.path().join("meta.db")).unwrap();
        // Queued as `report.txt`, it runs as `Report.txt` after a case rename on the server.
        assert!(db.insert_task_if_not_exist(&task("t1", native(r"C:\S\report.txt"))).unwrap());
        assert!(db.insert_task_if_not_exist(&task("t2", native(r"C:\S\docs\b.txt"))).unwrap());
        assert!(db.insert_task_if_not_exist(&task("t3", native(r"C:\S\reports.txt"))).unwrap());
        // The same file in another spelling is queued already.
        assert!(!db.insert_task_if_not_exist(&task("t4", native(r"C:\S\Report.TXT"))).unwrap());
        // Letters outside ASCII, `%` and `_` in names.
        assert!(db.insert_task_if_not_exist(&task("t5", native(r"C:\S\Ünd_100%.txt"))).unwrap());
        assert!(!db.insert_task_if_not_exist(&task("t6", native(r"C:\S\ünd_100%.TXT"))).unwrap());
        assert!(db.insert_task_if_not_exist(&task("t7", native(r"C:\S\Undx100x.txt"))).unwrap());
        let mut cancelled = db.cancel_tasks_by_path("d", native(r"C:\S\Report.txt")).unwrap();
        cancelled.extend(db.cancel_tasks_by_path("d", native(r"C:\S\Docs")).unwrap());
        cancelled.sort();
        assert_eq!(cancelled, ["t1", "t2"]);
        assert_eq!(db.get_task_status("t3").unwrap(), Some(TaskStatus::Pending));
    }
}
