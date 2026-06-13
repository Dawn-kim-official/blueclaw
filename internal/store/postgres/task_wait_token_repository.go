package postgres

import (
	"context"
	"database/sql"
	"time"

	"blueclaw/internal/task"
)

type TaskWaitTokenRepository struct {
	database Database
}

func NewTaskWaitTokenRepository(database Database) TaskWaitTokenRepository {
	return TaskWaitTokenRepository{database: database}
}

func (taskWaitTokenRepository TaskWaitTokenRepository) InsertTaskWaitToken(taskWaitToken task.TaskWaitToken) error {
	if taskWaitToken.State == "" {
		taskWaitToken.State = "open"
	}
	if taskWaitToken.CreatedAt.IsZero() {
		taskWaitToken.CreatedAt = time.Now().UTC()
	}
	_, errorValue := taskWaitTokenRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_wait_token (
  wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at, token_hash
) VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11, $12, $13, $14, $15
)
ON CONFLICT (wait_id) DO UPDATE SET
  task_run_id = EXCLUDED.task_run_id,
  person_id = EXCLUDED.person_id,
  platform = EXCLUDED.platform,
  conversation_id = EXCLUDED.conversation_id,
  reply_target_id = EXCLUDED.reply_target_id,
  thread_root_id = EXCLUDED.thread_root_id,
  dispatch_id = EXCLUDED.dispatch_id,
  interaction_id = EXCLUDED.interaction_id,
  kind = EXCLUDED.kind,
  state = EXCLUDED.state,
  expires_at = EXCLUDED.expires_at,
  created_at = EXCLUDED.created_at,
  resolved_at = EXCLUDED.resolved_at,
  token_hash = EXCLUDED.token_hash`,
		taskWaitToken.WaitID,
		taskWaitToken.TaskRunID,
		taskWaitToken.PersonID,
		taskWaitToken.Platform,
		taskWaitToken.ConversationID,
		taskWaitToken.ReplyTargetID,
		taskWaitToken.ThreadRootID,
		taskWaitToken.DispatchID,
		taskWaitToken.InteractionID,
		taskWaitToken.Kind,
		taskWaitToken.State,
		taskWaitToken.ExpiresAt,
		taskWaitToken.CreatedAt,
		taskWaitToken.ResolvedAt,
		taskWaitToken.WaitID,
	)
	return errorValue
}

func (taskWaitTokenRepository TaskWaitTokenRepository) FindOpenByWaitID(waitID string) (task.TaskWaitToken, bool, error) {
	return taskWaitTokenRepository.findOpenTaskWaitToken(`
SELECT wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at
FROM task_wait_token
WHERE wait_id = $1
  AND state = 'open'
  AND expires_at > now()
ORDER BY created_at ASC, wait_id ASC
LIMIT 1`, waitID)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) FindOpenByPersonConversationAndReplyTarget(personID string, platform string, conversationID string, replyTargetID string) (task.TaskWaitToken, bool, error) {
	return taskWaitTokenRepository.findOpenTaskWaitToken(`
SELECT wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at
FROM task_wait_token
WHERE person_id = $1
  AND platform = $2
  AND conversation_id = $3
  AND reply_target_id = $4
  AND state = 'open'
  AND expires_at > now()
ORDER BY created_at ASC, wait_id ASC
LIMIT 1`, personID, platform, conversationID, replyTargetID)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) FindOpenByPersonConversationAndThreadRoot(personID string, platform string, conversationID string, threadRootID string) (task.TaskWaitToken, bool, error) {
	return taskWaitTokenRepository.findOpenTaskWaitToken(`
SELECT wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at
FROM task_wait_token
WHERE person_id = $1
  AND platform = $2
  AND conversation_id = $3
  AND thread_root_id = $4
  AND state = 'open'
  AND expires_at > now()
ORDER BY created_at ASC, wait_id ASC
LIMIT 1`, personID, platform, conversationID, threadRootID)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) FindOpenByPersonConversationAndDispatchID(personID string, platform string, conversationID string, dispatchID string) (task.TaskWaitToken, bool, error) {
	return taskWaitTokenRepository.findOpenTaskWaitToken(`
SELECT wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at
FROM task_wait_token
WHERE person_id = $1
  AND platform = $2
  AND conversation_id = $3
  AND dispatch_id = $4
  AND state = 'open'
  AND expires_at > now()
ORDER BY created_at ASC, wait_id ASC
LIMIT 1`, personID, platform, conversationID, dispatchID)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) FindOpenByPersonTaskRunAndInteraction(personID string, taskRunID string, interactionID string) (task.TaskWaitToken, bool, error) {
	return taskWaitTokenRepository.findOpenTaskWaitToken(`
SELECT wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at
FROM task_wait_token
WHERE person_id = $1
  AND task_run_id = $2
  AND interaction_id = $3
  AND state = 'open'
  AND expires_at > now()
ORDER BY created_at ASC, wait_id ASC
LIMIT 1`, personID, taskRunID, interactionID)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) FindOpenByPersonAndConversation(personID string, platform string, conversationID string) ([]task.TaskWaitToken, error) {
	rows, errorValue := taskWaitTokenRepository.database.SQL.QueryContext(context.Background(), `
SELECT wait_id, task_run_id, person_id, platform, conversation_id, reply_target_id,
  thread_root_id, dispatch_id, interaction_id, kind, state, expires_at, created_at, resolved_at
FROM task_wait_token
WHERE person_id = $1
  AND platform = $2
  AND conversation_id = $3
  AND state = 'open'
  AND expires_at > now()
ORDER BY created_at ASC, wait_id ASC`, personID, platform, conversationID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	taskWaitTokens := []task.TaskWaitToken{}
	for rows.Next() {
		taskWaitToken, errorValue := scanTaskWaitToken(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		taskWaitTokens = append(taskWaitTokens, taskWaitToken)
	}
	return taskWaitTokens, rows.Err()
}

func (taskWaitTokenRepository TaskWaitTokenRepository) ResolveTaskWait(waitID string, resolvedAt time.Time) error {
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	_, errorValue := taskWaitTokenRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_wait_token
SET state = 'resolved',
    resolved_at = $2
WHERE wait_id = $1
  AND state = 'open'`, waitID, resolvedAt)
	return errorValue
}

func (taskWaitTokenRepository TaskWaitTokenRepository) ExpireOldTaskWaits(before time.Time) ([]string, error) {
	if before.IsZero() {
		before = time.Now().UTC()
	}
	rows, errorValue := taskWaitTokenRepository.database.SQL.QueryContext(context.Background(), `
UPDATE task_wait_token
SET state = 'expired',
    resolved_at = $1
WHERE state = 'open'
  AND expires_at <= $1
RETURNING task_run_id`, before)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskWaitTokenTaskRunIDs(rows)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) ExpireTaskWaitTokensForPerson(personID string, expiredAt time.Time) ([]string, error) {
	if expiredAt.IsZero() {
		expiredAt = time.Now().UTC()
	}
	rows, errorValue := taskWaitTokenRepository.database.SQL.QueryContext(context.Background(), `
UPDATE task_wait_token
SET state = 'expired',
    resolved_at = $2,
    expires_at = LEAST(expires_at, $2)
WHERE person_id = $1
  AND state = 'open'
RETURNING task_run_id`, personID, expiredAt)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskWaitTokenTaskRunIDs(rows)
}

func (taskWaitTokenRepository TaskWaitTokenRepository) findOpenTaskWaitToken(query string, arguments ...any) (task.TaskWaitToken, bool, error) {
	taskWaitToken, errorValue := scanTaskWaitToken(taskWaitTokenRepository.database.SQL.QueryRowContext(context.Background(), query, arguments...))
	if errorValue == sql.ErrNoRows {
		return task.TaskWaitToken{}, false, nil
	}
	if errorValue != nil {
		return task.TaskWaitToken{}, false, errorValue
	}
	return taskWaitToken, true, nil
}

type taskWaitTokenScanner interface {
	Scan(...any) error
}

func scanTaskWaitToken(scanner taskWaitTokenScanner) (task.TaskWaitToken, error) {
	var taskWaitToken task.TaskWaitToken
	var resolvedAt sql.NullTime
	errorValue := scanner.Scan(
		&taskWaitToken.WaitID,
		&taskWaitToken.TaskRunID,
		&taskWaitToken.PersonID,
		&taskWaitToken.Platform,
		&taskWaitToken.ConversationID,
		&taskWaitToken.ReplyTargetID,
		&taskWaitToken.ThreadRootID,
		&taskWaitToken.DispatchID,
		&taskWaitToken.InteractionID,
		&taskWaitToken.Kind,
		&taskWaitToken.State,
		&taskWaitToken.ExpiresAt,
		&taskWaitToken.CreatedAt,
		&resolvedAt,
	)
	if errorValue != nil {
		return task.TaskWaitToken{}, errorValue
	}
	if resolvedAt.Valid {
		taskWaitToken.ResolvedAt = &resolvedAt.Time
	}
	return taskWaitToken, nil
}

type taskWaitTokenRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanTaskWaitTokenTaskRunIDs(rows taskWaitTokenRows) ([]string, error) {
	taskRunIDs := []string{}
	for rows.Next() {
		var taskRunID string
		if errorValue := rows.Scan(&taskRunID); errorValue != nil {
			return nil, errorValue
		}
		taskRunIDs = append(taskRunIDs, taskRunID)
	}
	return taskRunIDs, rows.Err()
}

var _ task.TaskWaitTokenRepository = TaskWaitTokenRepository{}
