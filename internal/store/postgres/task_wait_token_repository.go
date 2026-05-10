package postgres

import (
	"context"
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
	_ = taskWaitToken
	return nil
}

func (taskWaitTokenRepository TaskWaitTokenRepository) ExpireTaskWaitTokensForPerson(personID string, expiredAt time.Time) ([]string, error) {
	if expiredAt.IsZero() {
		expiredAt = time.Now().UTC()
	}
	rows, errorValue := taskWaitTokenRepository.database.SQL.QueryContext(context.Background(), `
UPDATE task_wait_token
SET expires_at = $2
WHERE person_id = $1
  AND expires_at > $2
RETURNING task_run_id`, personID, expiredAt)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
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
