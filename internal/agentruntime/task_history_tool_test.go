package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

func newTaskHistoryTestRegistry(t *testing.T, requesterPersonID string) (*agent.ToolSet, *task.TaskRunService) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: requesterPersonID,
	})
	return toolRegistry, taskRunService
}

func invokeTaskHistory(t *testing.T, toolRegistry *agent.ToolSet, input map[string]any) taskHistoryToolOutput {
	t.Helper()
	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "task.history",
		Input:    agent.MarshalToolInput(input),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected task.history success, got %s", result.ContentText())
	}
	var output taskHistoryToolOutput
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	return output
}

func TestTaskHistoryToolListsRequesterRunsNewestFirst(t *testing.T) {
	toolRegistry, taskRunService := newTaskHistoryTestRegistry(t, "person-1")
	completedRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "프레젠테이션 만들어줘")
	if _, errorValue := taskRunService.CompleteTaskRun(completedRun.TaskRunID, "프레젠테이션을 첨부했어요"); errorValue != nil {
		t.Fatal(errorValue)
	}
	failedRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "사이트 배포해줘")
	if _, errorValue := taskRunService.FailTaskRun(failedRun.TaskRunID, "terminal.run exited 1"); errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.CreateTaskRun("person-2", "conversation-2", "다른 사람 작업")

	output := invokeTaskHistory(t, toolRegistry, map[string]any{})

	if len(output.Tasks) != 2 {
		t.Fatalf("tasks = %+v", output.Tasks)
	}
	if output.Tasks[0].TaskRunID != failedRun.TaskRunID || output.Tasks[0].Status != string(task.TaskStatusFailed) {
		t.Fatalf("first task = %+v", output.Tasks[0])
	}
	if output.Tasks[0].FailureReason != "terminal.run exited 1" {
		t.Fatalf("failure reason = %q", output.Tasks[0].FailureReason)
	}
	if output.Tasks[1].TaskRunID != completedRun.TaskRunID || output.Tasks[1].Result != "프레젠테이션을 첨부했어요" {
		t.Fatalf("second task = %+v", output.Tasks[1])
	}
}

func TestTaskHistoryToolFiltersByStatusAndLimit(t *testing.T) {
	toolRegistry, taskRunService := newTaskHistoryTestRegistry(t, "person-1")
	for index := 0; index < 3; index++ {
		taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "작업")
		if _, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "완료"); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	failedRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "실패한 작업")
	if _, errorValue := taskRunService.FailTaskRun(failedRun.TaskRunID, "provider unavailable"); errorValue != nil {
		t.Fatal(errorValue)
	}

	failedOutput := invokeTaskHistory(t, toolRegistry, map[string]any{"status": "failed"})
	if len(failedOutput.Tasks) != 1 || failedOutput.Tasks[0].TaskRunID != failedRun.TaskRunID {
		t.Fatalf("failed tasks = %+v", failedOutput.Tasks)
	}

	limitedOutput := invokeTaskHistory(t, toolRegistry, map[string]any{"limit": 2})
	if len(limitedOutput.Tasks) != 2 {
		t.Fatalf("limited tasks = %+v", limitedOutput.Tasks)
	}
}

func TestTaskHistoryToolRequiresRequesterPersonID(t *testing.T) {
	toolRegistry, _ := newTaskHistoryTestRegistry(t, "")
	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "task.history",
		Input:    agent.MarshalToolInput(map[string]any{}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected task.history to be unregistered without a requester person, got %s", result.ContentText())
	}
}
