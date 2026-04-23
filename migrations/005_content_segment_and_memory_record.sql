CREATE TABLE IF NOT EXISTS content_segment (
  content_segment_id uuid PRIMARY KEY,
  source_kind text NOT NULL,
  raw_event_id uuid REFERENCES raw_event(raw_event_id) ON DELETE CASCADE,
  attachment_id uuid REFERENCES attachment(attachment_id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversation(conversation_id),
  owner_person_id uuid REFERENCES person(person_id),
  sequence_number integer NOT NULL,
  content_ciphertext bytea NOT NULL,
  encryption_key_version smallint NOT NULL,
  content_sha256 bytea NOT NULL,
  embedding_model text NOT NULL,
  embedding vector(768) NOT NULL,
  security_level_rank smallint NOT NULL,
  required_classes text[] NOT NULL DEFAULT '{}',
  occurred_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_record (
  memory_record_id uuid PRIMARY KEY,
  scope_type text NOT NULL,
  scope_person_id uuid REFERENCES person(person_id),
  scope_conversation_id uuid REFERENCES conversation(conversation_id),
  memory_type text NOT NULL,
  title text,
  content_ciphertext bytea NOT NULL,
  encryption_key_version smallint NOT NULL,
  content_sha256 bytea NOT NULL,
  embedding_model text NOT NULL,
  embedding vector(768) NOT NULL,
  security_level_rank smallint NOT NULL,
  required_classes text[] NOT NULL DEFAULT '{}',
  source_conversation_id text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  superseded_at timestamptz
);

CREATE TABLE IF NOT EXISTS memory_source (
  memory_source_id uuid PRIMARY KEY,
  memory_record_id uuid NOT NULL REFERENCES memory_record(memory_record_id) ON DELETE CASCADE,
  raw_event_id uuid REFERENCES raw_event(raw_event_id) ON DELETE CASCADE,
  attachment_id uuid REFERENCES attachment(attachment_id) ON DELETE CASCADE,
  content_segment_id uuid REFERENCES content_segment(content_segment_id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL
);
