package task

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTaskRunCancelCallsRegisteredCancelFunction(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !taskRunService.IsTaskRunActuallyRunning(runningTaskRun) {
		t.Fatal("expected task run to be active after advance")
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
	if taskRunService.IsTaskRunActuallyRunning(cancelledTaskRun) {
		t.Fatal("expected cancelled task run to leave active attempt registry")
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

func TestAdvanceTaskRunCreatesCurrentAttempt(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")

	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runningTaskRun.Status != TaskStatusRunning {
		t.Fatalf("status = %s, want running", runningTaskRun.Status)
	}
	if runningTaskRun.CurrentAttemptID == "" {
		t.Fatal("expected current attempt id")
	}
	taskAttempt, isFound := taskRunService.taskAttempts[runningTaskRun.CurrentAttemptID]
	if !isFound {
		t.Fatal("expected task attempt to be recorded")
	}
	if taskAttempt.TaskRunID != taskRun.TaskRunID || taskAttempt.Status != TaskAttemptStatusRunning {
		t.Fatalf("unexpected attempt = %+v", taskAttempt)
	}
	if !taskRunService.IsTaskRunActuallyRunning(runningTaskRun) {
		t.Fatal("expected active attempt registry to own running task")
	}
}

func TestAdvanceTaskRunRejectsTerminalStatuses(t *testing.T) {
	testCases := []struct {
		name      string
		terminate func(*TaskRunService, string) (TaskRun, error)
	}{
		{
			name: "completed",
			terminate: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.CompleteTaskRun(taskRunID, "done")
			},
		},
		{
			name: "failed",
			terminate: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.FailTaskRun(taskRunID, "failed")
			},
		},
		{
			name: "cancelled",
			terminate: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.CancelTaskRunWithReason(taskRunID, "person-1", "cancelled")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
			terminalTaskRun, errorValue := testCase.terminate(taskRunService, taskRun.TaskRunID)
			if errorValue != nil {
				t.Fatal(errorValue)
			}

			advancedTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")

			var transitionError ErrIllegalTransition
			if !errors.As(errorValue, &transitionError) {
				t.Fatalf("error = %v, want ErrIllegalTransition", errorValue)
			}
			if advancedTaskRun.TaskRunID != "" {
				t.Fatalf("advanced task run = %+v, want zero value", advancedTaskRun)
			}
			storedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
			if !isFound || storedTaskRun.Status != terminalTaskRun.Status {
				t.Fatalf("stored status = %s, found = %v, want %s", storedTaskRun.Status, isFound, terminalTaskRun.Status)
			}
			if taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.running", "assistant") {
				t.Fatal("did not expect task.running event for rejected transition")
			}
		})
	}
}

func TestAdvanceTaskRunAllowsPlannedAndWaitingTaskRuns(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	plannedTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "planned")

	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(plannedTaskRun.TaskRunID, "assistant")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runningTaskRun.Status != TaskStatusRunning {
		t.Fatalf("status = %s, want running", runningTaskRun.Status)
	}
	waitingTaskRun, errorValue := taskRunService.PauseTaskRun(plannedTaskRun.TaskRunID, TaskStatusWaitingUserInput, "ask input")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	resumedTaskRun, errorValue := taskRunService.AdvanceTaskRun(waitingTaskRun.TaskRunID, "assistant")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if resumedTaskRun.Status != TaskStatusRunning {
		t.Fatalf("status = %s, want running", resumedTaskRun.Status)
	}
	if resumedTaskRun.CurrentAttemptID == runningTaskRun.CurrentAttemptID {
		t.Fatal("expected resumed task run to start a new attempt")
	}
}

func TestPauseTaskRunTransitionKeepsTaskAttemptAndEventConsistent(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	waitingTaskRun, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, TaskStatusWaitingUserInput, "ask input")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if waitingTaskRun.Status != TaskStatusWaitingUserInput {
		t.Fatalf("status = %s, want waiting_user_input", waitingTaskRun.Status)
	}
	storedTaskRun := taskRunService.taskRuns[taskRun.TaskRunID]
	if storedTaskRun.Status != waitingTaskRun.Status || storedTaskRun.UpdatedAt.IsZero() {
		t.Fatalf("stored task run = %+v, want waiting task run", storedTaskRun)
	}
	taskAttempt := taskRunService.taskAttempts[runningTaskRun.CurrentAttemptID]
	if taskAttempt.Status != TaskAttemptStatusInterrupted || taskAttempt.FinishedAt == nil {
		t.Fatalf("attempt = %+v, want interrupted finished attempt", taskAttempt)
	}
	if taskRunService.IsTaskRunActuallyRunning(waitingTaskRun) {
		t.Fatal("expected waiting task run to leave active attempt registry")
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.paused", "ask input") {
		t.Fatal("expected task.paused event")
	}
}

func TestTaskRunTerminalTransitionsCloseCurrentAttempt(t *testing.T) {
	testCases := []struct {
		name                  string
		transition            func(*TaskRunService, string) (TaskRun, error)
		expectedTaskStatus    TaskStatus
		expectedAttemptStatus TaskAttemptStatus
	}{
		{
			name: "complete",
			transition: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.CompleteTaskRun(taskRunID, "done")
			},
			expectedTaskStatus:    TaskStatusCompleted,
			expectedAttemptStatus: TaskAttemptStatusCompleted,
		},
		{
			name: "fail",
			transition: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.FailTaskRun(taskRunID, "failed")
			},
			expectedTaskStatus:    TaskStatusFailed,
			expectedAttemptStatus: TaskAttemptStatusFailed,
		},
		{
			name: "cancel",
			transition: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.CancelTaskRunWithReason(taskRunID, "person-1", "cancelled")
			},
			expectedTaskStatus:    TaskStatusCancelled,
			expectedAttemptStatus: TaskAttemptStatusCancelled,
		},
		{
			name: "pause",
			transition: func(taskRunService *TaskRunService, taskRunID string) (TaskRun, error) {
				return taskRunService.PauseTaskRun(taskRunID, TaskStatusWaitingUserInput, "ask input")
			},
			expectedTaskStatus:    TaskStatusWaitingUserInput,
			expectedAttemptStatus: TaskAttemptStatusInterrupted,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
			runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
			if errorValue != nil {
				t.Fatal(errorValue)
			}

			closedTaskRun, errorValue := testCase.transition(taskRunService, taskRun.TaskRunID)

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if closedTaskRun.Status != testCase.expectedTaskStatus {
				t.Fatalf("status = %s, want %s", closedTaskRun.Status, testCase.expectedTaskStatus)
			}
			taskAttempt := taskRunService.taskAttempts[runningTaskRun.CurrentAttemptID]
			if taskAttempt.Status != testCase.expectedAttemptStatus {
				t.Fatalf("attempt status = %s, want %s", taskAttempt.Status, testCase.expectedAttemptStatus)
			}
			if taskAttempt.FinishedAt == nil {
				t.Fatal("expected attempt finished at")
			}
			if taskRunService.IsTaskRunActuallyRunning(closedTaskRun) {
				t.Fatal("expected closed task run to leave active attempt registry")
			}
		})
	}
}

func TestInterruptOrphanedRuntimeTaskRunsMarksRuntimeOwnedTasksInterrupted(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskRunService := NewTaskRunService(taskEventService)
	plannedTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "planned")
	runningTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "running")
	waitingTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "waiting")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(runningTaskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := taskRunService.PauseTaskRun(waitingTaskRun.TaskRunID, TaskStatusWaitingUserInput, "ask input"); errorValue != nil {
		t.Fatal(errorValue)
	}
	taskEventService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.site.app.build.requested", `{"observationID":"observation-1","toolName":"site.app.build"}`)
	delete(taskRunService.activeAttempts, runningTaskRun.CurrentAttemptID)

	interruptedTaskRuns := taskRunService.InterruptOrphanedRuntimeTaskRuns("runtime restarted")

	if len(interruptedTaskRuns) != 2 {
		t.Fatalf("interrupted count = %d, want 2", len(interruptedTaskRuns))
	}
	for _, taskRunID := range []string{plannedTaskRun.TaskRunID, runningTaskRun.TaskRunID} {
		taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
		if !isFound || taskRun.Status != TaskStatusInterrupted {
			t.Fatalf("task %s status = %+v, found=%v", taskRunID, taskRun.Status, isFound)
		}
		if !taskEventsContain(taskRunService.ListTaskEvent(taskRunID), "task.interrupted", "runtime restarted") {
			t.Fatalf("expected task.interrupted event for %s", taskRunID)
		}
	}
	taskAttempt := taskRunService.taskAttempts[runningTaskRun.CurrentAttemptID]
	if taskAttempt.Status != TaskAttemptStatusInterrupted {
		t.Fatalf("attempt status = %s, want interrupted", taskAttempt.Status)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(runningTaskRun.TaskRunID), "tool.site.app.build.cancelled", "cancelled_by_attempt_end") {
		t.Fatal("expected open tool request to be cancelled")
	}
	taskRun, isFound := taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if !isFound || taskRun.Status != TaskStatusWaitingUserInput {
		t.Fatalf("waiting task status = %+v, found=%v", taskRun.Status, isFound)
	}
}

func TestClaimInterruptedTaskRunAutoResumeAllowsOnlyOneAttempt(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := interruptedTaskRunForTest(t, taskRunService, time.Now())

	if !taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "boot resume") {
		t.Fatal("expected first auto-resume claim")
	}
	if taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "boot resume") {
		t.Fatal("did not expect second auto-resume claim")
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.auto_resume_attempted", `"attemptCount":1`) {
		t.Fatal("expected persisted auto-resume attempt event")
	}
}

func TestSelectInterruptedTaskRunsForAutoResumeCapsNewestFirst(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	baseTime := time.Now()
	for index := 0; index < 7; index++ {
		interruptedTaskRunForTest(t, taskRunService, baseTime.Add(time.Duration(index)*time.Minute))
	}

	selection := taskRunService.SelectInterruptedTaskRunsForAutoResume(baseTime.Add(time.Hour), 5)

	if len(selection.SelectedTaskRuns) != 5 {
		t.Fatalf("selected count = %d, want 5", len(selection.SelectedTaskRuns))
	}
	if len(selection.SkippedTaskRuns) != 2 {
		t.Fatalf("skipped count = %d, want 2", len(selection.SkippedTaskRuns))
	}
	for index := 1; index < len(selection.SelectedTaskRuns); index++ {
		if selection.SelectedTaskRuns[index].UpdatedAt.After(selection.SelectedTaskRuns[index-1].UpdatedAt) {
			t.Fatal("expected newest interrupted task runs first")
		}
	}
}

func TestSelectInterruptedTaskRunsForAutoResumeExcludesOldTasks(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	now := time.Now()
	interruptedTaskRunForTest(t, taskRunService, now.Add(-25*time.Hour))
	recentTaskRun := interruptedTaskRunForTest(t, taskRunService, now.Add(-23*time.Hour))

	selection := taskRunService.SelectInterruptedTaskRunsForAutoResume(now, 5)

	if len(selection.SelectedTaskRuns) != 1 || selection.SelectedTaskRuns[0].TaskRunID != recentTaskRun.TaskRunID {
		t.Fatalf("selected task runs = %+v, want recent task", selection.SelectedTaskRuns)
	}
}

func TestSelectInterruptedTaskRunsForAutoResumeExcludesWaitingStatuses(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	waitingTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "waiting")
	waitingTaskRun.Status = TaskStatusWaitingApproval
	waitingTaskRun.UpdatedAt = time.Now()
	taskRunService.taskRuns[waitingTaskRun.TaskRunID] = waitingTaskRun
	taskRunService.AppendTaskEvent(waitingTaskRun.TaskRunID, "task.interrupted", "runtime restarted")

	selection := taskRunService.SelectInterruptedTaskRunsForAutoResume(time.Now(), 5)

	if len(selection.SelectedTaskRuns) != 0 {
		t.Fatalf("selected count = %d, want 0", len(selection.SelectedTaskRuns))
	}
}

func interruptedTaskRunForTest(t *testing.T, taskRunService *TaskRunService, updatedAt time.Time) TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	delete(taskRunService.activeAttempts, runningTaskRun.CurrentAttemptID)
	interruptedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, "runtime restarted")
	if !isInterrupted {
		t.Fatal("expected interrupted task run")
	}
	interruptedTaskRun.UpdatedAt = updatedAt
	taskRunService.taskRuns[interruptedTaskRun.TaskRunID] = interruptedTaskRun
	return interruptedTaskRun
}

func taskEventsContain(taskEvents []TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && (bodyFragment == "" || strings.Contains(taskEvent.Body, bodyFragment)) {
			return true
		}
	}
	return false
}
