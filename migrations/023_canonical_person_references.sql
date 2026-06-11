DO $$
BEGIN
  ALTER TABLE task_schedule
    ADD CONSTRAINT task_schedule_creator_person_id_present
    CHECK (creator_person_id IS NOT NULL) NOT VALID;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
  ALTER TABLE task_run
    ADD CONSTRAINT task_run_requester_person_id_fkey
    FOREIGN KEY (requester_person_id) REFERENCES person(person_id) NOT VALID;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;
