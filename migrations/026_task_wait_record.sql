DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'task_wait_token'
      AND column_name = 'task_wait_token_id'
  ) AND NOT EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'task_wait_token'
      AND column_name = 'wait_id'
  ) THEN
    ALTER TABLE task_wait_token RENAME COLUMN task_wait_token_id TO wait_id;
  END IF;
END $$;

ALTER TABLE task_wait_token
  ADD COLUMN IF NOT EXISTS platform text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS conversation_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS reply_target_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS thread_root_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS dispatch_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS interaction_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT 'open',
  ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS resolved_at timestamptz;

ALTER TABLE task_wait_token
  ALTER COLUMN token_hash DROP NOT NULL;

CREATE INDEX IF NOT EXISTS task_wait_token_open_reply_target_idx
  ON task_wait_token(person_id, platform, conversation_id, reply_target_id)
  WHERE state = 'open';

CREATE INDEX IF NOT EXISTS task_wait_token_open_thread_root_idx
  ON task_wait_token(person_id, platform, conversation_id, thread_root_id)
  WHERE state = 'open';

CREATE INDEX IF NOT EXISTS task_wait_token_open_dispatch_idx
  ON task_wait_token(person_id, platform, conversation_id, dispatch_id)
  WHERE state = 'open';

CREATE INDEX IF NOT EXISTS task_wait_token_open_interaction_idx
  ON task_wait_token(person_id, task_run_id, interaction_id)
  WHERE state = 'open';
