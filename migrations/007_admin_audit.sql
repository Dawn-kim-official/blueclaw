CREATE TABLE IF NOT EXISTS policy_revision (
  policy_revision_id uuid PRIMARY KEY,
  policy_sha256 bytea NOT NULL UNIQUE,
  backup_file_path text NOT NULL,
  changed_by_person_id uuid REFERENCES person(person_id),
  changed_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_audit_log (
  admin_audit_log_id uuid PRIMARY KEY,
  actor_person_id uuid REFERENCES person(person_id),
  action_name text NOT NULL,
  target_type text NOT NULL,
  target_identifier text NOT NULL,
  before_sha256 bytea,
  after_sha256 bytea,
  metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL
);
