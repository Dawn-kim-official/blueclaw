ALTER TABLE task_schedule
  ADD COLUMN IF NOT EXISTS expires_at timestamptz NOT NULL DEFAULT '9999-12-31 23:59:59+00';

CREATE INDEX IF NOT EXISTS task_schedule_active_due_index
  ON task_schedule (next_run_at, next_attempt_at, expires_at)
  WHERE next_run_at IS NOT NULL;
