package postgres

import (
	"context"
	"database/sql"

	"blueclaw/internal/task"
)

type TaskEventRepository struct {
	database Database
}

func NewTaskEventRepository(database Database) TaskEventRepository {
	return TaskEventRepository{database: database}
}

func (taskEventRepository TaskEventRepository) InsertTaskEvent(taskEvent task.TaskEvent) error {
	_, errorValue := taskEventRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_event (task_event_id, task_run_id, name, body, created_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (task_event_id) DO NOTHING`,
		taskEvent.TaskEventID,
		taskEvent.TaskRunID,
		taskEvent.Name,
		taskEvent.Body,
		taskEvent.CreatedAt,
	)
	return errorValue
}

func (taskEventRepository TaskEventRepository) ListTaskEvent(taskRunID string) ([]task.TaskEvent, error) {
	rows, errorValue := taskEventRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_event_id, task_run_id, name, body, created_at
FROM task_event WHERE task_run_id = $1 ORDER BY created_at ASC`, taskRunID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskEvents(rows)
}

func (taskEventRepository TaskEventRepository) ListTaskEventByNameForTaskRuns(taskRunIDs []string, name string) ([]task.TaskEvent, error) {
	if len(taskRunIDs) == 0 || name == "" {
		return []task.TaskEvent{}, nil
	}
	rows, errorValue := taskEventRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_event_id, task_run_id, name, body, created_at
FROM task_event
WHERE task_run_id = ANY($1::text[]) AND name = $2
ORDER BY created_at ASC`, taskRunIDs, name)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskEvents(rows)
}

func scanTaskEvents(rows *sql.Rows) ([]task.TaskEvent, error) {
	taskEvents := []task.TaskEvent{}
	for rows.Next() {
		var taskEvent task.TaskEvent
		if errorValue := rows.Scan(&taskEvent.TaskEventID, &taskEvent.TaskRunID, &taskEvent.Name, &taskEvent.Body, &taskEvent.CreatedAt); errorValue != nil {
			return nil, errorValue
		}
		taskEvents = append(taskEvents, taskEvent)
	}
	return taskEvents, rows.Err()
}
