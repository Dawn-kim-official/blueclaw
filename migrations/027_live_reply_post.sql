CREATE TABLE IF NOT EXISTS live_reply_post (
  task_run_id text NOT NULL,
  conversation_id text NOT NULL,
  post_id text NOT NULL,
  last_seq timestamptz NOT NULL,
  outbox_id text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (task_run_id, conversation_id)
);
