CREATE TABLE IF NOT EXISTS person (
  person_id uuid PRIMARY KEY,
  display_name text NOT NULL,
  security_level_name text NOT NULL,
  security_level_rank smallint NOT NULL,
  granted_classes text[] NOT NULL DEFAULT '{}',
  is_admin boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS person_email (
  person_email_id uuid PRIMARY KEY,
  person_id uuid NOT NULL REFERENCES person(person_id) ON DELETE CASCADE,
  email citext NOT NULL UNIQUE,
  is_primary boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS policy_channel_rule (
  policy_channel_rule_id uuid PRIMARY KEY,
  platform text NOT NULL,
  external_conversation_id text NOT NULL,
  conversation_type text NOT NULL,
  display_name text NOT NULL,
  default_security_level_rank smallint NOT NULL,
  default_required_classes text[] NOT NULL DEFAULT '{}',
  is_collect_enabled boolean NOT NULL DEFAULT true,
  is_reply_enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (platform, external_conversation_id)
);
