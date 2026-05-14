package task

import (
	"testing"
)

func TestTaskRunCancelCallsRegisteredCancelFunction(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	cancelCalled := false
	taskRunService.RegisterTaskRunCancel(taskRun.TaskRunID, func() {
		cancelCalled = true
	})

	cancelledTaskRun, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "user stop")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if cancelledTaskRun.Status != TaskStatusCancelled {
		t.Fatalf("status = %s, want cancelled", cancelledTaskRun.Status)
	}
	if !cancelCalled {
		t.Fatal("registered cancel function was not called")
	}
}

func TestCancelledTaskRunCannotComplete(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "user stop"); errorValue != nil {
		t.Fatal(errorValue)
	}

	completedTaskRun, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "late reply")

	if errorValue == nil {
		t.Fatal("expected complete to fail for cancelled task run")
	}
	if completedTaskRun.Status != TaskStatusCancelled {
		t.Fatalf("status = %s, want cancelled", completedTaskRun.Status)
	}
}
