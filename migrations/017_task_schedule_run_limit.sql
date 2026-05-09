ALTER TABLE task_schedule
  ADD COLUMN IF NOT EXISTS max_run_count integer,
  ADD COLUMN IF NOT EXISTS completed_run_count integer NOT NULL DEFAULT 0;
