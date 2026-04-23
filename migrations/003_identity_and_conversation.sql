CREATE TABLE IF NOT EXISTS platform_account (
  platform_account_id uuid PRIMARY KEY,
  platform text NOT NULL,
  external_user_id text NOT NULL,
  email citext,
  display_name text NOT NULL,
  person_id uuid REFERENCES person(person_id),
  is_approved_internal boolean NOT NULL DEFAULT false,
  last_seen_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (platform, external_user_id)
);

CREATE TABLE IF NOT EXISTS conversation (
  conversation_id uuid PRIMARY KEY,
  platform text NOT NULL,
  external_conversation_id text NOT NULL,
  conversation_type text NOT NULL,
  display_name text,
  last_seen_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (platform, external_conversation_id)
);
