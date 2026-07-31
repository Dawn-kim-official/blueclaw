package postgres

import "github.com/Dawn-kim-official/blueclaw/internal/task"

type TaskSessionRepository struct {
	database Database
}

func NewTaskSessionRepository(database Database) TaskSessionRepository {
	return TaskSessionRepository{database: database}
}

func (taskSessionRepository TaskSessionRepository) InsertTaskSession(taskSession task.TaskSession) error {
	_ = taskSession
	return nil
}
