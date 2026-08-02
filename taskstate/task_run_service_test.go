package taskstate

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

	var transitionError ErrIllegalTransition
	if !errors.As(errorValue, &transitionError) {
		t.Fatalf("error = %v, want ErrIllegalTransition", errorValue)
	}
	if completedTaskRun.TaskRunID != "" {
		t.Fatalf("completed task run = %+v, want zero value", completedTaskRun)
	}
	storedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || storedTaskRun.Status != TaskStatusCancelled {
		t.Fatalf("stored status = %s, found = %v, want cancelled", storedTaskRun.Status, isFound)
	}
}

func TestDeleteTerminalTaskRunRemovesCompletedTaskRun(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "finished task")
	if _, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}

	deletedTaskRun, errorValue := taskRunService.DeleteTerminalTaskRun(taskRun.TaskRunID, "person-1")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if deletedTaskRun.TaskRunID != taskRun.TaskRunID {
		t.Fatalf("deleted task run = %+v, want %s", deletedTaskRun, taskRun.TaskRunID)
	}
	if _, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID); isFound {
		t.Fatal("expected task run to be deleted")
	}
}

func TestDeleteTerminalTaskRunRejectsRunningTaskRun(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "running task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}

	deletedTaskRun, errorValue := taskRunService.DeleteTerminalTaskRun(taskRun.TaskRunID, "person-1")

	if !errors.Is(errorValue, ErrTaskRunNotDeletable) {
		t.Fatalf("error = %v, want ErrTaskRunNotDeletable", errorValue)
	}
	if deletedTaskRun.TaskRunID != "" {
		t.Fatalf("deleted task run = %+v, want zero value", deletedTaskRun)
	}
	if _, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID); !isFound {
		t.Fatal("running task run should remain")
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

func TestAdvanceTaskRunAllowsBlockedResume(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	blockedTaskRun := pausedTaskRunForTest(t, taskRunService, TaskStatusBlocked, "max_iterations")

	resumedTaskRun, errorValue := taskRunService.AdvanceTaskRun(blockedTaskRun.TaskRunID, "assistant")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if resumedTaskRun.Status != TaskStatusRunning {
		t.Fatalf("status = %s, want running", resumedTaskRun.Status)
	}
	if resumedTaskRun.CurrentAttemptID == blockedTaskRun.CurrentAttemptID {
		t.Fatal("expected resume to create a new attempt")
	}
	if resumedTaskRun.FailureReason != "" {
		t.Fatalf("expected resume to clear the stale block reason so a running task shows no failure, got %q", resumedTaskRun.FailureReason)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(blockedTaskRun.TaskRunID), "task.running", "assistant") {
		t.Fatal("expected running transition event")
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
				if _, errorValue := taskRunService.AdvanceTaskRun(taskRunID, "assistant"); errorValue != nil {
					return TaskRun{}, errorValue
				}
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
			runningEventCount := countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.running")

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
			if countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.running") != runningEventCount {
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

func TestCompleteTaskRunTransitionAllowsActiveTaskRuns(t *testing.T) {
	testCases := []struct {
		name           string
		prepareTaskRun func(*testing.T, *TaskRunService) TaskRun
		hasTaskAttempt bool
	}{
		{
			name: "planned",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return taskRunService.CreateTaskRun("person-1", "direct-1", "planned")
			},
		},
		{
			name: "running",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return runningTaskRunForTest(t, taskRunService, "running")
			},
			hasTaskAttempt: true,
		},
		{
			name: "waiting user input",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusWaitingUserInput, "waiting user input")
			},
			hasTaskAttempt: true,
		},
		{
			name: "waiting approval",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusWaitingApproval, "waiting approval")
			},
			hasTaskAttempt: true,
		},
		{
			name: "blocked",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusBlocked, "blocked")
			},
			hasTaskAttempt: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := testCase.prepareTaskRun(t, taskRunService)

			completedTaskRun, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done")

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if completedTaskRun.Status != TaskStatusCompleted || completedTaskRun.Result != "done" || completedTaskRun.FailureReason != "" {
				t.Fatalf("completed task run = %+v, want completed result without failure reason", completedTaskRun)
			}
			storedTaskRun := taskRunService.taskRuns[taskRun.TaskRunID]
			if storedTaskRun.Status != TaskStatusCompleted || storedTaskRun.Result != "done" {
				t.Fatalf("stored task run = %+v, want completed result", storedTaskRun)
			}
			if testCase.hasTaskAttempt {
				taskAttempt := taskRunService.taskAttempts[completedTaskRun.CurrentAttemptID]
				if taskAttempt.Status != TaskAttemptStatusCompleted || taskAttempt.FinishedAt == nil || taskAttempt.FailureReason != "" {
					t.Fatalf("attempt = %+v, want completed attempt without failure reason", taskAttempt)
				}
			}
			if !taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.completed", "done") {
				t.Fatal("expected task.completed event")
			}
		})
	}
}

func TestCompleteTaskRunTransitionRejectsIllegalSourceStates(t *testing.T) {
	testCases := []struct {
		name           string
		prepareTaskRun func(*testing.T, *TaskRunService) TaskRun
		expectedStatus TaskStatus
	}{
		{
			name: "interrupted",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return inactiveInterruptedTaskRunForTest(t, taskRunService)
			},
			expectedStatus: TaskStatusInterrupted,
		},
		{
			name: "completed",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "completed")
				completedTaskRun, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return completedTaskRun
			},
			expectedStatus: TaskStatusCompleted,
		},
		{
			name: "failed",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "failed")
				failedTaskRun, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return failedTaskRun
			},
			expectedStatus: TaskStatusFailed,
		},
		{
			name: "cancelled",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "cancelled")
				cancelledTaskRun, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "cancelled")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return cancelledTaskRun
			},
			expectedStatus: TaskStatusCancelled,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := testCase.prepareTaskRun(t, taskRunService)
			completedEventCount := countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.completed")

			completedTaskRun, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "late result")

			assertIllegalTaskRunTransition(t, errorValue, testCase.expectedStatus, TaskStatusCompleted)
			if completedTaskRun.TaskRunID != "" {
				t.Fatalf("completed task run = %+v, want zero value", completedTaskRun)
			}
			storedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
			if !isFound || storedTaskRun.Status != testCase.expectedStatus {
				t.Fatalf("stored status = %s, found = %v, want %s", storedTaskRun.Status, isFound, testCase.expectedStatus)
			}
			if countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.completed") != completedEventCount {
				t.Fatal("did not expect task.completed event for rejected transition")
			}
		})
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

func TestCancelTaskRunTransitionAllowsActiveTaskRuns(t *testing.T) {
	testCases := []struct {
		name           string
		prepareTaskRun func(*testing.T, *TaskRunService) TaskRun
	}{
		{
			name: "planned",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return taskRunService.CreateTaskRun("person-1", "direct-1", "planned")
			},
		},
		{
			name: "running",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return runningTaskRunForTest(t, taskRunService, "running")
			},
		},
		{
			name: "waiting user input",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusWaitingUserInput, "waiting user input")
			},
		},
		{
			name: "waiting approval",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusWaitingApproval, "waiting approval")
			},
		},
		{
			name: "blocked",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusBlocked, "blocked")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := testCase.prepareTaskRun(t, taskRunService)

			cancelledTaskRun, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "user stop")

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if cancelledTaskRun.Status != TaskStatusCancelled || cancelledTaskRun.FailureReason != "user stop" {
				t.Fatalf("cancelled task run = %+v, want cancelled with reason", cancelledTaskRun)
			}
			storedTaskRun := taskRunService.taskRuns[taskRun.TaskRunID]
			if storedTaskRun.Status != TaskStatusCancelled || storedTaskRun.FailureReason != "user stop" {
				t.Fatalf("stored task run = %+v, want cancelled with reason", storedTaskRun)
			}
			if cancelledTaskRun.CurrentAttemptID != "" {
				taskAttempt := taskRunService.taskAttempts[cancelledTaskRun.CurrentAttemptID]
				if taskAttempt.Status != TaskAttemptStatusCancelled || taskAttempt.FinishedAt == nil || taskAttempt.FailureReason != "user stop" {
					t.Fatalf("attempt = %+v, want cancelled attempt with reason", taskAttempt)
				}
			}
			if !taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.cancelled", "user stop") {
				t.Fatal("expected task.cancelled event")
			}
		})
	}
}

func TestCancelTaskRunTransitionRejectsIllegalSourceStates(t *testing.T) {
	testCases := []struct {
		name           string
		prepareTaskRun func(*testing.T, *TaskRunService) TaskRun
		expectedStatus TaskStatus
	}{
		{
			name: "interrupted",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return inactiveInterruptedTaskRunForTest(t, taskRunService)
			},
			expectedStatus: TaskStatusInterrupted,
		},
		{
			name: "completed",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "completed")
				completedTaskRun, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return completedTaskRun
			},
			expectedStatus: TaskStatusCompleted,
		},
		{
			name: "failed",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "failed")
				failedTaskRun, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return failedTaskRun
			},
			expectedStatus: TaskStatusFailed,
		},
		{
			name: "cancelled",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "cancelled")
				cancelledTaskRun, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "cancelled")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return cancelledTaskRun
			},
			expectedStatus: TaskStatusCancelled,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := testCase.prepareTaskRun(t, taskRunService)
			cancelledEventCount := countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.cancelled")

			cancelledTaskRun, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "late stop")

			assertIllegalTaskRunTransition(t, errorValue, testCase.expectedStatus, TaskStatusCancelled)
			if cancelledTaskRun.TaskRunID != "" {
				t.Fatalf("cancelled task run = %+v, want zero value", cancelledTaskRun)
			}
			storedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
			if !isFound || storedTaskRun.Status != testCase.expectedStatus {
				t.Fatalf("stored status = %s, found = %v, want %s", storedTaskRun.Status, isFound, testCase.expectedStatus)
			}
			if countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.cancelled") != cancelledEventCount {
				t.Fatal("did not expect task.cancelled event for rejected transition")
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
	taskEventService.AppendTaskEvent(runningTaskRun.TaskRunID, "tool.site.build.requested", `{"observationID":"observation-1","toolName":"site.build"}`)
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
	if !taskEventsContain(taskRunService.ListTaskEvent(runningTaskRun.TaskRunID), "tool.site.build.cancelled", "cancelled_by_attempt_end") {
		t.Fatal("expected open tool request to be cancelled")
	}
	taskRun, isFound := taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if !isFound || taskRun.Status != TaskStatusWaitingUserInput {
		t.Fatalf("waiting task status = %+v, found=%v", taskRun.Status, isFound)
	}
}

func TestInterruptInactiveTaskRunTransitionAllowsInactiveRuntimeTaskRuns(t *testing.T) {
	testCases := []struct {
		name             string
		prepareTaskRun   func(*testing.T, *TaskRunService) TaskRun
		hasTaskAttempt   bool
		expectedTaskBody string
	}{
		{
			name: "planned",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return taskRunService.CreateTaskRun("person-1", "direct-1", "planned")
			},
			expectedTaskBody: "runtime restarted",
		},
		{
			name: "running inactive",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "running")
				delete(taskRunService.activeAttempts, taskRun.CurrentAttemptID)
				return taskRun
			},
			hasTaskAttempt:   true,
			expectedTaskBody: "runtime restarted",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := testCase.prepareTaskRun(t, taskRunService)

			interruptedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, "runtime restarted")

			if !isInterrupted {
				t.Fatal("expected task run to be interrupted")
			}
			if interruptedTaskRun.Status != TaskStatusInterrupted || interruptedTaskRun.FailureReason != "runtime restarted" {
				t.Fatalf("interrupted task run = %+v, want interrupted with reason", interruptedTaskRun)
			}
			storedTaskRun := taskRunService.taskRuns[taskRun.TaskRunID]
			if storedTaskRun.Status != TaskStatusInterrupted || storedTaskRun.FailureReason != "runtime restarted" {
				t.Fatalf("stored task run = %+v, want interrupted with reason", storedTaskRun)
			}
			if testCase.hasTaskAttempt {
				taskAttempt := taskRunService.taskAttempts[interruptedTaskRun.CurrentAttemptID]
				if taskAttempt.Status != TaskAttemptStatusInterrupted || taskAttempt.FinishedAt == nil || taskAttempt.FailureReason != "runtime restarted" {
					t.Fatalf("attempt = %+v, want interrupted attempt with reason", taskAttempt)
				}
			}
			if !taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.interrupted", testCase.expectedTaskBody) {
				t.Fatal("expected task.interrupted event")
			}
		})
	}
}

func TestInterruptInactiveTaskRunTransitionRejectsIllegalSourceStates(t *testing.T) {
	testCases := []struct {
		name           string
		prepareTaskRun func(*testing.T, *TaskRunService) TaskRun
		expectedStatus TaskStatus
	}{
		{
			name: "running active",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return runningTaskRunForTest(t, taskRunService, "running")
			},
			expectedStatus: TaskStatusRunning,
		},
		{
			name: "waiting user input",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusWaitingUserInput, "waiting user input")
			},
			expectedStatus: TaskStatusWaitingUserInput,
		},
		{
			name: "blocked",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return pausedTaskRunForTest(t, taskRunService, TaskStatusBlocked, "blocked")
			},
			expectedStatus: TaskStatusBlocked,
		},
		{
			name: "interrupted",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				return inactiveInterruptedTaskRunForTest(t, taskRunService)
			},
			expectedStatus: TaskStatusInterrupted,
		},
		{
			name: "completed",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "completed")
				completedTaskRun, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return completedTaskRun
			},
			expectedStatus: TaskStatusCompleted,
		},
		{
			name: "failed",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "failed")
				failedTaskRun, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return failedTaskRun
			},
			expectedStatus: TaskStatusFailed,
		},
		{
			name: "cancelled",
			prepareTaskRun: func(t *testing.T, taskRunService *TaskRunService) TaskRun {
				taskRun := runningTaskRunForTest(t, taskRunService, "cancelled")
				cancelledTaskRun, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "cancelled")
				if errorValue != nil {
					t.Fatal(errorValue)
				}
				return cancelledTaskRun
			},
			expectedStatus: TaskStatusCancelled,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := NewTaskRunService(NewTaskEventService())
			taskRun := testCase.prepareTaskRun(t, taskRunService)
			interruptedEventCount := countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.interrupted")

			interruptedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, "runtime restarted")

			if isInterrupted {
				t.Fatalf("interrupted task run = %+v, want no interruption", interruptedTaskRun)
			}
			storedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
			if !isFound || storedTaskRun.Status != testCase.expectedStatus {
				t.Fatalf("stored status = %s, found = %v, want %s", storedTaskRun.Status, isFound, testCase.expectedStatus)
			}
			if countTaskEvents(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.interrupted") != interruptedEventCount {
				t.Fatal("did not expect task.interrupted event for rejected transition")
			}
		})
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

func runningTaskRunForTest(t *testing.T, taskRunService *TaskRunService, prompt string) TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", prompt)
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return runningTaskRun
}

func pausedTaskRunForTest(t *testing.T, taskRunService *TaskRunService, status TaskStatus, prompt string) TaskRun {
	t.Helper()
	taskRun := runningTaskRunForTest(t, taskRunService, prompt)
	pausedTaskRun, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, status, string(status))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return pausedTaskRun
}

func inactiveInterruptedTaskRunForTest(t *testing.T, taskRunService *TaskRunService) TaskRun {
	t.Helper()
	taskRun := runningTaskRunForTest(t, taskRunService, "interrupted")
	delete(taskRunService.activeAttempts, taskRun.CurrentAttemptID)
	interruptedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, "runtime restarted")
	if !isInterrupted {
		t.Fatal("expected interrupted task run")
	}
	return interruptedTaskRun
}

func assertIllegalTaskRunTransition(t *testing.T, errorValue error, currentStatus TaskStatus, toState TaskStatus) {
	t.Helper()
	var transitionError ErrIllegalTransition
	if !errors.As(errorValue, &transitionError) {
		t.Fatalf("error = %v, want ErrIllegalTransition", errorValue)
	}
	if transitionError.CurrentStatus != currentStatus || transitionError.ToState != toState {
		t.Fatalf("transition error = %+v, want current %s to %s", transitionError, currentStatus, toState)
	}
}

func countTaskEvents(taskEvents []TaskEvent, name string) int {
	count := 0
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			count++
		}
	}
	return count
}

func taskEventsContain(taskEvents []TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && (bodyFragment == "" || strings.Contains(taskEvent.Body, bodyFragment)) {
			return true
		}
	}
	return false
}

func TestClaimInterruptedTaskRunAutoResumeAllowsRepeatedPlannedShutdownResume(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskRunService := NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	interruptedTaskRuns := taskRunService.InterruptRuntimeTaskRunsForPlannedShutdown()
	if len(interruptedTaskRuns) != 1 {
		t.Fatalf("interrupted count = %d, want 1", len(interruptedTaskRuns))
	}
	if interruptedTaskRuns[0].FailureReason != TaskInterruptReasonPlannedShutdown {
		t.Fatalf("failure reason = %q, want planned_shutdown", interruptedTaskRuns[0].FailureReason)
	}
	if !taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
		t.Fatal("expected first auto-resume claim to succeed")
	}
	if !taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
		t.Fatal("expected planned-shutdown task to remain claimable after a prior attempt")
	}
}

func TestClaimInterruptedTaskRunAutoResumeKeepsOneShotPolicyForCrashInterruption(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskRunService := NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	taskRun, advanceError := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if advanceError != nil {
		t.Fatal(advanceError)
	}
	delete(taskRunService.activeAttempts, taskRun.CurrentAttemptID)
	if len(taskRunService.InterruptOrphanedRuntimeTaskRuns(TaskInterruptReasonRuntimeRestart)) != 1 {
		t.Fatal("expected crash interruption")
	}
	if !taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
		t.Fatal("expected first auto-resume claim to succeed")
	}
	if taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
		t.Fatal("expected crash-interrupted task to stay one-shot")
	}
}

func TestInterruptRuntimeTaskRunsForPlannedShutdownIncludesActivelyRunningTasks(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskRunService := NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "running task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !taskRunService.IsTaskRunActuallyRunning(mustFindTaskRun(t, taskRunService, taskRun.TaskRunID)) {
		t.Fatal("expected an actively running task before shutdown")
	}
	if len(taskRunService.InterruptRuntimeTaskRunsForPlannedShutdown()) != 1 {
		t.Fatal("expected actively running task to be interrupted for planned shutdown")
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(taskRun.TaskRunID), "task.interrupted", TaskInterruptReasonPlannedShutdown) {
		t.Fatal("expected planned shutdown interruption event")
	}
}

func mustFindTaskRun(t *testing.T, taskRunService *TaskRunService, taskRunID string) TaskRun {
	t.Helper()
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		t.Fatalf("task run %s not found", taskRunID)
	}
	return taskRun
}

func TestRecordTaskRunResultPersistsResultWithoutTransition(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := runningTaskRunForTest(t, taskRunService, "failing task")
	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "tooling failed"); errorValue != nil {
		t.Fatal(errorValue)
	}

	recordedTaskRun, errorValue := taskRunService.RecordTaskRunResult(taskRun.TaskRunID, "failure notice sent to the user")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if recordedTaskRun.Status != TaskStatusFailed || recordedTaskRun.Result != "failure notice sent to the user" {
		t.Fatalf("recorded task run = %+v, want failed status with persisted result", recordedTaskRun)
	}
	storedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || storedTaskRun.Result != "failure notice sent to the user" {
		t.Fatalf("stored task run = %+v, want persisted failure result", storedTaskRun)
	}
	if storedTaskRun.FailureReason != "tooling failed" {
		t.Fatalf("stored failure reason = %q, want original failure reason preserved", storedTaskRun.FailureReason)
	}
}

func TestRecordTaskRunResultRejectsUnknownTaskRun(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	if _, errorValue := taskRunService.RecordTaskRunResult("missing-task", "result"); errorValue == nil {
		t.Fatal("recording a result for an unknown task run must fail")
	}
}

func staleTestTaskRun(t *testing.T, taskRunService *TaskRunService, status TaskStatus, age time.Duration) TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "stale candidate")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	pausedTaskRun, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, status, "test pause")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	pausedTaskRun.UpdatedAt = time.Now().Add(-age)
	taskRunService.taskRuns[pausedTaskRun.TaskRunID] = pausedTaskRun
	return pausedTaskRun
}

func TestStaleUnattendedTaskRunReasonByStatusAndAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		status TaskStatus
		age    time.Duration
		reason string
	}{
		{TaskStatusBlocked, 25 * time.Hour, "blocked_expired"},
		{TaskStatusBlocked, time.Hour, ""},
		{TaskStatusWaitingApproval, 73 * time.Hour, "waiting_expired"},
		{TaskStatusWaitingApproval, 25 * time.Hour, ""},
		{TaskStatusWaitingUserInput, 73 * time.Hour, "waiting_expired"},
		{TaskStatusRunning, 100 * time.Hour, ""},
		{TaskStatusFailed, 100 * time.Hour, ""},
	}
	for _, testCase := range cases {
		taskRun := TaskRun{Status: testCase.status, UpdatedAt: now.Add(-testCase.age)}
		if reason := StaleUnattendedTaskRunReason(taskRun, now); reason != testCase.reason {
			t.Fatalf("status %s age %s: reason = %q, want %q", testCase.status, testCase.age, reason, testCase.reason)
		}
	}
}

func TestSelectStaleUnattendedTaskRunsOldestFirst(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	fresh := staleTestTaskRun(t, taskRunService, TaskStatusBlocked, time.Hour)
	older := staleTestTaskRun(t, taskRunService, TaskStatusBlocked, 48*time.Hour)
	oldest := staleTestTaskRun(t, taskRunService, TaskStatusWaitingApproval, 96*time.Hour)
	selected := taskRunService.SelectStaleUnattendedTaskRuns(time.Now())
	if len(selected) != 2 {
		t.Fatalf("selected %d stale runs, want 2", len(selected))
	}
	if selected[0].TaskRunID != oldest.TaskRunID || selected[1].TaskRunID != older.TaskRunID {
		t.Fatalf("unexpected order: %s, %s", selected[0].TaskRunID, selected[1].TaskRunID)
	}
	for _, taskRun := range selected {
		if taskRun.TaskRunID == fresh.TaskRunID {
			t.Fatal("fresh blocked run must not be selected")
		}
	}
}
