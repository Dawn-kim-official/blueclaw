package task

import "time"

type TaskScheduleRepository interface {
	UpsertTaskSchedule(TaskSchedule) error
	ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]TaskSchedule, error)
	MarkTaskScheduleSucceeded(TaskSchedule) error
	MarkTaskScheduleFailed(TaskSchedule, string, time.Time) error
}
