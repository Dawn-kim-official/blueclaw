CREATE TABLE IF NOT EXISTS task_run (
  task_run_id text PRIMARY KEY,
  requester_person_id text,
  origin_conversation_id text,
  current_agent_profile_name text NOT NULL,
  status text NOT NULL,
  prompt text NOT NULL,
  result text,
  failure_reason text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS task_step (
  task_step_id text PRIMARY KEY,
  task_run_id text NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  parent_task_step_id text,
  assigned_agent_profile_name text NOT NULL,
  instruction text NOT NULL,
  status text NOT NULL,
  output text
);

CREATE TABLE IF NOT EXISTS task_event (
  task_event_id text PRIMARY KEY,
  task_run_id text NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  name text NOT NULL,
  body text NOT NULL,
  created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS task_artifact (
  task_artifact_id text PRIMARY KEY,
  task_run_id text NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  name text NOT NULL,
  body text NOT NULL
);

CREATE TABLE IF NOT EXISTS task_wait_token (
  task_wait_token_id text PRIMARY KEY,
  person_id text REFERENCES person(person_id),
  task_run_id text NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  token_hash text NOT NULL,
  expires_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS task_session (
  task_session_id text PRIMARY KEY,
  person_id text REFERENCES person(person_id),
  expires_at timestamptz NOT NULL
);
