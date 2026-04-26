CREATE TABLE IF NOT EXISTS graphiti_namespace (
  namespace_id text PRIMARY KEY,
  scope_type text NOT NULL,
  scope_person_id text REFERENCES person(person_id),
  scope_conversation_id text,
  security_level_rank smallint NOT NULL,
  required_classes text[] NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS graphiti_episode (
  episode_id text PRIMARY KEY,
  source_platform text NOT NULL,
  source_message_id text NOT NULL,
  conversation_id text NOT NULL,
  sender_person_id text NOT NULL REFERENCES person(person_id),
  namespace_document jsonb NOT NULL,
  ingestion_status text NOT NULL,
  ingestion_error text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (source_platform, source_message_id)
);

CREATE INDEX IF NOT EXISTS graphiti_episode_conversation_lookup_idx
  ON graphiti_episode (conversation_id, updated_at DESC);
