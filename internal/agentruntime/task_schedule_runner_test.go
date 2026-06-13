package agentruntime

import (
	"context"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestTaskScheduleRunnerLaunchesDueSchedule(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("scheduled done")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)
	provisioner := &recordingRequesterWorkspaceProvisioner{}
	taskLauncher := NewTaskLauncher(agentKernel, toolCatalogBuilder)
	taskLauncher.UseRequesterWorkspaceProvisioner(provisioner)
	runAt := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)

	result, errorValue := NewTaskScheduleRunner(taskLauncher).RunIfDue(context.Background(), TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Prompt:          "daily brief",
			Kind:            task.TaskScheduleKindOnce,
			RunAt:           &runAt,
			NextRunAt:       &runAt,
		},
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due schedule to run")
	}
	if result.TaskSchedule.LastTaskRunID == "" {
		t.Fatalf("expected last task run id, got %+v", result.TaskSchedule)
	}
	if provisioner.callCount != 1 {
		t.Fatalf("expected scheduled launch to provision requester workspace, got %d calls", provisioner.callCount)
	}
	if result.TaskSchedule.LastRunAt == nil || !result.TaskSchedule.LastRunAt.Equal(runAt) {
		t.Fatalf("expected last run time, got %+v", result.TaskSchedule.LastRunAt)
	}
	if result.TaskSchedule.NextRunAt != nil {
		t.Fatalf("expected one-time schedule to complete, got next run %+v", result.TaskSchedule.NextRunAt)
	}

	taskEvents := taskEventService.ListTaskEvent(result.LaunchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.task_launched") {
		t.Fatalf("expected scheduled launch event, got %+v", taskEvents)
	}
}
