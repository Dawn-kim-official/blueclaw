package agent

import (
	"context"
	"testing"

	"blueclaw/internal/task"
)

func TestTerminalRunModelApprovalPausesBeforeExecution(t *testing.T) {
	terminalInput := `{"command":"publish-release","approvalRequired":true,"approvalReason":"This command publishes the release."}`
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":` + terminalInput + `}`,
		`{"question":"릴리스를 게시할까요?"}`,
		finishMessageDocument("릴리스를 게시했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolSet := newTestToolSet([]string{TerminalRunToolName})
	invokedInputs := []string{}
	toolSet.RegisterTool(ToolDefinition{Name: TerminalRunToolName}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		return ToolSuccess(`{"status":"published"}`), nil
	})

	firstResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "릴리스를 게시해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolSet,
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if firstResult.TaskRun.Status != task.TaskStatusWaitingApproval || len(invokedInputs) != 0 {
		t.Fatalf("expected approval before terminal execution, calls=%d result=%+v", len(invokedInputs), firstResult)
	}

	secondResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:      "person-1",
		ExistingTaskRunID:      firstResult.TaskRun.TaskRunID,
		IsApprovalContinuation: true,
		ConversationID:         "conversation-1",
		Prompt:                 "승인",
		ResponseLanguage:       ResponseLanguageKorean,
		ToolSet:                toolSet,
		WorkspaceRootPath:      t.TempDir(),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if secondResult.TaskRun.Status != task.TaskStatusCompleted || len(invokedInputs) != 1 || invokedInputs[0] != terminalInput {
		t.Fatalf("expected one approved terminal execution, calls=%+v result=%+v", invokedInputs, secondResult)
	}
}
