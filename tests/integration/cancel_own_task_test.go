package integration

import (
	"testing"

	"blueclaw/internal/task"
)

func TestCancelOwnTask(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "cancel me")

	cancelledTaskRun, errorValue := taskRunService.CancelTaskRun(taskRun.TaskRunID, "person-1")
	if errorValue != nil {
		t.Fatalf("expected task cancellation to succeed: %v", errorValue)
	}
	if cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected task to be cancelled, got %s", cancelledTaskRun.Status)
	}
}
