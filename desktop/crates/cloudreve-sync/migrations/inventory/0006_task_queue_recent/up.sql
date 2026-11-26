DROP INDEX IF EXISTS idx_task_queue_status;
CREATE INDEX IF NOT EXISTS idx_task_queue_status_updated ON task_queue(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_task_queue_drive_status_updated ON task_queue(drive_id, status, updated_at);
