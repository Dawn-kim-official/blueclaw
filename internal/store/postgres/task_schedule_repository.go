package postgres

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/task"
)

type TaskScheduleRepository struct {
	database Database
}

func NewTaskScheduleRepository(database Database) TaskScheduleRepository {
	return TaskScheduleRepository{database: database}
}

func (taskScheduleRepository TaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	now := time.Now().UTC()
	if taskSchedule.CreatedAt.IsZero() {
		taskSchedule.CreatedAt = now
	}
	if taskSchedule.UpdatedAt.IsZero() {
		taskSchedule.UpdatedAt = now
	}
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_schedule (
  task_schedule_id, creator_person_id, name, prompt, execution_mode, agent_profile_name,
  schedule_kind, run_at, interval_second, cron_expression, next_run_at,
  last_run_at, last_task_run_id, expires_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone,
  lease_owner, leased_until, failure_count, last_error, next_attempt_at,
  max_run_count, completed_run_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
ON CONFLICT (task_schedule_id) DO UPDATE SET
  name = EXCLUDED.name,
  prompt = EXCLUDED.prompt,
  execution_mode = EXCLUDED.execution_mode,
  agent_profile_name = EXCLUDED.agent_profile_name,
  schedule_kind = EXCLUDED.schedule_kind,
  run_at = EXCLUDED.run_at,
  interval_second = EXCLUDED.interval_second,
  cron_expression = EXCLUDED.cron_expression,
  next_run_at = EXCLUDED.next_run_at,
  last_run_at = EXCLUDED.last_run_at,
  last_task_run_id = EXCLUDED.last_task_run_id,
  expires_at = EXCLUDED.expires_at,
  updated_at = EXCLUDED.updated_at,
  platform = EXCLUDED.platform,
  delivery_conversation_id = EXCLUDED.delivery_conversation_id,
  reply_target_id = EXCLUDED.reply_target_id,
  time_zone = EXCLUDED.time_zone,
  lease_owner = EXCLUDED.lease_owner,
  leased_until = EXCLUDED.leased_until,
  failure_count = EXCLUDED.failure_count,
  last_error = EXCLUDED.last_error,
  next_attempt_at = EXCLUDED.next_attempt_at,
  max_run_count = EXCLUDED.max_run_count,
  completed_run_count = EXCLUDED.completed_run_count`,
		taskSchedule.TaskScheduleID,
		emptyStringAsNil(taskSchedule.CreatorPersonID),
		taskSchedule.Name,
		taskSchedule.Prompt,
		normalizedTaskScheduleExecutionMode(taskSchedule.ExecutionMode),
		taskSchedule.AgentProfileName,
		string(taskSchedule.Kind),
		taskSchedule.RunAt,
		zeroAsNil(taskSchedule.IntervalSecond),
		emptyStringAsNil(taskSchedule.CronExpression),
		taskSchedule.NextRunAt,
		taskSchedule.LastRunAt,
		emptyStringAsNil(taskSchedule.LastTaskRunID),
		taskScheduleExpiresAt(taskSchedule),
		taskSchedule.CreatedAt,
		taskSchedule.UpdatedAt,
		taskSchedule.Platform,
		taskSchedule.ConversationID,
		taskSchedule.ReplyTargetID,
		firstNonEmptyPostgresString(taskSchedule.TimeZone, "Asia/Seoul"),
		taskSchedule.LeaseOwner,
		taskSchedule.LeasedUntil,
		taskSchedule.FailureCount,
		taskSchedule.LastError,
		firstNonNilTaskScheduleTime(taskSchedule.NextAttemptAt, taskSchedule.CreatedAt),
		zeroAsNil(taskSchedule.MaxRunCount),
		taskSchedule.CompletedRunCount,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) ClaimDueTaskSchedules(limit int, leaseDuration time.Duration, referenceTime time.Time, leaseOwner string) ([]task.TaskSchedule, error) {
	if limit <= 0 {
		limit = 1
	}
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	leaseSecond := int(leaseDuration.Seconds())
	if leaseSecond <= 0 {
		leaseSecond = 300
	}
	rows, errorValue := taskScheduleRepository.database.SQL.QueryContext(context.Background(), `
WITH claim AS (
  SELECT task_schedule_id FROM task_schedule
  WHERE next_run_at IS NOT NULL
    AND next_run_at <= $1
    AND next_attempt_at <= $1
    AND (expires_at IS NULL OR expires_at > $1)
    AND (leased_until IS NULL OR leased_until <= $1 OR lease_owner = $3)
  ORDER BY next_run_at ASC
  LIMIT $2
  FOR UPDATE SKIP LOCKED
)
UPDATE task_schedule
SET lease_owner = $3,
  leased_until = $1 + ($4 * interval '1 second'),
  updated_at = $1
WHERE task_schedule_id IN (SELECT task_schedule_id FROM claim)
RETURNING `+taskScheduleReturningColumns(),
		referenceTime,
		limit,
		leaseOwner,
		leaseSecond,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskSchedules(rows)
}

func (taskScheduleRepository TaskScheduleRepository) MarkTaskScheduleSucceeded(taskSchedule task.TaskSchedule) error {
	now := time.Now().UTC()
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
SET next_run_at = $2,
  last_run_at = $3,
  last_task_run_id = $4,
  lease_owner = '',
  leased_until = NULL,
  failure_count = 0,
  last_error = '',
  next_attempt_at = $1,
  completed_run_count = $6,
  updated_at = $1
WHERE task_schedule_id = $5`,
		now,
		taskSchedule.NextRunAt,
		taskSchedule.LastRunAt,
		emptyStringAsNil(taskSchedule.LastTaskRunID),
		taskSchedule.TaskScheduleID,
		taskSchedule.CompletedRunCount,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) MarkTaskScheduleFailed(taskSchedule task.TaskSchedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	nextAttemptAt := referenceTime.Add(taskScheduleRetryDelay(taskSchedule.FailureCount + 1))
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
SET lease_owner = '',
  leased_until = NULL,
  failure_count = failure_count + 1,
  last_error = $1,
  next_attempt_at = $2,
  updated_at = $3
WHERE task_schedule_id = $4`,
		errorMessage,
		nextAttemptAt,
		referenceTime,
		taskSchedule.TaskScheduleID,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) CancelTaskSchedules(request task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	cancelledAt := request.CancelledAt
	if cancelledAt.IsZero() {
		cancelledAt = time.Now().UTC()
	}
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if requesterPersonID == "" {
		return task.TaskScheduleCancelResult{}, nil
	}
	conditions := []string{
		"next_run_at IS NOT NULL",
		"(expires_at IS NULL OR expires_at > $1)",
	}
	arguments := []any{cancelledAt}
	switch request.Scope {
	case task.TaskScheduleCancelScopeCurrentConversation:
		conversationID := strings.TrimSpace(request.ConversationID)
		if conversationID == "" {
			return task.TaskScheduleCancelResult{}, nil
		}
		conditions = append(conditions, "delivery_conversation_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, conversationID)
	case task.TaskScheduleCancelScopeScheduleIDs:
		condition, values := taskScheduleIDCondition(request.TaskScheduleIDs, len(arguments)+1)
		if condition == "" {
			return task.TaskScheduleCancelResult{}, nil
		}
		conditions = append(conditions, condition)
		arguments = append(arguments, values...)
		conditions = append(conditions, taskScheduleCancelAccessCondition(request, len(arguments)+1))
		if strings.TrimSpace(request.ConversationID) != "" {
			arguments = append(arguments, requesterPersonID, strings.TrimSpace(request.ConversationID))
		} else {
			arguments = append(arguments, requesterPersonID)
		}
	default:
		conditions = append(conditions, "creator_person_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, requesterPersonID)
	}
	query := `UPDATE task_schedule
SET expires_at = $1,
  next_run_at = NULL,
  lease_owner = '',
  leased_until = NULL,
  updated_at = $1
WHERE ` + strings.Join(conditions, " AND ") + `
RETURNING ` + taskScheduleReturningColumns()
	rows, errorValue := taskScheduleRepository.database.SQL.QueryContext(context.Background(), query, arguments...)
	if errorValue != nil {
		return task.TaskScheduleCancelResult{}, errorValue
	}
	defer rows.Close()
	taskSchedules, errorValue := scanTaskSchedules(rows)
	if errorValue != nil {
		return task.TaskScheduleCancelResult{}, errorValue
	}
	return task.TaskScheduleCancelResult{TaskSchedules: taskSchedules}, nil
}

func taskScheduleCancelAccessCondition(request task.TaskScheduleCancelRequest, firstPlaceholderIndex int) string {
	if strings.TrimSpace(request.ConversationID) == "" {
		return "creator_person_id = $" + strconv.Itoa(firstPlaceholderIndex)
	}
	return "(creator_person_id = $" + strconv.Itoa(firstPlaceholderIndex) + " OR delivery_conversation_id = $" + strconv.Itoa(firstPlaceholderIndex+1) + ")"
}

func taskScheduleIDCondition(taskScheduleIDs []string, firstPlaceholderIndex int) (string, []any) {
	placeholders := []string{}
	values := []any{}
	seenValues := map[string]bool{}
	for _, taskScheduleID := range taskScheduleIDs {
		trimmedTaskScheduleID := strings.TrimSpace(taskScheduleID)
		if trimmedTaskScheduleID == "" || seenValues[trimmedTaskScheduleID] {
			continue
		}
		seenValues[trimmedTaskScheduleID] = true
		values = append(values, trimmedTaskScheduleID)
		placeholders = append(placeholders, "$"+strconv.Itoa(firstPlaceholderIndex+len(values)-1))
	}
	if len(placeholders) == 0 {
		return "", nil
	}
	return "task_schedule_id IN (" + strings.Join(placeholders, ",") + ")", values
}

func taskScheduleRetryDelay(failureCount int) time.Duration {
	if failureCount <= 0 {
		return time.Minute
	}
	delay := time.Duration(failureCount) * 5 * time.Minute
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

type taskScheduleScanner interface {
	Scan(...any) error
}

func scanTaskSchedule(scanner taskScheduleScanner) (task.TaskSchedule, error) {
	var taskSchedule task.TaskSchedule
	var executionMode string
	var kind string
	var cronExpression sql.NullString
	var intervalSecond sql.NullInt64
	var maxRunCount sql.NullInt64
	var runAt sql.NullTime
	var nextRunAt sql.NullTime
	var lastRunAt sql.NullTime
	var leasedUntil sql.NullTime
	var nextAttemptAt sql.NullTime
	var expiresAt sql.NullTime
	errorValue := scanner.Scan(
		&taskSchedule.TaskScheduleID,
		&taskSchedule.CreatorPersonID,
		&taskSchedule.Name,
		&taskSchedule.Prompt,
		&taskSchedule.AgentProfileName,
		&executionMode,
		&kind,
		&runAt,
		&intervalSecond,
		&cronExpression,
		&nextRunAt,
		&lastRunAt,
		&taskSchedule.LastTaskRunID,
		&expiresAt,
		&taskSchedule.CreatedAt,
		&taskSchedule.UpdatedAt,
		&taskSchedule.Platform,
		&taskSchedule.ConversationID,
		&taskSchedule.ReplyTargetID,
		&taskSchedule.TimeZone,
		&taskSchedule.LeaseOwner,
		&leasedUntil,
		&taskSchedule.FailureCount,
		&taskSchedule.LastError,
		&nextAttemptAt,
		&maxRunCount,
		&taskSchedule.CompletedRunCount,
	)
	taskSchedule.ExecutionMode = task.TaskScheduleExecutionMode(normalizedTaskScheduleExecutionMode(task.TaskScheduleExecutionMode(executionMode)))
	taskSchedule.Kind = task.TaskScheduleKind(kind)
	if intervalSecond.Valid {
		taskSchedule.IntervalSecond = int(intervalSecond.Int64)
	}
	if maxRunCount.Valid {
		taskSchedule.MaxRunCount = int(maxRunCount.Int64)
	}
	if cronExpression.Valid {
		taskSchedule.CronExpression = cronExpression.String
	}
	taskSchedule.RunAt = nullableTaskScheduleTime(runAt)
	taskSchedule.NextRunAt = nullableTaskScheduleTime(nextRunAt)
	taskSchedule.LastRunAt = nullableTaskScheduleTime(lastRunAt)
	taskSchedule.LeasedUntil = nullableTaskScheduleTime(leasedUntil)
	taskSchedule.NextAttemptAt = nullableTaskScheduleTime(nextAttemptAt)
	if expiresAt.Valid {
		taskSchedule.ExpiresAt = &expiresAt.Time
	}
	return taskSchedule, errorValue
}

func normalizedTaskScheduleExecutionMode(value task.TaskScheduleExecutionMode) string {
	switch value {
	case task.TaskScheduleExecutionModeMessage:
		return string(task.TaskScheduleExecutionModeMessage)
	default:
		return string(task.TaskScheduleExecutionModeAgent)
	}
}

func scanTaskSchedules(rows *sql.Rows) ([]task.TaskSchedule, error) {
	taskSchedules := []task.TaskSchedule{}
	for rows.Next() {
		taskSchedule, errorValue := scanTaskSchedule(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		taskSchedules = append(taskSchedules, taskSchedule)
	}
	return taskSchedules, rows.Err()
}

func taskScheduleReturningColumns() string {
	return `task_schedule_id, COALESCE(creator_person_id, ''), name, prompt, agent_profile_name,
  execution_mode, schedule_kind, run_at, interval_second, cron_expression, next_run_at,
  last_run_at, COALESCE(last_task_run_id, ''), expires_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone,
  lease_owner, leased_until, failure_count, last_error, next_attempt_at,
  max_run_count, completed_run_count`
}

func taskScheduleExpiresAt(taskSchedule task.TaskSchedule) any {
	if taskSchedule.ExpiresAt == nil || taskSchedule.ExpiresAt.IsZero() {
		return nil
	}
	return taskSchedule.ExpiresAt.UTC()
}

func zeroAsNil(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func firstNonNilTaskScheduleTime(pointerValue *time.Time, fallbackValue time.Time) *time.Time {
	if pointerValue != nil && !pointerValue.IsZero() {
		return pointerValue
	}
	if fallbackValue.IsZero() {
		return nil
	}
	return &fallbackValue
}

func nullableTaskScheduleTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func firstNonEmptyPostgresString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ task.TaskScheduleRepository = TaskScheduleRepository{}
