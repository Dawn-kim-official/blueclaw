CREATE TABLE IF NOT EXISTS task_run (
  task_run_id uuid PRIMARY KEY,
  requester_person_id uuid REFERENCES person(person_id),
  origin_conversation_id uuid REFERENCES conversation(conversation_id),
  current_agent_profile_name text NOT NULL,
  status text NOT NULL,
  prompt text NOT NULL,
  result text,
  failure_reason text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS task_step (
  task_step_id uuid PRIMARY KEY,
  task_run_id uuid NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  parent_task_step_id uuid,
  assigned_agent_profile_name text NOT NULL,
  instruction text NOT NULL,
  status text NOT NULL,
  output text
);

CREATE TABLE IF NOT EXISTS task_event (
  task_event_id uuid PRIMARY KEY,
  task_run_id uuid NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  name text NOT NULL,
  body text NOT NULL,
  created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS task_artifact (
  task_artifact_id uuid PRIMARY KEY,
  task_run_id uuid NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  name text NOT NULL,
  body text NOT NULL
);

CREATE TABLE IF NOT EXISTS task_wait_token (
  task_wait_token_id uuid PRIMARY KEY,
  person_id uuid REFERENCES person(person_id),
  task_run_id uuid NOT NULL REFERENCES task_run(task_run_id) ON DELETE CASCADE,
  token_hash text NOT NULL,
  expires_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS task_session (
  task_session_id uuid PRIMARY KEY,
  person_id uuid REFERENCES person(person_id),
  expires_at timestamptz NOT NULL
);
