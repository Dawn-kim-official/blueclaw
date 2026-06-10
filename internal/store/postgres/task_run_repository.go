package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

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
  task_run_id, requester_person_id, origin_conversation_id, origin_reply_target_id, origin_is_thread,
  current_attempt_id, current_agent_profile_name, status, prompt, result, failure_reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (task_run_id) DO UPDATE SET
  current_attempt_id = EXCLUDED.current_attempt_id,
  current_agent_profile_name = EXCLUDED.current_agent_profile_name,
  status = EXCLUDED.status,
  result = EXCLUDED.result,
  failure_reason = EXCLUDED.failure_reason,
  updated_at = EXCLUDED.updated_at`,
		taskRun.TaskRunID,
		emptyStringAsNil(taskRun.RequesterPersonID),
		emptyStringAsNil(taskRun.OriginConversationID),
		emptyStringAsNil(taskRun.OriginReplyTargetID),
		taskRun.OriginIsThread,
		emptyStringAsNil(taskRun.CurrentAttemptID),
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

func (taskRunRepository TaskRunRepository) StartTaskRunAttempt(taskRun task.TaskRun, taskAttempt task.TaskAttempt) error {
	transaction, errorValue := taskRunRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := taskRunRepository.saveTaskRunWithExecutor(transaction, taskRun); errorValue != nil {
		_ = transaction.Rollback()
		return errorValue
	}
	if errorValue := saveTaskAttemptWithExecutor(transaction, taskAttempt); errorValue != nil {
		_ = transaction.Rollback()
		return errorValue
	}
	return transaction.Commit()
}

func (taskRunRepository TaskRunRepository) FinishTaskRunAttempt(taskRun task.TaskRun, taskAttempt task.TaskAttempt) error {
	transaction, errorValue := taskRunRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := taskRunRepository.saveTaskRunWithExecutor(transaction, taskRun); errorValue != nil {
		_ = transaction.Rollback()
		return errorValue
	}
	if strings.TrimSpace(taskAttempt.TaskAttemptID) != "" {
		if errorValue := saveTaskAttemptWithExecutor(transaction, taskAttempt); errorValue != nil {
			_ = transaction.Rollback()
			return errorValue
		}
	}
	return transaction.Commit()
}

func (taskRunRepository TaskRunRepository) FindTaskAttempt(taskAttemptID string) (task.TaskAttempt, bool, error) {
	row := taskRunRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT task_attempt_id, task_run_id, runner_id, status, started_at, finished_at, COALESCE(failure_reason, '')
FROM task_attempt WHERE task_attempt_id = $1`, taskAttemptID)
	taskAttempt, errorValue := scanTaskAttempt(row)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return task.TaskAttempt{}, false, nil
	}
	return taskAttempt, errorValue == nil, errorValue
}

func (taskRunRepository TaskRunRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	row := taskRunRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT task_run_id, COALESCE(requester_person_id, ''), COALESCE(origin_conversation_id, ''),
  COALESCE(origin_reply_target_id, ''), COALESCE(origin_is_thread, false),
  COALESCE(current_attempt_id, ''), current_agent_profile_name, status, prompt, COALESCE(result, ''), COALESCE(failure_reason, ''),
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
  COALESCE(origin_reply_target_id, ''), COALESCE(origin_is_thread, false),
  COALESCE(current_attempt_id, ''), current_agent_profile_name, status, prompt, COALESCE(result, ''), COALESCE(failure_reason, ''),
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
  COALESCE(origin_reply_target_id, ''), COALESCE(origin_is_thread, false),
  COALESCE(current_attempt_id, ''), current_agent_profile_name, status, prompt, COALESCE(result, ''), COALESCE(failure_reason, ''),
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

type taskRunExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (taskRunRepository TaskRunRepository) saveTaskRunWithExecutor(executor taskRunExecutor, taskRun task.TaskRun) error {
	_, errorValue := executor.ExecContext(context.Background(), `
INSERT INTO task_run (
  task_run_id, requester_person_id, origin_conversation_id, origin_reply_target_id, origin_is_thread,
  current_attempt_id, current_agent_profile_name, status, prompt, result, failure_reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (task_run_id) DO UPDATE SET
  current_attempt_id = EXCLUDED.current_attempt_id,
  current_agent_profile_name = EXCLUDED.current_agent_profile_name,
  status = EXCLUDED.status,
  result = EXCLUDED.result,
  failure_reason = EXCLUDED.failure_reason,
  updated_at = EXCLUDED.updated_at`,
		taskRun.TaskRunID,
		emptyStringAsNil(taskRun.RequesterPersonID),
		emptyStringAsNil(taskRun.OriginConversationID),
		emptyStringAsNil(taskRun.OriginReplyTargetID),
		taskRun.OriginIsThread,
		emptyStringAsNil(taskRun.CurrentAttemptID),
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

func saveTaskAttemptWithExecutor(executor taskRunExecutor, taskAttempt task.TaskAttempt) error {
	_, errorValue := executor.ExecContext(context.Background(), `
INSERT INTO task_attempt (
  task_attempt_id, task_run_id, runner_id, status, started_at, finished_at, failure_reason
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (task_attempt_id) DO UPDATE SET
  runner_id = EXCLUDED.runner_id,
  status = EXCLUDED.status,
  finished_at = EXCLUDED.finished_at,
  failure_reason = EXCLUDED.failure_reason`,
		taskAttempt.TaskAttemptID,
		taskAttempt.TaskRunID,
		taskAttempt.RunnerID,
		string(taskAttempt.Status),
		taskAttempt.StartedAt,
		taskAttempt.FinishedAt,
		emptyStringAsNil(taskAttempt.FailureReason),
	)
	return errorValue
}

func scanTaskRun(scanner taskRunScanner) (task.TaskRun, error) {
	var taskRun task.TaskRun
	var status string
	errorValue := scanner.Scan(
		&taskRun.TaskRunID,
		&taskRun.RequesterPersonID,
		&taskRun.OriginConversationID,
		&taskRun.OriginReplyTargetID,
		&taskRun.OriginIsThread,
		&taskRun.CurrentAttemptID,
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

func scanTaskAttempt(scanner taskRunScanner) (task.TaskAttempt, error) {
	var taskAttempt task.TaskAttempt
	var status string
	var finishedAt sql.NullTime
	errorValue := scanner.Scan(
		&taskAttempt.TaskAttemptID,
		&taskAttempt.TaskRunID,
		&taskAttempt.RunnerID,
		&status,
		&taskAttempt.StartedAt,
		&finishedAt,
		&taskAttempt.FailureReason,
	)
	if finishedAt.Valid {
		finishedAtTime := finishedAt.Time
		taskAttempt.FinishedAt = &finishedAtTime
	}
	taskAttempt.Status = task.TaskAttemptStatus(status)
	return taskAttempt, errorValue
}
