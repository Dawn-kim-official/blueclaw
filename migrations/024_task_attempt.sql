CREATE TABLE IF NOT EXISTS task_attempt (
  task_attempt_id text PRIMARY KEY,
  task_run_id text NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  runner_id text NOT NULL,
  status text NOT NULL,
  started_at timestamptz NOT NULL,
  finished_at timestamptz,
  failure_reason text
);

ALTER TABLE task_run ADD COLUMN IF NOT EXISTS current_attempt_id text;

CREATE INDEX IF NOT EXISTS task_attempt_task_run_id_idx ON task_attempt(task_run_id);
CREATE INDEX IF NOT EXISTS task_attempt_status_idx ON task_attempt(status);
