package task

type TaskScheduler struct{}

func (taskScheduler TaskScheduler) ScheduleTask(taskRun TaskRun) TaskRun {
	taskRun.Status = TaskStatusPlanned
	return taskRun
}
