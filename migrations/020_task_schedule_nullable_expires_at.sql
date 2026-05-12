ALTER TABLE task_schedule
  ALTER COLUMN expires_at DROP DEFAULT,
  ALTER COLUMN expires_at DROP NOT NULL;

UPDATE task_schedule
SET expires_at = NULL
WHERE expires_at = '9999-12-31 23:59:59+00';
