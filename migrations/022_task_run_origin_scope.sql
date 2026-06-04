ALTER TABLE task_run ADD COLUMN IF NOT EXISTS origin_reply_target_id text;
ALTER TABLE task_run ADD COLUMN IF NOT EXISTS origin_is_thread boolean NOT NULL DEFAULT false;
