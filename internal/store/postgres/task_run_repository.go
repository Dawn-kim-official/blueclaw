package postgres

import (
	"context"
	"database/sql"
	"errors"

	"blueclaw/internal/task"
)

type TaskRunRepository struct {
	database Database
}

func NewTaskRunRepository(database Database) TaskRunRepository {
	return TaskRunRepository{database: database}
}

func (taskRunRepository TaskRunRepository) InsertTaskRun(taskRun task.TaskRun) error {
	return taskRunRepository.SaveTaskRun(taskRun)
}

func (taskRunRepository TaskRunRepository) SaveTaskRun(taskRun task.TaskRun) error {
	_, errorValue := taskRunRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_run (
  task_run_id, requester_person_id, origin_conversation_id, current_agent_profile_name,
  status, prompt, result, failure_reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (task_run_id) DO UPDATE SET
  current_agent_profile_name = EXCLUDED.current_agent_profile_name,
  status = EXCLUDED.status,
  result = EXCLUDED.result,
  failure_reason = EXCLUDED.failure_reason,
  updated_at = EXCLUDED.updated_at`,
		taskRun.TaskRunID,
		emptyStringAsNil(taskRun.RequesterPersonID),
		emptyStringAsNil(taskRun.OriginConversationID),
		taskRun.CurrentAgentProfileName,
		string(taskRun.Status),
		taskRun.Prompt,
		taskRun.Result,
		taskRun.FailureReason,
		taskRun.CreatedAt,
		taskRun.UpdatedAt,
	)
	return errorValue
}

func (taskRunRepository TaskRunRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	row := taskRunRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT task_run_id, COALESCE(requester_person_id, ''), COALESCE(origin_conversation_id, ''),
  current_agent_profile_name, status, prompt, COALESCE(result, ''), COALESCE(failure_reason, ''),
  created_at, updated_at
FROM task_run WHERE task_run_id = $1`, taskRunID)
	taskRun, errorValue := scanTaskRun(row)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return task.TaskRun{}, false, nil
	}
	return taskRun, errorValue == nil, errorValue
}

func (taskRunRepository TaskRunRepository) ListTaskRun() ([]task.TaskRun, error) {
	rows, errorValue := taskRunRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_run_id, COALESCE(requester_person_id, ''), COALESCE(origin_conversation_id, ''),
  current_agent_profile_name, status, prompt, COALESCE(result, ''), COALESCE(failure_reason, ''),
  created_at, updated_at
FROM task_run ORDER BY created_at DESC`)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskRuns(rows)
}

func (taskRunRepository TaskRunRepository) ListTaskRunByPersonID(personID string) ([]task.TaskRun, error) {
	rows, errorValue := taskRunRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_run_id, COALESCE(requester_person_id, ''), COALESCE(origin_conversation_id, ''),
  current_agent_profile_name, status, prompt, COALESCE(result, ''), COALESCE(failure_reason, ''),
  created_at, updated_at
FROM task_run WHERE requester_person_id = $1 ORDER BY created_at DESC`, personID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskRuns(rows)
}

type taskRunScanner interface {
	Scan(...any) error
}

func scanTaskRun(scanner taskRunScanner) (task.TaskRun, error) {
	var taskRun task.TaskRun
	var status string
	errorValue := scanner.Scan(
		&taskRun.TaskRunID,
		&taskRun.RequesterPersonID,
		&taskRun.OriginConversationID,
		&taskRun.CurrentAgentProfileName,
		&status,
		&taskRun.Prompt,
		&taskRun.Result,
		&taskRun.FailureReason,
		&taskRun.CreatedAt,
		&taskRun.UpdatedAt,
	)
	taskRun.Status = task.TaskStatus(status)
	return taskRun, errorValue
}

func scanTaskRuns(rows *sql.Rows) ([]task.TaskRun, error) {
	taskRuns := []task.TaskRun{}
	for rows.Next() {
		taskRun, errorValue := scanTaskRun(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		taskRuns = append(taskRuns, taskRun)
	}
	return taskRuns, rows.Err()
}
