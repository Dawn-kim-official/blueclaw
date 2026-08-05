package postgres

import (
	"context"
	"database/sql"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type TaskArtifactRepository struct {
	database Database
}

func NewTaskArtifactRepository(database Database) TaskArtifactRepository {
	return TaskArtifactRepository{database: database}
}

func (taskArtifactRepository TaskArtifactRepository) InsertTaskArtifact(taskArtifact task.TaskArtifact) error {
	_, errorValue := taskArtifactRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_artifact (task_artifact_id, task_run_id, name, body)
VALUES ($1,$2,$3,$4)
ON CONFLICT (task_artifact_id) DO UPDATE SET
  name = EXCLUDED.name,
  body = EXCLUDED.body`,
		taskArtifact.TaskArtifactID,
		taskArtifact.TaskRunID,
		taskArtifact.Name,
		taskArtifact.Body,
	)
	return errorValue
}

func (taskArtifactRepository TaskArtifactRepository) ListTaskArtifact(taskRunID string) ([]task.TaskArtifact, error) {
	rows, errorValue := taskArtifactRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_artifact_id, task_run_id, name, body
FROM task_artifact WHERE task_run_id = $1 ORDER BY task_artifact_id ASC`, taskRunID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskArtifacts(rows)
}

func scanTaskArtifacts(rows *sql.Rows) ([]task.TaskArtifact, error) {
	taskArtifacts := []task.TaskArtifact{}
	for rows.Next() {
		var taskArtifact task.TaskArtifact
		if errorValue := rows.Scan(&taskArtifact.TaskArtifactID, &taskArtifact.TaskRunID, &taskArtifact.Name, &taskArtifact.Body); errorValue != nil {
			return nil, errorValue
		}
		taskArtifacts = append(taskArtifacts, taskArtifact)
	}
	return taskArtifacts, rows.Err()
}
