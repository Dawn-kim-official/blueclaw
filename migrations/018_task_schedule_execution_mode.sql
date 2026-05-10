ALTER TABLE task_schedule
  ADD COLUMN IF NOT EXISTS execution_mode text NOT NULL DEFAULT 'agent';

DO $$
BEGIN
  ALTER TABLE task_schedule
    ADD CONSTRAINT task_schedule_execution_mode_check
    CHECK (execution_mode IN ('agent', 'message'));
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;
