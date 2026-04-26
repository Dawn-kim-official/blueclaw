ALTER TABLE memory_record
  ADD COLUMN IF NOT EXISTS source_platform text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS source_message_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS memory_record_scope_lookup_idx
  ON memory_record (scope_type, scope_person_id, scope_conversation_id, superseded_at, updated_at DESC);
