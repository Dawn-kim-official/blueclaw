ALTER TABLE raw_event
  ADD COLUMN IF NOT EXISTS reply_target_id text,
  ADD COLUMN IF NOT EXISTS visible_context_ciphertext bytea,
  ADD COLUMN IF NOT EXISTS visible_context_sha256 bytea,
  ADD COLUMN IF NOT EXISTS has_more_before boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS history_cursor text;
