CREATE INDEX IF NOT EXISTS idx_task_queue_status_updated ON task_queue(status, updated_at);
