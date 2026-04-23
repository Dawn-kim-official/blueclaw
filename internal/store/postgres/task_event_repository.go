package postgres

import "blueclaw/internal/task"

type TaskEventRepository struct {
	database Database
}

func NewTaskEventRepository(database Database) TaskEventRepository {
	return TaskEventRepository{database: database}
}

func (taskEventRepository TaskEventRepository) InsertTaskEvent(taskEvent task.TaskEvent) error {
	_ = taskEvent
	return nil
}
