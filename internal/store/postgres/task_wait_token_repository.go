package postgres

import "blueclaw/internal/task"

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
