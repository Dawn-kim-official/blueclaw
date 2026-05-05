ALTER TABLE person
  ADD COLUMN IF NOT EXISTS circles text[] NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS circle (
  circle_id text PRIMARY KEY,
  display_name text NOT NULL,
  mattermost_channel_id text NOT NULL DEFAULT '',
  is_mattermost_managed boolean NOT NULL DEFAULT false,
  workspace_directory_path text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS person_circle (
  person_circle_id text PRIMARY KEY,
  person_id text NOT NULL REFERENCES person(person_id) ON DELETE CASCADE,
  circle_id text NOT NULL REFERENCES circle(circle_id) ON DELETE CASCADE,
  source text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (person_id, circle_id)
);

CREATE TABLE IF NOT EXISTS resource_access_rule (
  resource_access_rule_id text PRIMARY KEY,
  resource text NOT NULL,
  actions text[] NOT NULL DEFAULT '{}',
  circles text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (resource, actions, circles)
);

CREATE TABLE IF NOT EXISTS mattermost_circle_link (
  circle_id text PRIMARY KEY REFERENCES circle(circle_id) ON DELETE CASCADE,
  channel_name text NOT NULL UNIQUE,
  channel_id text NOT NULL DEFAULT '',
  last_synced_at timestamptz
);

ALTER TABLE graphiti_namespace
  ADD COLUMN IF NOT EXISTS scope_circle_id text;

CREATE INDEX IF NOT EXISTS graphiti_namespace_circle_lookup_idx
  ON graphiti_namespace (scope_type, scope_circle_id, updated_at DESC);
