ALTER TABLE task_schedule
  ADD COLUMN IF NOT EXISTS platform text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS delivery_conversation_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS reply_target_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS time_zone text NOT NULL DEFAULT 'Asia/Seoul',
  ADD COLUMN IF NOT EXISTS lease_owner text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS leased_until timestamptz,
  ADD COLUMN IF NOT EXISTS failure_count integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_error text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS task_schedule_due_claim_index
  ON task_schedule (next_run_at, next_attempt_at)
  WHERE is_paused = false;
