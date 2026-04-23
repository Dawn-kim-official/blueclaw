package postgres

import "blueclaw/internal/task"

type TaskScheduleRepository struct {
	database Database
}

func NewTaskScheduleRepository(database Database) TaskScheduleRepository {
	return TaskScheduleRepository{database: database}
}

func (taskScheduleRepository TaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	_ = taskSchedule
	return nil
}
