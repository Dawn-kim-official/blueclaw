CREATE TABLE IF NOT EXISTS task_schedule (
  task_schedule_id uuid PRIMARY KEY,
  creator_person_id uuid REFERENCES person(person_id),
  name text NOT NULL,
  prompt text NOT NULL,
  agent_profile_name text NOT NULL,
  schedule_kind text NOT NULL CHECK (schedule_kind IN ('once', 'interval', 'cron')),
  run_at timestamptz,
  interval_second integer,
  cron_expression text,
  next_run_at timestamptz,
  last_run_at timestamptz,
  last_task_run_id uuid REFERENCES task_run(task_run_id),
  is_paused boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK (
    (schedule_kind = 'once' AND run_at IS NOT NULL AND interval_second IS NULL AND cron_expression IS NULL) OR
    (schedule_kind = 'interval' AND interval_second IS NOT NULL AND interval_second > 0 AND cron_expression IS NULL) OR
    (schedule_kind = 'cron' AND cron_expression IS NOT NULL AND interval_second IS NULL)
  )
);

CREATE INDEX IF NOT EXISTS task_schedule_next_run_at_index
  ON task_schedule (next_run_at)
  WHERE is_paused = false;
