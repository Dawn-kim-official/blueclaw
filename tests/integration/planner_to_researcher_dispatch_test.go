package integration

import (
	"testing"

	"github.com/Dawn-kim-official/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func TestPlannerToResearcherDispatch(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := bluecollar.NewAgentKernel(taskRunService, taskStepService)

	taskRun, errorValue := agentKernel.RunTask("person-1", "conversation-1", "draft a policy summary")
	if errorValue != nil {
		t.Fatalf("expected task to run: %v", errorValue)
	}
	if taskRun.Status != task.TaskStatusRunning {
		t.Fatalf("expected task to be running, got %s", taskRun.Status)
	}
	if len(taskStepService.ListTaskStep(taskRun.TaskRunID)) != 3 {
		t.Fatal("expected three task steps to be created")
	}
}
