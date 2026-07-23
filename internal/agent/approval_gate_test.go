package agent

import (
	"context"
	"encoding/json"
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
	registerTestTool(toolSet, ToolDefinition{Name: TerminalRunToolName}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		return testToolSuccess(`{"status":"published"}`), nil
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
		ToolSet:                toolSet.WithAllowedToolNames(nil),
		WorkspaceRootPath:      t.TempDir(),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if secondResult.TaskRun.Status != task.TaskStatusCompleted || len(invokedInputs) != 1 || invokedInputs[0] != terminalInput {
		t.Fatalf("expected one approved terminal execution, calls=%+v result=%+v", invokedInputs, secondResult)
	}
}

func TestIsApprovedHeldCallVerbatimMatch(t *testing.T) {
	testCases := []struct {
		name                string
		approvedHeldCallKey string
		actionDocument      turnActionDocument
		expectMatch         bool
	}{
		{
			name:                "exact tool and input matches",
			approvedHeldCallKey: canonicalToolCallKey("task.delete", []byte(`{"taskID":"task-A"}`)),
			actionDocument:      turnActionDocument{ToolName: "task.delete", ToolInput: []byte(`{"taskID":"task-A"}`)},
			expectMatch:         true,
		},
		{
			name:                "same tool with different input does not match",
			approvedHeldCallKey: canonicalToolCallKey("task.delete", []byte(`{"taskID":"task-A"}`)),
			actionDocument:      turnActionDocument{ToolName: "task.delete", ToolInput: []byte(`{"taskID":"task-B"}`)},
			expectMatch:         false,
		},
		{
			name:                "different tool with the same input does not match",
			approvedHeldCallKey: canonicalToolCallKey("task.delete", []byte(`{"taskID":"task-A"}`)),
			actionDocument:      turnActionDocument{ToolName: "message.delete", ToolInput: []byte(`{"taskID":"task-A"}`)},
			expectMatch:         false,
		},
		{
			name:                "consumed grant never matches",
			approvedHeldCallKey: "",
			actionDocument:      turnActionDocument{ToolName: "task.delete", ToolInput: []byte(`{"taskID":"task-A"}`)},
			expectMatch:         false,
		},
		{
			name:                "key ignores unrelated json field ordering",
			approvedHeldCallKey: canonicalToolCallKey("task.delete", []byte(`{"taskID":"task-A","reason":"cleanup"}`)),
			actionDocument:      turnActionDocument{ToolName: "task.delete", ToolInput: []byte(`{"reason":"cleanup","taskID":"task-A"}`)},
			expectMatch:         true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if isApprovedHeldCallVerbatimMatch(testCase.approvedHeldCallKey, testCase.actionDocument) != testCase.expectMatch {
				t.Fatalf("expected match=%v for %s", testCase.expectMatch, testCase.name)
			}
		})
	}
}

func TestExecuteApprovedHeldCallConsumesGrantAfterVerbatimExecution(t *testing.T) {
	heldInput := `{"taskID":"task-A"}`
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "task.delete", heldInput),
		`{"question":"task-A를 삭제할까요?"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"task.delete"})
	invokedInputs := []string{}
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.delete", RequiresApproval: true}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		return testToolSuccess(`{"status":"deleted"}`), nil
	})

	firstResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "task-A 삭제해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"task.delete"},
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if firstResult.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected held call before execution, result=%+v", firstResult)
	}

	if _, errorValue := services.taskRunService.AdvanceTaskRun(firstResult.TaskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	continuationRequest := AgentTurnRequest{
		RequesterPersonID:      "person-1",
		ExistingTaskRunID:      firstResult.TaskRun.TaskRunID,
		IsApprovalContinuation: true,
		ConversationID:         "conversation-1",
		ToolSet:                toolRegistry,
		PinnedToolNames:        []string{"task.delete"},
		WorkspaceRootPath:      t.TempDir(),
	}
	state := buildInitialAgentTaskState(continuationRequest, TurnOptions{}, firstResult.TaskRun.TaskRunID)
	updatedRequest, _, shouldReturn := services.runner.executeApprovedHeldCall(context.Background(), firstResult.TaskRun.TaskRunID, continuationRequest, &state, map[string]turnObservation{})
	if shouldReturn {
		t.Fatalf("expected the verbatim held call to complete without pausing again")
	}
	if len(invokedInputs) != 1 || invokedInputs[0] != heldInput {
		t.Fatalf("expected exactly one verbatim execution, got %+v", invokedInputs)
	}
	if updatedRequest.ApprovedHeldCallKey != "" {
		t.Fatalf("expected the single-use approval grant to be consumed after the verbatim execution, got %q", updatedRequest.ApprovedHeldCallKey)
	}
	if state.Request.ApprovedHeldCallKey != "" {
		t.Fatalf("expected the carried task state to reflect the consumed grant, got %q", state.Request.ApprovedHeldCallKey)
	}
}

func TestCurrentThreadSendSkipsRuntimeApproval(t *testing.T) {
	sendDefinition := testToolDescriptor("message.send")
	sendDefinition.RequiresApproval = true
	sendDefinition.SideEffectClass = ToolSideEffectExternalSend
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{sendDefinition})

	currentThreadCall := turnActionDocument{ToolName: "message.send", ToolInput: json.RawMessage(`{"targetType":"currentThread","message":"요약"}`)}
	if toolCallRequiresRuntimeApproval(toolSet, currentThreadCall) {
		t.Fatal("expected a current-thread send to run without approval, like a reply")
	}
	currentChannelCall := turnActionDocument{ToolName: "message.send", ToolInput: json.RawMessage(`{"targetType":"currentChannel","message":"메모"}`)}
	if toolCallRequiresRuntimeApproval(toolSet, currentChannelCall) {
		t.Fatal("expected a current-channel send to run without approval, like a reply")
	}
	directMessageCall := turnActionDocument{ToolName: "message.send", ToolInput: json.RawMessage(`{"targetType":"directMessage","personHint":"우경","message":"안내"}`)}
	if !toolCallRequiresRuntimeApproval(toolSet, directMessageCall) {
		t.Fatal("expected an external send to keep requiring approval")
	}
}
