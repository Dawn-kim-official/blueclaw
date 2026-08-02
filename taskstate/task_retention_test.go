package taskstate

import (
	"testing"
	"time"
)

func TestPruneTerminalTaskRunsBeforeEvictsTerminalAndKeepsActive(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskStepService := NewTaskStepService()
	taskArtifactService := NewTaskArtifactService()
	taskRunService := NewTaskRunService(taskEventService)

	completedRun := taskRunService.CreateTaskRun("person-1", "direct-1", "completed task")
	if _, errorValue := taskRunService.AdvanceTaskRun(completedRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := taskRunService.CompleteTaskRun(completedRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}

	failedRun := taskRunService.CreateTaskRun("person-1", "direct-2", "failed task")
	if _, errorValue := taskRunService.AdvanceTaskRun(failedRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := taskRunService.FailTaskRun(failedRun.TaskRunID, "error"); errorValue != nil {
		t.Fatal(errorValue)
	}

	cancelledRun := taskRunService.CreateTaskRun("person-1", "direct-3", "cancelled task")
	if _, errorValue := taskRunService.CancelTaskRunWithReason(cancelledRun.TaskRunID, "person-1", "stop"); errorValue != nil {
		t.Fatal(errorValue)
	}

	runningRun := taskRunService.CreateTaskRun("person-1", "direct-4", "still running")
	if _, errorValue := taskRunService.AdvanceTaskRun(runningRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}

	plannedRun := taskRunService.CreateTaskRun("person-1", "direct-5", "still planned")

	taskEventService.AppendTaskEvent(completedRun.TaskRunID, "task.note", "some event")
	taskStepService.AddTaskStep(TaskStep{TaskStepID: "step-1", TaskRunID: completedRun.TaskRunID})
	taskArtifactService.AddTaskArtifactBody(completedRun.TaskRunID, "output", "artifact body")

	prunedIDs := taskRunService.PruneTerminalTaskRunsBefore(time.Now().Add(time.Hour))

	if len(prunedIDs) != 3 {
		t.Fatalf("pruned count = %d, want 3", len(prunedIDs))
	}
	prunedSet := map[string]bool{}
	for _, taskRunID := range prunedIDs {
		prunedSet[taskRunID] = true
	}
	if !prunedSet[completedRun.TaskRunID] {
		t.Error("expected completed run to be pruned")
	}
	if !prunedSet[failedRun.TaskRunID] {
		t.Error("expected failed run to be pruned")
	}
	if !prunedSet[cancelledRun.TaskRunID] {
		t.Error("expected cancelled run to be pruned")
	}

	if _, isFound := taskRunService.taskRuns[runningRun.TaskRunID]; !isFound {
		t.Error("expected running run to be kept")
	}
	if _, isFound := taskRunService.taskRuns[plannedRun.TaskRunID]; !isFound {
		t.Error("expected planned run to be kept")
	}
	if _, isFound := taskRunService.taskRuns[completedRun.TaskRunID]; isFound {
		t.Error("expected completed run to be evicted from taskRuns")
	}

	for _, taskRunID := range prunedIDs {
		taskStepService.RemoveTaskRunSteps(taskRunID)
		taskEventService.RemoveTaskRunEvents(taskRunID)
		taskArtifactService.RemoveTaskRunArtifacts(taskRunID)
	}

	if len(taskEventService.ListTaskEvent(completedRun.TaskRunID)) != 0 {
		t.Error("expected task events to be cleared after remove")
	}
	if len(taskStepService.ListTaskStep(completedRun.TaskRunID)) != 0 {
		t.Error("expected task steps to be cleared after remove")
	}
	if len(taskArtifactService.ListTaskArtifact(completedRun.TaskRunID)) != 0 {
		t.Error("expected task artifacts to be cleared after remove")
	}
}

func TestPruneTerminalTaskRunsBeforeRespectsTimeCutoff(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())

	oldRun := taskRunService.CreateTaskRun("person-1", "direct-1", "old completed task")
	if _, errorValue := taskRunService.CompleteTaskRun(oldRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}

	prunedIDs := taskRunService.PruneTerminalTaskRunsBefore(time.Now().Add(-time.Hour))

	if len(prunedIDs) != 0 {
		t.Fatalf("expected no runs pruned with past cutoff, got %d", len(prunedIDs))
	}
	if _, isFound := taskRunService.taskRuns[oldRun.TaskRunID]; !isFound {
		t.Error("expected run to be kept when cutoff is in the past")
	}
}
