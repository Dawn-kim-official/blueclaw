package scheduler

import (
	"testing"
	"time"

	"blueclaw/internal/task"
)

func TestTaskRetentionSweeperSweepOnceRemovesCompletedAndKeepsRunning(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)

	completedRun := taskRunService.CreateTaskRun("person-1", "direct-1", "completed task")
	if _, errorValue := taskRunService.AdvanceTaskRun(completedRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := taskRunService.CompleteTaskRun(completedRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}

	runningRun := taskRunService.CreateTaskRun("person-1", "direct-2", "still running")
	if _, errorValue := taskRunService.AdvanceTaskRun(runningRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}

	sweeper := TaskRetentionSweeper{
		TaskRunService:      taskRunService,
		TaskEventService:    taskEventService,
		TaskStepService:     taskStepService,
		TaskArtifactService: taskArtifactService,
		RetentionDays:       0,
	}

	count := sweeper.SweepOnce(time.Now().Add(15 * 24 * time.Hour))

	if count != 1 {
		t.Fatalf("sweep count = %d, want 1", count)
	}
	if _, isFound := taskRunService.FindTaskRun(runningRun.TaskRunID); !isFound {
		t.Error("expected running run to survive sweep")
	}
	if _, isFound := taskRunService.FindTaskRun(completedRun.TaskRunID); isFound {
		t.Error("expected completed run to be removed by sweep")
	}
}
