DROP INDEX IF EXISTS idx_task_queue_drive_status_updated;
DROP INDEX IF EXISTS idx_task_queue_status_updated;
CREATE INDEX IF NOT EXISTS idx_task_queue_status ON task_queue(status);
