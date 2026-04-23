package postgres

import "blueclaw/internal/task"

type TaskRunRepository struct {
	database Database
}

func NewTaskRunRepository(database Database) TaskRunRepository {
	return TaskRunRepository{database: database}
}

func (taskRunRepository TaskRunRepository) InsertTaskRun(taskRun task.TaskRun) error {
	_ = taskRun
	return nil
}
