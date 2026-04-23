package postgres

import "blueclaw/internal/task"

type TaskStepRepository struct {
	database Database
}

func NewTaskStepRepository(database Database) TaskStepRepository {
	return TaskStepRepository{database: database}
}

func (taskStepRepository TaskStepRepository) InsertTaskStep(taskStep task.TaskStep) error {
	_ = taskStep
	return nil
}
