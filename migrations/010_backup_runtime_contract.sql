ALTER TABLE raw_event
  ADD COLUMN IF NOT EXISTS connector_result_json jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS backup_lock (
  lock_id text PRIMARY KEY,
  holder text NOT NULL,
  acquired_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL
);
