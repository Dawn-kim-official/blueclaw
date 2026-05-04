ALTER TABLE raw_event
  ADD COLUMN IF NOT EXISTS connector_event_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS connector_status text NOT NULL DEFAULT 'succeeded',
  ADD COLUMN IF NOT EXISTS connector_attempt_count integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS connector_next_attempt_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS connector_started_at timestamptz,
  ADD COLUMN IF NOT EXISTS connector_completed_at timestamptz,
  ADD COLUMN IF NOT EXISTS connector_error text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS connector_outbox (
  outbox_id text PRIMARY KEY,
  raw_event_id text NOT NULL REFERENCES raw_event(raw_event_id) ON DELETE CASCADE,
  platform text NOT NULL,
  reply_target_id text NOT NULL,
  reply_target_json jsonb NOT NULL,
  reply_json jsonb NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  attempt_count integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  completed_at timestamptz,
  dispatch_id text NOT NULL DEFAULT '',
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (raw_event_id)
);
