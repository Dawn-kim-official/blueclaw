package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type externalHarness struct {
	taskRunService *task.TaskRunService
}

func (harness externalHarness) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if existingTaskRunID := strings.TrimSpace(request.ExistingTaskRunID); existingTaskRunID != "" {
		if existingTaskRun, isFound := harness.taskRunService.FindTaskRun(existingTaskRunID); isFound {
			return agentcontract.AgentTurnResult{TaskRun: existingTaskRun, FinishMessage: "done"}, nil
		}
	}
	return agentcontract.AgentTurnResult{TaskRun: task.TaskRun{Status: task.TaskStatusCompleted}, FinishMessage: "done"}, nil
}

func launchThroughExternalHarness(t *testing.T) (*task.TaskEventService, *task.TaskRunService, TaskLaunchResult) {
	t.Helper()
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskLauncher := NewTaskLauncher(externalHarness{taskRunService: taskRunService}, taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "회의록 정리해줘",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return taskEventService, taskRunService, launchResult
}

func TestAFirstTurnHasATaskRunWhateverHarnessRanIt(t *testing.T) {
	_, taskRunService, launchResult := launchThroughExternalHarness(t)

	taskRunID := strings.TrimSpace(launchResult.TurnResult.TaskRun.TaskRunID)
	if taskRunID == "" {
		t.Fatal("a turn that ran under a harness which creates no task run of its own left nothing for the requester to look at")
	}
	if _, isFound := taskRunService.FindTaskRun(taskRunID); !isFound {
		t.Fatalf("expected the task run %q to be readable from the store", taskRunID)
	}
}

func TestOneTurnOpensOneTaskRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "회의록 정리해줘",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	if taskRunCount := len(taskRunService.ListTaskRun()); taskRunCount != 1 {
		t.Fatalf("the host opens the run and the harness settles it, so one turn is one run, got %d", taskRunCount)
	}
}

func TestAFirstTurnIsAuditedWhateverHarnessRanIt(t *testing.T) {
	taskEventService, _, launchResult := launchThroughExternalHarness(t)

	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	for _, expectedEventName := range []string{"agent.task_launched", "agent.conversation_scope"} {
		if !containsTaskEvent(taskEvents, expectedEventName) {
			t.Fatalf("expected %q to be written for every harness, got %+v", expectedEventName, taskEvents)
		}
	}
}
