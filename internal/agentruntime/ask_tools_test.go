package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func TestAskInputUsesTypedQuestionAndResultData(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "Need an answer")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask.input"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolContext := bluecollar.WithUserFacingMessage(bluecollar.WithTaskRunID(context.Background(), taskRun.TaskRunID), "Context question must not replace input")
	result, errorValue := toolRegistry.Invoke(toolContext, toolcontract.ToolInvocation{
		ToolName: "ask.input",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"question": "Which report should I use?",
			"choices":  []string{"First", "Second"},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected ask.input success, got %s", result.ContentText())
	}
	var resultDocument askInputResult
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Question != "Which report should I use?" || resultDocument.Status != string(task.TaskStatusWaitingUserInput) || len(resultDocument.Options) != 2 {
		t.Fatalf("expected typed ask.input result, got %+v", resultDocument)
	}
}

func TestAskInputRejectsUnknownInput(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask.input"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "ask.input",
		Input:    toolcontract.MarshalToolInput(map[string]any{"question": "Continue?", "extra": true}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_input_schema" {
		t.Fatalf("expected unknown ask.input field to fail schema validation, got %+v", result)
	}
}
