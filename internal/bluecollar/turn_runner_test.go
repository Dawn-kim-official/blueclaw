package bluecollar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func structuredFailureToolResult(content string, message string, code string, stage string, retryable bool, safeRetry bool) ToolResult {
	return ToolResult{
		Output: ToolOutput{Content: content},
		Failure: &ToolFailure{
			Kind:            FailureExternalService,
			Code:            code,
			Stage:           stage,
			UserSafeSummary: message,
			Retryable:       retryable,
			SafeRetry:       safeRetry,
		},
	}
}

func TestFinishActionMessagePrefersReplyPartBody(t *testing.T) {
	reply := finishActionMessage(turnActionDocument{
		Message: "요약만 있습니다.",
		ReplyParts: []AgentPart{{
			Type: AgentPartTypeText,
			Text: "사용자에게 전달할 상세 본문입니다.",
		}},
	})

	if reply != "사용자에게 전달할 상세 본문입니다." {
		t.Fatalf("expected reply part body, got %q", reply)
	}
}

func TestAgentTurnRunnerCallsToolsUntilFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{modelTier: "low", contents: []string{
		`{"action":"continue","toolName":"alpha","toolInput":{"value":"one"}}`,
		`{"action":"continue","toolName":"beta","toolInput":{"value":"two"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, TaskLevel: TaskLevelHigh})
	toolRegistry := newTestToolSet([]string{"alpha", "beta"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "beta"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("beta result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID)) != 3 {
		t.Fatalf("expected three task steps, got %d", len(services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID)))
	}
	if len(languageModel.requests) != 3 {
		t.Fatalf("expected three action calls, got %d", len(languageModel.requests))
	}
	llmCallEventCount := 0
	for _, taskEvent := range services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID) {
		if taskEvent.Name == "llm.call" {
			llmCallEventCount++
		}
	}
	if llmCallEventCount != 4 {
		t.Fatalf("expected four llm.call ledger events, got %d", llmCallEventCount)
	}
	for _, taskEvent := range services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID) {
		if taskEvent.Name != "llm.call" {
			continue
		}
		var record llmCallRecord
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue != nil {
			t.Fatalf("expected llm.call ledger body: %v", errorValue)
		}
		if record.SchemaName == agentActionSchemaName && record.ModelTier != "low" {
			t.Fatalf("expected response tier to remain low instead of requested high, got %+v", record)
		}
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "llm.call", "blueclaw_agent_turn_action") {
		t.Fatal("expected llm.call event with action schema name")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.result", "durationMs") {
		t.Fatal("expected tool result event with duration")
	}
}

func TestAgentTurnRunnerAppliesPendingSteeringEvent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("HTML로 작성하겠습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "PDF 보고서를 작성한다")
	services.taskEventService.AppendTaskEvent(taskRun.TaskRunID, "task.steer.requested", marshalEventBody(map[string]string{
		"messageID":   "message-steer",
		"instruction": "PDF 대신 HTML로 작성한다.",
		"reason":      "user corrected output format",
	}))

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRun.TaskRunID,
		ConversationID:    "conversation-1",
		Prompt:            "PDF 보고서를 작성한다",
		ResponseLanguage:  "ko",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("expected steering event to apply: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "task.steer.applied", "message-steer") {
		t.Fatal("expected steer applied event")
	}
	if !strings.Contains(joinMessageContent(languageModel.requests[0].Messages), "PDF 대신 HTML") {
		t.Fatalf("expected steering instruction in model context, got %+v", languageModel.requests[0].Messages)
	}
}

func TestAgentTurnRunnerFailsWhenAttemptStartFails(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("should not run")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	services.taskRunService.UseRepository(failingAttemptStartRepository{errorValue: errors.New("attempt store unavailable token=secret-value")})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ResponseLanguage:  "ko",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("expected attempt start failure to become task result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %+v", result.TaskRun)
	}
	if !strings.Contains(result.FailureNotice.SendableMessage(), "attempt store unavailable") {
		t.Fatalf("expected raw attempt failure notice, got %+v", result.FailureNotice)
	}
	if strings.Contains(result.FailureNotice.SendableMessage(), "secret-value") {
		t.Fatalf("expected secret redaction, got %q", result.FailureNotice.SendableMessage())
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected no action model calls, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerSendsCheckpointAndStillRunsTool(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"작업 중입니다.","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	wasToolCalled := false
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		wasToolCalled = true
		return testToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !wasToolCalled {
		t.Fatal("expected tool to run after checkpoint")
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(checkpoints) != 1 || checkpoints[0].Message != "작업 중입니다." || checkpoints[0].ToolName != "alpha" {
		t.Fatalf("expected checkpoint before tool, got %+v", checkpoints)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.checkpoint.sent", "alpha") {
		t.Fatal("expected checkpoint sent event")
	}
}

type contextCancelingTurnLanguageModel struct {
	cancel         context.CancelFunc
	actionContents []string
	requestIndex   int
	judgeError     error
}

func (model *contextCancelingTurnLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (model *contextCancelingTurnLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == completionJudgeSchemaName {
		model.cancel()
		return llm.StructuredResponse{}, model.judgeError
	}
	content := ""
	if model.requestIndex < len(model.actionContents) {
		content = model.actionContents[model.requestIndex]
	}
	model.requestIndex++
	return llm.StructuredResponse{Content: content}, nil
}

func (model *contextCancelingTurnLanguageModel) GenerateChatCompletion(_ context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	if request.SchemaName == completionReplySchemaName {
		return llm.ChatCompletionResponse{
			FinishReason: "stop",
			Message:      llm.ChatCompletionMessage{Role: "assistant", Content: "오래된 작업을 삭제했습니다."},
		}, nil
	}
	return llm.ChatCompletionResponse{
		FinishReason: "tool_calls",
		Message: llm.ChatCompletionMessage{
			Role: "assistant",
			ToolCalls: []llm.ChatCompletionToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ChatCompletionToolCallFunction{
					Name:      "task.delete",
					Arguments: `{"taskID":"task-1"}`,
				},
			}},
		},
	}, nil
}

func TestAgentTurnRunnerCompletesWhenCallerContextExpiresDuringCompletionJudge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	languageModel := &contextCancelingTurnLanguageModel{
		cancel: cancel,
		actionContents: []string{
			directToolAction("continue", "삭제할게요.", "task.delete", `{"taskID":"task-1"}`),
		},
		judgeError: context.DeadlineExceeded,
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolDefinition := testToolDescriptor("task.delete")
	toolDefinition.Completion = ToolCompletion{Mode: ToolCompletionObservation}
	toolRegistry := newTestToolSetWithDefinitions([]ToolDefinition{toolDefinition})

	result, errorValue := services.runner.RunTurn(ctx, AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "오래된 작업을 삭제해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.delete"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"task.delete"}},
	})

	if errorValue != nil {
		t.Fatalf("expected the turn to succeed despite the caller context expiring mid-judge-call: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected the task to complete, got status %q (replySuppressed=%v) failureReason=%q", result.TaskRun.Status, result.ReplySuppressed, result.TaskRun.FailureReason)
	}
	if result.ReplySuppressed {
		t.Fatal("expected the reply not to be suppressed once the completion gate and reply already exist")
	}
	if result.FinishMessage == "" {
		t.Fatal("expected a non-empty finish message so the connector does not suppress it as missing_user_notice")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event to be recorded")
	}
}

func TestAgentTurnRunnerUsesPostEvidenceWordingAfterCheckpoint(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "추가할게요.", "task.add", `{"title":"고객지원 분기 결산","dueDate":"2026-07-17"}`),
		`{"action":"finish","message":"고객지원 분기 결산 업무를 7월 17일 마감으로 등록했습니다.","completionSummary":"고객지원 분기 결산 업무 등록 완료","replyParts":[{"type":"text","text":"고객지원 분기 결산 업무를 7월 17일 마감으로 등록했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"task.add"}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"task.add"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"taskID":"task-1","title":"고객지원 분기 결산","dueDate":"2026-07-17"}`), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "고객지원 분기 결산 업무를 이번 주 금요일까지로 추가해줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.add"},
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected task creation to succeed: %v", errorValue)
	}
	if len(checkpoints) != 1 || checkpoints[0].Message != "추가할게요." {
		t.Fatalf("expected the pre-tool checkpoint to be sent, got %+v", checkpoints)
	}
	if result.FinishMessage != "고객지원 분기 결산 업무를 7월 17일 마감으로 등록했습니다." {
		t.Fatalf("expected post-evidence wording, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a post-evidence model call, got %d requests", len(languageModel.requests))
	}
	if languageModel.requests[1].StructuredOutputSchema.Name != "blueclaw_agent_turn_action" {
		t.Fatalf("expected a post-evidence action, got %q", languageModel.requests[1].StructuredOutputSchema.Name)
	}
}

func TestAgentTurnRunnerSuppressesCheckpointForSimpleTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"일정을 확인하겠습니다.","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("등록했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	wasToolCalled := false
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		wasToolCalled = true
		return testToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "일정 등록해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		TaskLevel:         TaskLevelXLow,
		EstimatedMinutes:  1,
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !wasToolCalled {
		t.Fatal("expected tool to run after skipped checkpoint")
	}
	if result.FinishMessage != "등록했습니다." {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(checkpoints) != 0 {
		t.Fatalf("expected no checkpoint for simple task, got %+v", checkpoints)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.checkpoint.skipped", "task_level_xlow") {
		t.Fatal("expected xlow task checkpoint skip event")
	}
}

func TestAgentTurnRunnerRunsToolWhenCheckpointFails(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"작업 중입니다.","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	wasToolCalled := false
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		wasToolCalled = true
		return testToolSuccess("alpha result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender: func(context.Context, AgentCheckpoint) error {
			return errors.New("send failed")
		},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !wasToolCalled {
		t.Fatal("expected tool to run after failed checkpoint")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.checkpoint.failed", "send failed") {
		t.Fatal("expected checkpoint failure event")
	}
}

func TestAgentTurnRunnerDoesNotSendCheckpointForRejectedToolCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"첫 작업입니다.","toolName":"schedule.create","toolInput":{"value":"one"}}`,
		`{"action":"continue","message":"다시 실행합니다.","toolName":"schedule.create","toolInput":{"value":"one"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.create"})
	registerTestTool(toolRegistry, ToolDefinition{
		Name:            "schedule.create",
		Namespace:       "schedule",
		SideEffectClass: ToolSideEffectStateChange,
		Completion:      ToolCompletion{Mode: ToolCompletionObservation},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(checkpoints) != 1 || checkpoints[0].Message != "첫 작업입니다." {
		t.Fatalf("expected only accepted tool call checkpoint, got %+v", checkpoints)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.duplicate_tool_call_rejected", "schedule.create") {
		t.Fatal("expected duplicate rejection event")
	}
}

func TestDuplicateSuccessfulToolCallNarrowsNextActionSchemaToTerminalActions(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "일정을 갱신합니다.", "calendar.update", `{"eventID":"evt-1","title":"Standup"}`),
		directToolAction("continue", "다시 갱신합니다.", "calendar.update", `{"eventID":"evt-1","title":"Standup"}`),
		finishMessageDocument("아직 확인 중입니다."),
		`{"action":"finish","message":"완료했습니다.","goalStatus":"satisfied","goalSatisfied":true,"hasRemainingWork":false,"completionEvidenceIDs":["obs-001"],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"calendar.update"})
	registerTestTool(toolRegistry, ToolDefinition{
		Name:            "calendar.update",
		Namespace:       "calendar",
		SideEffectClass: ToolSideEffectStateChange,
		Completion:      ToolCompletion{Mode: ToolCompletionObservation},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("updated"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "캘린더 일정을 갱신해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "완료했습니다." {
		t.Fatalf("expected the cited follow-up finish to complete the task, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.duplicate_tool_call_rejected", "calendar.update") {
		t.Fatal("expected duplicate rejection event")
	}

	actionRequests := []llm.StructuredResponseRequest{}
	for _, request := range languageModel.requests {
		if request.StructuredOutputSchema.Name == "blueclaw_agent_turn_action" {
			actionRequests = append(actionRequests, request)
		}
	}
	if len(actionRequests) != 3 {
		t.Fatalf("expected three normal action-loop requests, got %d: %v", len(actionRequests), structuredRequestNames(languageModel.requests))
	}
	narrowedSchema := actionRequests[2].StructuredOutputSchema.Document
	if actionSchemaHasVariant(t, narrowedSchema, "continue") {
		t.Fatalf("expected the post-duplicate-rejection schema to drop continue/tool variants, got %s", narrowedSchema)
	}
	if !actionSchemaHasVariant(t, narrowedSchema, "finish") {
		t.Fatalf("expected the post-duplicate-rejection schema to still offer finish, got %s", narrowedSchema)
	}
	if !actionSchemaHasVariant(t, narrowedSchema, "fail") {
		t.Fatalf("expected the post-duplicate-rejection schema to still offer fail, got %s", narrowedSchema)
	}

	firstSchema := actionRequests[0].StructuredOutputSchema.Document
	if !actionSchemaHasVariant(t, firstSchema, "continue") {
		t.Fatalf("expected the initial schema to expose the tool palette, got %s", firstSchema)
	}
}

func TestOverlappingRepeatedFileReadDoesNotNarrowNextActionSchema(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "메모를 확인합니다.", FileReadToolName, `{"path":"notes.txt","startLine":1,"lineCount":200}`),
		directToolAction("continue", "다시 확인합니다.", FileReadToolName, `{"path":"notes.txt","startLine":50,"lineCount":100}`),
		finishMessageDocument("확인했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{FileReadToolName})
	registerTestTool(toolRegistry, ToolDefinition{
		Name:            FileReadToolName,
		SideEffectClass: ToolSideEffectRead,
	}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path      string `json:"path"`
			StartLine int    `json:"startLine"`
			LineCount int    `json:"lineCount"`
		}
		_ = json.Unmarshal(invocation.Input, &input)
		startLine := input.StartLine
		if startLine <= 0 {
			startLine = 1
		}
		lineCount := input.LineCount
		if lineCount <= 0 {
			lineCount = 200
		}
		content, _ := json.Marshal(map[string]any{
			"path":      input.Path,
			"startLine": startLine,
			"endLine":   startLine + lineCount - 1,
			"content":   "line content",
		})
		return testToolSuccess(string(content)), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "메모를 확인해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "확인했습니다." {
		t.Fatalf("expected the finish reply, got %+v", result)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.file_read_cache_hit", "notes.txt") {
		t.Fatal("expected a file read cache hit event for the overlapping re-read")
	}
	if countTaskEvents(taskEvents, "agent.duplicate_tool_call_rejected") != 0 {
		t.Fatal("expected the repeated read not to be treated as a duplicate side-effect call")
	}

	actionRequests := []llm.StructuredResponseRequest{}
	for _, request := range languageModel.requests {
		if request.StructuredOutputSchema.Name == "blueclaw_agent_turn_action" {
			actionRequests = append(actionRequests, request)
		}
	}
	if len(actionRequests) != 3 {
		t.Fatalf("expected three normal action-loop requests, got %d: %v", len(actionRequests), structuredRequestNames(languageModel.requests))
	}
	afterCacheHitSchema := actionRequests[2].StructuredOutputSchema.Document
	if !actionSchemaHasVariant(t, afterCacheHitSchema, "continue") {
		t.Fatalf("expected the schema after a read cache hit to keep the full tool palette, got %s", afterCacheHitSchema)
	}
}

func TestAgentTurnRunnerInjectsInstructionPrompt(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		InstructionPrompt: "Use agent-browser for web automation.",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !messagesContain(languageModel.requests[0].Messages, "Use agent-browser for web automation.") {
		t.Fatal("expected instruction prompt to be injected")
	}
}

func TestAgentTurnRunnerAuditsSelectedSkillDecisions(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"terminal.run"})
	for _, toolName := range []string{"terminal.run", "site.serve"} {
		currentToolName := toolName
		registerTestTool(toolRegistry, ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:  "person-1",
		ConversationID:     "conversation-1",
		Prompt:             "피피티 만들어줘",
		ToolSet:            toolRegistry,
		PinnedToolNames:    toolRegistry.ListToolNames(),
		AvailableSkills:    []SkillInstruction{{Name: "presentation", ToolReferences: []string{"terminal.run", "site.serve"}}},
		InstructionPrompt:  "Available skill index.\n\nSelected skill instructions:\nGenerate PPTX with Marp.",
		InstructionSources: []InstructionSource{{Path: "skills/presentation/SKILL.md", SkillName: "presentation", SHA256: "abc"}},
		SkillDecisions: []SkillSelectionDecision{{
			Name:   "presentation",
			Status: "selected",
			Reason: "embedding_similarity",
			Source: InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation", SHA256: "abc"},
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected turn result: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "presentation") {
		t.Fatal("expected selected skill in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "embedding_similarity") {
		t.Fatal("expected selected skill reason in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "skills/presentation/SKILL.md") {
		t.Fatal("expected selected skill source in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "registeredToolCount") ||
		!taskEventsContain(taskEvents, "agent.instructions_loaded", "hiddenDescribedToolNames") ||
		!taskEventsContain(taskEvents, "agent.instructions_loaded", "site.serve") {
		t.Fatal("expected tool visibility debug fields in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "selectedSkillToolReferences") {
		t.Fatal("expected selected skill allowed tools in instructions event")
	}
}

func TestActionSchemaRequiresFailureResolutionWhenRecoveryIsExhausted(t *testing.T) {
	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{ToolSet: newTestToolSet(nil)},
		Options: TurnOptions{RecoveryBudget: RecoveryBudget{}},
		Observations: []turnObservation{{
			ObservationID:      "obs-001",
			Action:             "continue",
			Tool:               "schedule.list",
			Output:             ToolOutput{Content: "schedule storage unavailable"},
			Failure:            &ToolFailure{Kind: FailureExternalService, Code: FailureCodes.OperationFailed.String(), Stage: "schedule_lookup", UserSafeSummary: "schedule storage unavailable"},
			ToolInputKey:       "schedule.list\x00{\"range\":\"today\"}",
			AttemptFingerprint: "schedule.list\x00{\"range\":\"today\"}\x00operation_failed",
		}},
	})
	schemaDocument := request.StructuredOutputSchema.Document
	if !strings.Contains(schemaDocument, `"failureResolution"`) || !strings.Contains(schemaDocument, `"usedFailureFacts"`) {
		t.Fatalf("expected debt-aware schema, got %s", schemaDocument)
	}
	if !structuredRequestsContain([]llm.StructuredResponseRequest{request}, "FailureReportFacts") {
		t.Fatal("expected debt-aware request to inject FailureReportFacts")
	}
	finishMessageVariant := actionSchemaVariant(t, schemaDocument, "finish")
	finishMessageRequired := stringSliceFromAny(finishMessageVariant["required"])
	if !containsString(finishMessageRequired, "message") || !containsString(finishMessageRequired, "failureResolution") {
		t.Fatalf("expected finish to require message and failureResolution, got %+v", finishMessageRequired)
	}
	finishMessageProperties := mapFromAny(finishMessageVariant["properties"])
	finishMessageFailureResolution := mapFromAny(finishMessageProperties["failureResolution"])
	if containsString(stringSliceFromAny(finishMessageFailureResolution["enum"]), failureResolutionFailureReport) {
		t.Fatal("finish schema must not allow failure_report; failure reports must use fail with usedFailureFacts")
	}
	finishGoalSatisfied := mapFromAny(finishMessageProperties["goalSatisfied"])
	if finishGoalSatisfied["type"] != "boolean" {
		t.Fatalf("finish schema goalSatisfied must be a boolean, got %+v", finishGoalSatisfied)
	}
	if _, hasEnum := finishGoalSatisfied["enum"]; hasEnum {
		t.Fatalf("finish schema goalSatisfied must not use a single-value boolean enum; gemini structured output rejects it, got %+v", finishGoalSatisfied)
	}
	failVariant := actionSchemaVariant(t, schemaDocument, "fail")
	failRequired := stringSliceFromAny(failVariant["required"])
	for _, fieldName := range []string{"reason", "goalStatus", "goalSatisfied", "failureResolution", "usedFailureFacts"} {
		if !containsString(failRequired, fieldName) {
			t.Fatalf("expected fail schema to require %s, got %+v", fieldName, failRequired)
		}
	}
	failProperties := mapFromAny(failVariant["properties"])
	failGoalSatisfied := mapFromAny(failProperties["goalSatisfied"])
	if failGoalSatisfied["type"] != "boolean" {
		t.Fatalf("fail schema goalSatisfied must be a boolean, got %+v", failGoalSatisfied)
	}
	if _, hasEnum := failGoalSatisfied["enum"]; hasEnum {
		t.Fatalf("fail schema goalSatisfied must not use a single-value boolean enum; gemini structured output rejects it, got %+v", failGoalSatisfied)
	}
	usedFailureFacts := mapFromAny(failProperties["usedFailureFacts"])
	attempts := mapFromAny(mapFromAny(usedFailureFacts["properties"])["attempts"])
	if attempts["type"] != "array" {
		t.Fatalf("expected usedFailureFacts.attempts array schema, got %+v", attempts)
	}
}

func TestContinueActionSchemaRequiresCompletionIntent(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"task.list"})
	request := BuildAgentActionRequest(agentTaskState{Request: AgentTurnRequest{ToolSet: toolRegistry}})
	continueVariant := actionSchemaVariant(t, request.StructuredOutputSchema.Document, "continue")
	continueRequired := stringSliceFromAny(continueVariant["required"])
	for _, fieldName := range []string{"goalSatisfied", "hasRemainingWork"} {
		if !containsString(continueRequired, fieldName) {
			t.Fatalf("expected continue schema to require %s, got %+v", fieldName, continueRequired)
		}
	}
}

func TestActionSchemaHidesFailWhileRecoveryBudgetRemains(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"site.serve", "file.write"})
	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{ToolSet: toolRegistry, RequiredEvidenceTools: []string{"site.serve"}},
		Options: TurnOptions{RecoveryBudget: defaultRecoveryBudget()},
		Observations: []turnObservation{{
			ObservationID:      "obs-001",
			Action:             "continue",
			Tool:               "site.serve",
			Output:             ToolOutput{Content: "starter scaffold remains"},
			Failure:            &ToolFailure{Kind: FailureInvalidInput, Code: FailureCodes.InvalidInput.String(), Stage: "site_publish", UserSafeSummary: "starter scaffold remains"},
			ToolInputKey:       "site.serve\x00{\"siteID\":\"site-1\"}",
			AttemptFingerprint: "site.serve\x00{\"siteID\":\"site-1\"}\x00invalid_input",
		}},
	})
	schemaDocument := request.StructuredOutputSchema.Document
	if actionSchemaHasVariant(t, schemaDocument, "fail") {
		t.Fatalf("expected fail action to remain hidden while typed recovery is available, got %s", schemaDocument)
	}
	if actionSchemaHasVariant(t, schemaDocument, "finish") {
		t.Fatalf("expected finish action to remain hidden while required recovery evidence is missing, got %s", schemaDocument)
	}
	if !actionSchemaHasVariant(t, schemaDocument, "continue") {
		t.Fatalf("expected recovery tool actions to remain available, got %s", schemaDocument)
	}
}

func TestAgentTurnRunnerBudgetExhaustedContinueTriggersSingleTerminalNoToolsCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "schedule.list", `{"range":"today"}`),
		directToolAction("continue", "", "schedule.list", `{"range":"tomorrow"}`),
		noToolFallbackFinishMessageDocument("I can still answer from the available context."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "schedule.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check my schedule",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal fallback result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if countStructuredRequestsByName(languageModel.requests, "blueclaw_agent_terminal_no_tools_action") != 1 {
		t.Fatalf("expected exactly one terminal no-tools request, got %+v", structuredRequestNames(languageModel.requests))
	}
	if toolCallCount != 1 {
		t.Fatalf("expected denied recovery not to invoke a second tool call, got %d", toolCallCount)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if countTaskEvents(taskEvents, "agent.recovery_budget_exhausted") != 1 {
		t.Fatalf("expected one recovery budget exhausted event, got %+v", taskEvents)
	}
	if taskEventsContain(taskEvents, "agent.no_progress_loop_stopped", "") {
		t.Fatal("expected terminal no-tools path not to stop through watchdog")
	}
}

func TestAgentTurnRunnerTerminalNoToolsAcceptsNoToolFallbackFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule.list","toolInput":{"range":"today"}}`,
		`{"action":"continue","toolName":"schedule.list","toolInput":{"range":"tomorrow"}}`,
		noToolFallbackFinishMessageDocument("The available context is enough to answer without another tool."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"schedule.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "schedule.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check my schedule",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal fallback result: %v", errorValue)
	}
	if result.FinishMessage != "The available context is enough to answer without another tool." {
		t.Fatalf("expected terminal fallback finish, got %q", result.FinishMessage)
	}
	assertTerminalNoToolsSchemasExcludeToolActions(t, languageModel.requests)
}

func TestAgentTurnRunnerTerminalNoToolsAcceptsFailureReportFail(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "schedule.list", `{"range":"today"}`),
		directToolAction("continue", "", "schedule.list", `{"range":"tomorrow"}`),
		failureReportDocument("Schedule lookup is blocked because schedule_lookup returned operation_failed.", "schedule.list", "today", FailureCodes.OperationFailed.String(), "schedule_lookup", "schedule storage unavailable"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "schedule.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check my schedule",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal failure report result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.UserNotice, "schedule_lookup") {
		t.Fatalf("expected failure report reason to be delivered, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_report_facts_used", "schedule_lookup") {
		t.Fatal("expected used failure facts event")
	}
	assertTerminalNoToolsSchemasExcludeToolActions(t, languageModel.requests)
}

func TestAgentTurnRunnerTerminalNoToolsRepairsInvalidOutputWithoutReopeningTools(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "schedule.list", `{"range":"today"}`),
		directToolAction("continue", "", "schedule.list", `{"range":"tomorrow"}`),
		directToolAction("continue", "", "schedule.list", `{"range":"this week"}`),
		noToolFallbackFinishMessageDocument("I repaired the terminal answer without another tool."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "schedule.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check my schedule",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected repaired terminal fallback result: %v", errorValue)
	}
	if result.FinishMessage != "I repaired the terminal answer without another tool." {
		t.Fatalf("expected repaired terminal finish, got %q", result.FinishMessage)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected repair not to invoke tools, got %d calls", toolCallCount)
	}
	if countStructuredRequestsByName(languageModel.requests, "blueclaw_agent_terminal_no_tools_action") != 2 {
		t.Fatalf("expected one terminal repair request, got %+v", structuredRequestNames(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.terminal_no_tools_rejected", "must be finish or fail") {
		t.Fatal("expected terminal no-tools rejection event")
	}
	assertTerminalNoToolsSchemasExcludeToolActions(t, languageModel.requests)
}

func TestAgentTurnRunnerTerminalNoToolsRejectsFinishWithoutFailureResolution(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "schedule.list", `{"range":"today"}`),
		directToolAction("continue", "", "schedule.list", `{"range":"tomorrow"}`),
		finishMessageDocument("premature finish that omits failureResolution"),
		noToolFallbackFinishMessageDocument("recovered after supplying a valid failureResolution."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "schedule.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check my schedule",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal fallback result: %v", errorValue)
	}
	if result.FinishMessage != "recovered after supplying a valid failureResolution." {
		t.Fatalf("expected the repaired finish to be delivered, got %q", result.FinishMessage)
	}
	if countStructuredRequestsByName(languageModel.requests, "blueclaw_agent_terminal_no_tools_action") != 2 {
		t.Fatalf("expected one terminal repair request, got %+v", structuredRequestNames(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.terminal_no_tools_rejected", "failureResolution to be recovered_with_success or no_tool_fallback") {
		t.Fatal("expected a rejection event naming the missing failureResolution requirement")
	}
	assertTerminalNoToolsSchemasExcludeToolActions(t, languageModel.requests)
}

func TestAgentTurnRunnerTerminalNoToolsRejectsFailWithoutReason(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "schedule.list", `{"range":"today"}`),
		directToolAction("continue", "", "schedule.list", `{"range":"tomorrow"}`),
		`{"action":"fail","goalStatus":"blocked","goalSatisfied":false}`,
		failureReportDocument("Schedule lookup is blocked because schedule_lookup returned operation_failed.", "schedule.list", "today", FailureCodes.OperationFailed.String(), "schedule_lookup", "schedule storage unavailable"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "schedule.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check my schedule",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal failure report result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if countStructuredRequestsByName(languageModel.requests, "blueclaw_agent_terminal_no_tools_action") != 2 {
		t.Fatalf("expected one terminal repair request, got %+v", structuredRequestNames(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.terminal_no_tools_rejected", "fail requires a non-empty reason") {
		t.Fatal("expected a rejection event naming the missing reason")
	}
	assertTerminalNoToolsSchemasExcludeToolActions(t, languageModel.requests)
}

func TestAgentTurnRunnerCompletesBrowserOpenWithPostEvidenceReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "브라우저를 열었습니다. 완료했습니다.", "browser.open", `{"url":"https://www.google.com"}`),
		`{"action":"finish","message":"구글 홈페이지를 브라우저에서 열었습니다.","completionSummary":"구글 홈페이지 열기 완료","replyParts":[{"type":"text","text":"구글 홈페이지를 브라우저에서 열었습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"browser.open"}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"browser.open"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"url":"https://www.google.com/"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "브라우저 열어줘.",
		TaskLevel:             TaskLevelXLow,
		EstimatedMinutes:      1,
		TaskShape:             TaskShapeBrowserHandoffTask,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser.open"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "구글 홈페이지를 브라우저에서 열었습니다." {
		t.Fatalf("expected post-evidence browser-open reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a post-evidence model call, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerRejectsBrowserFollowUpReplyWithoutToolEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("말로만 답변"),
		directToolAction("continue", "", "browser.open", `{"url":"https://console.cloud.google.com/"}`),
		finishMessageWithEvidence("열었습니다", "obs-002", "browser.open", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"browser.open"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"url":"https://console.cloud.google.com/"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "다시 열어봐",
		TaskShape:         TaskShapeBrowserHandoffTask,
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser.open"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "열었습니다" {
		t.Fatalf("expected browser-backed reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "browser.") {
		t.Fatal("expected browser follow-up completion gate to reject tool-free reply")
	}
}

func TestBrowserActionSchemaUsesProviderCompatibleObjectInputs(t *testing.T) {
	runner := NewAgentTurnRunner(nil, nil, nil, nil, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.open", "browser.click", "browser.fill", "browser.select", "browser.wait"})
	inputSchemas := map[string]json.RawMessage{
		"browser.open":   json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		"browser.click":  json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"}},"additionalProperties":false}`),
		"browser.fill":   json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		"browser.select": json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		"browser.wait":   json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"milliseconds":{"type":"number"}},"additionalProperties":false}`),
	}
	for toolName, inputSchema := range inputSchemas {
		registerTestTool(toolRegistry, ToolDefinition{Name: toolName, InputSchema: inputSchema}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	schemaDocument := runner.buildActionSchema(toolRegistry, true, nil, false)

	if strings.Contains(schemaDocument, "anyOf") {
		t.Fatalf("expected browser action schema to avoid anyOf, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, `"toolInput":{"oneOf"`) {
		t.Fatalf("expected browser tool inputs to avoid oneOf unions, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, `{"type":"string","minLength":1}`) {
		t.Fatalf("expected browser tool inputs to avoid string shortcut branches, got %s", schemaDocument)
	}
	assertActionSchemaUsesProviderSafeNestedSubset(t, schemaDocument)
	for _, fragment := range []string{
		`"toolName":{"enum":["browser.open"],"type":"string"}`,
		`"properties":{"milliseconds":{"type":"number"},"ref":{"type":"string"},"selector":{"type":"string"},"target":{"type":"string"}}`,
	} {
		if !strings.Contains(schemaDocument, fragment) {
			t.Fatalf("expected action schema to include %q, got %s", fragment, schemaDocument)
		}
	}
}

func assertActionSchemaUsesProviderSafeNestedSubset(t *testing.T, schemaDocument string) {
	t.Helper()
	var document struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &document); errorValue != nil {
		t.Fatalf("action schema is invalid: %v", errorValue)
	}
	for _, variant := range document.OneOf {
		properties, _ := variant["properties"].(map[string]any)
		assertProviderSafeNestedSchemaValue(t, properties, true)
	}
}

func assertProviderSafeNestedSchemaValue(t *testing.T, value any, isPropertiesMap bool) {
	t.Helper()
	document, isDocument := value.(map[string]any)
	if isDocument {
		for fieldName, fieldValue := range document {
			if isPropertiesMap {
				assertProviderSafeNestedSchemaValue(t, fieldValue, false)
				continue
			}
			if fieldName == "additionalProperties" {
				if fieldValue != false {
					t.Fatalf("nested action schema is not closed in %+v", document)
				}
				continue
			}
			if fieldName == "maxItems" {
				t.Fatalf("nested action schema uses unsupported key %s in %+v", fieldName, document)
			}
			if fieldName == "type" && fieldValue == "integer" {
				t.Fatalf("nested action schema uses integer type in %+v", document)
			}
			assertProviderSafeNestedSchemaValue(t, fieldValue, fieldName == "properties")
		}
		return
	}
	values, isValues := value.([]any)
	if isValues {
		for _, item := range values {
			assertProviderSafeNestedSchemaValue(t, item, false)
		}
	}
}

func TestAgentTurnRunnerSiteLoopBuildsReviewsPublishesBeforeFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.serve","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"build the created site","expectedTools":["site.build","artifact.review"],"doneCriteria":["site build succeeds"],"risk":"draft may be incomplete","workingSetReason":"creation must lead into build and review"}}`,
		`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"review the built artifact","expectedTools":["artifact.review","site.serve"],"doneCriteria":["review passes"],"risk":"visual issues may block publish","workingSetReason":"build output needs review before publish"}}`,
		`{"action":"continue","toolName":"artifact.review","toolInput":{"path":"home/sites/site-1/app/dist/index.html"},"nextStepPlan":{"objective":"publish reviewed site","expectedTools":["site.serve","site.list"],"doneCriteria":["publish succeeds"],"risk":"publish may reject stale build","workingSetReason":"review evidence allows publish"}}`,
		`{"action":"continue","toolName":"site.serve","toolInput":{"siteID":"site-1","message":"Publish portfolio"},"nextStepPlan":{"objective":"confirm final status","expectedTools":["site.list"],"doneCriteria":["status shows published URL"],"risk":"status may not reflect latest version","workingSetReason":"final status is required evidence"}}`,
		`{"action":"continue","toolName":"site.list","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish with status evidence","expectedTools":[],"doneCriteria":["finish with published URL"],"risk":"none","workingSetReason":"all required evidence has been collected"}}`,
		`{"action":"finish","message":"같은 URL에 배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"같은 URL에 배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"site.build"},{"observationID":"obs-003","toolName":"artifact.review"},{"observationID":"obs-004","toolName":"site.serve"},{"observationID":"obs-005","toolName":"site.list"}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 8})
	toolRegistry := newTestCapabilityToolSet([]string{"site.list", "site.serve", "site.build", "artifact.review", "site.serve"})
	toolCalls := []string{}
	hasBuildQuality := false
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.list")
		return testToolSuccess(`{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example","revisionCount":1}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.serve"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.serve")
		return testToolSuccess(`{"siteID":"site-1","sourceWorkspacePath":"home/sites/site-1","appWorkspacePath":"home/sites/site-1/app","publishedURL":"https://portfolio.example"}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.build")
		hasBuildQuality = true
		return testToolSuccess(`{"qualityPath":"home/sites/site-1/.internkim/build-quality.json","distPath":"home/sites/site-1/app/dist"}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "artifact.review"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "artifact.review")
		return testToolSuccess(`{"status":"passed","blockingIssueCount":0}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{
		Name:            "site.serve",
		Namespace:       "site",
		SideEffectClass: ToolSideEffectExternalPublish,
		Completion:      ToolCompletion{Mode: ToolCompletionObservation},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.serve")
		if !hasBuildQuality {
			return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "site_publish", "missing build-quality.json"), nil
		}
		return testToolSuccess(`{"siteID":"site-1","publishedURL":"https://portfolio.example","currentVersionID":"rev-2"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 만들고 배포해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.list", "site.build", "artifact.review", "site.serve"},
		AvailableSkills: []SkillInstruction{{
			Name:           "site-prototype",
			ToolReferences: []string{"site.list", "site.serve", "site.build", "artifact.review", "site.serve"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	})
	if errorValue != nil {
		t.Fatalf("expected site loop to succeed: %v", errorValue)
	}
	expectedCalls := []string{"site.serve", "site.build", "artifact.review", "site.serve", "site.list"}
	if strings.Join(toolCalls, ",") != strings.Join(expectedCalls, ",") {
		t.Fatalf("expected site tool loop %v, got %v", expectedCalls, toolCalls)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted || !strings.Contains(result.FinishMessage, "배포") {
		t.Fatalf("expected completed publish finish, got status=%s message=%q", result.TaskRun.Status, result.FinishMessage)
	}
}

func TestAgentTurnRunnerSiteWorkingSetKeepsCreationRouteWithRequiredEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{
		"site.list",
		"site.serve",
		"file.write",
		"terminal.run",
		"site.build",
		"artifact.review",
		"site.serve",
		"file.deliver",
	})
	request := AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "김인턴 너의 개인 홈페이지 하나 만들어서 배포해봐.",
		ToolSet:               toolRegistry,
		PinnedToolNames:       []string{"site.list", "site.serve", "file.write", "site.build", "artifact.review", "site.serve"},
		RequiredEvidenceTools: []string{"site.list", "site.build", "site.serve", "file.deliver"},
		AvailableSkills: []SkillInstruction{{
			Name: "site-prototype",
			ToolReferences: []string{
				"site.list",
				"site.serve",
				"file.write",
				"terminal.run",
				"site.build",
				"artifact.review",
				"site.serve",
				"file.deliver",
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "김인턴 너의 개인 홈페이지 하나 만들어서 배포해봐.",
			Status:              ActiveGoalStatusActive,
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"site.list", "site.build", "site.serve", "file.deliver"},
				ArtifactRequirement:   ArtifactRequirementRequired,
			},
		},
	}

	stepRequest := services.runner.requestForStep(context.Background(), request, agentTaskState{Request: request})
	for _, toolName := range []string{"site.list", "site.serve", "file.write", "site.build", "artifact.review", "site.serve"} {
		if !stepRequest.ToolSet.CanExpose(toolName) {
			t.Fatalf("expected initial site working set to expose %s, got %+v", toolName, stepRequest.ToolExposure.ExposedToolIDs)
		}
	}
}

func TestAgentTurnRunnerReselectsToolsAfterRejectedSiteFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"site.serve","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"build the draft before finishing","expectedTools":["site.build"],"doneCriteria":["build evidence exists"],"risk":"draft creation alone is not completion","workingSetReason":"site.build is required evidence"}}`,
			`{"action":"finish","message":"초안이 만들어졌습니다.","replyParts":[{"type":"text","text":"초안이 만들어졌습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.serve"}]}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish after build evidence","expectedTools":[],"doneCriteria":["build observation exists"],"risk":"none","workingSetReason":"required evidence has been collected"}}`,
			finishMessageWithEvidence("빌드까지 완료했습니다.", "obs-003", "site.build", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"site.serve", "site.build"})
	toolCalls := []string{}
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.serve"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.serve")
		return testToolSuccess(`{"siteID":"site-1","status":"draft"}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.build")
		return testToolSuccess(`{"siteID":"site-1","distPath":"home/sites/site-1/app/dist"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 만들고 배포해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.build"},
		AvailableSkills: []SkillInstruction{{
			Name:           "site-prototype",
			ToolReferences: []string{"site.serve", "site.build"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	})
	if errorValue != nil {
		t.Fatalf("expected rejected finish to recover into build: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Join(toolCalls, ",") != "site.serve,site.build" {
		t.Fatalf("expected create then build, got %+v", toolCalls)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "site.build") {
		t.Fatal("expected early finish to be rejected by completion gate")
	}
	if !taskEventsContain(events, "agent.step_working_set", "selected_skills") {
		t.Fatal("expected selected direct tools in the per-iteration working set")
	}
}

func TestAgentTurnRunnerRejectsFailAfterSiteSourceWriteBeforeBuildPublish(t *testing.T) {
	// site.build was removed as a native/capability tool (commit d4a0e36):
	// Blueclaw no longer owns site build logic, the build step is an ordinary
	// terminal.run, and only the publish step is guarded by the workflow
	// recovery gate.
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"/workspace/sites/site-1/draft/app/src/App.tsx","content":"export default function App(){return <main>Pretty</main>}"}}`,
			`{"action":"fail","reason":"cannot continue","goalStatus":"blocked","goalSatisfied":false,"remainingWork":"build and publish still needed"}`,
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"npm run build","workingDirectoryPath":"/workspace/sites/site-1/draft/app"}}`,
			directToolAction("continue", "", "site.serve", `{"siteID":"site-1"}`),
			finishMessageWithEvidence("배포했습니다: https://pretty.example", "obs-004", "site.serve", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 8})
	toolRegistry := newHybridKernelCapabilityToolSet([]string{"file.write", "terminal.run"}, []string{"site.serve"})
	toolCalls := []string{}
	registerTestTool(toolRegistry, ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "file.write")
		return testToolSuccess(`{"path":"/workspace/sites/site-1/draft/app/src/App.tsx"}`), nil
	})
	registerTestTool(toolRegistry, terminalRunTestToolDefinition(), func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "terminal.run")
		data := json.RawMessage(`{"mode":"command","completed":true,"exitCode":0,"stdout":"built","stderr":"","timedOut":false,"outputTrimmed":false}`)
		return ToolSuccessData(string(data), data), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{
		Name:            "site.serve",
		Namespace:       "site",
		SideEffectClass: ToolSideEffectExternalPublish,
		Completion:      ToolCompletion{Mode: ToolCompletionObservation},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.serve")
		return testToolSuccess(`{"siteID":"site-1","publishedURL":"https://pretty.example"}`), nil
	})

	request := AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "사이트 더 예쁘게 수정하고 배포해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.serve"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.serve"},
		},
	}
	if !turnRequestLooksLikeSitePrototypeWork(request) {
		t.Fatal("expected typed site descriptor to identify site work")
	}
	if !sitePublishIsRequired(request) {
		t.Fatal("expected typed site contract to require publish")
	}
	result, errorValue := services.runner.RunTurn(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected recoverable fail to continue: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Join(toolCalls, ",") != "file.write,terminal.run,site.serve" {
		t.Fatalf("expected write then build/publish, got %+v", toolCalls)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recoverable_fail_rejected", "site.serve") {
		t.Fatal("expected recoverable fail rejection to suggest publish")
	}
}

func TestSiteRequestWithCalendarContentDoesNotPinCalendarTools(t *testing.T) {
	request := AgentTurnRequest{
		Prompt: "메일, 일정, 브라우저 제어 역량을 소개하는 홈페이지를 만들어서 배포해줘",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "메일, 일정, 브라우저 제어 역량을 소개하는 홈페이지를 만들어서 배포해줘",
			OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{
				{ID: "site-public-link", Type: "link", Description: "public website URL", Required: true},
			}},
		},
	}

	updatedRequest := requestWithStepWorkingSetTools(request, nil)

	if stringSliceContains(updatedRequest.PinnedToolNames, "calendar.add") || stringSliceContains(updatedRequest.PinnedToolNames, "calendar.delete") {
		t.Fatalf("did not expect calendar operations pinned for site content mention, got %+v", updatedRequest.PinnedToolNames)
	}
}

func TestSlidesRequestWithCalendarContentDoesNotPinCalendarTools(t *testing.T) {
	request := AgentTurnRequest{
		Prompt: "메일, 일정, 브라우저 제어 역량을 소개하는 5장 발표자료를 PPTX로 만들어줘",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "메일, 일정, 브라우저 제어 역량을 소개하는 5장 발표자료를 PPTX로 만들어줘",
			OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{
				{ID: "attached-file", Type: "file", Description: "PPTX file", Required: true},
			}},
		},
	}

	updatedRequest := requestWithStepWorkingSetTools(request, nil)

	if stringSliceContains(updatedRequest.PinnedToolNames, "calendar.add") || stringSliceContains(updatedRequest.PinnedToolNames, "calendar.delete") {
		t.Fatalf("did not expect calendar operations pinned for slides content mention, got %+v", updatedRequest.PinnedToolNames)
	}
}

func TestAgentTurnRunnerPausesBeforeRequiresApprovalDirectTool(t *testing.T) {
	heldInput := `{"eventHint":"event-1"}`
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "calendar.delete", heldInput),
		`{"question":"이 일정을 삭제할까요?"}`,
		`{"action":"finish","message":"일정을 삭제했습니다.","replyParts":[{"type":"text","text":"일정을 삭제했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"calendar.delete"}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"calendar.delete"})
	invokedInputs := []string{}
	registerTestTool(toolRegistry, ToolDefinition{Name: "calendar.delete", RequiresApproval: true}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		return testToolSuccess(`{"eventID":"event-1","status":"deleted"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "일정 삭제해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"calendar.delete"},
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(invokedInputs) != 0 {
		t.Fatalf("expected direct tool execution to wait for approval, got %+v", invokedInputs)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "approval.pending_call", heldInput) {
		t.Fatalf("expected direct tool approval hold, events=%+v", events)
	}
}

func TestAgentTurnRunnerApprovalRequiredPausesAndExecutesHeldCall(t *testing.T) {
	heldInput := `{"targetType":"directMessage","personHint":"테스트","message":"오늘 오후 3시에 확인하자"}`
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "message.send", heldInput),
		`{"question":"테스트에게 다음 내용을 보낼까요?\n\n오늘 오후 3시에 확인하자"}`,
		`{"action":"finish","message":"테스트이에게 DM을 보냈습니다.","replyParts":[{"type":"text","text":"테스트이에게 DM을 보냈습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"message.send"}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	invokedInputs := []string{}
	wasExecutedBeforeApprovalContinuationModel := false
	registerTestTool(toolRegistry, ToolDefinition{Name: "message.send"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		if len(invokedInputs) == 1 {
			return ToolResult{
				Output: ToolOutput{Content: "requires approval"},
				Failure: &ToolFailure{
					Kind:             FailureExternalService,
					Code:             FailureCodes.OperationFailed.String(),
					Stage:            "authorization",
					UserSafeSummary:  "requires approval",
					RequiresApproval: true,
				},
			}, nil
		}
		wasExecutedBeforeApprovalContinuationModel = len(languageModel.requests) == 2
		return testToolSuccess(`{"messageID":"message-1","deliveryStatus":"sent"}`), nil
	})

	firstResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "테스트에게 DM 보내줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolRegistry,
		PinnedToolNames:       []string{"message.send"},
		RequiredEvidenceTools: []string{"message.send"},
		WorkspaceRootPath:     t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected first turn to pause: %v", errorValue)
	}
	if firstResult.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval task, got %s events=%+v", firstResult.TaskRun.Status, services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID))
	}
	if !strings.Contains(firstResult.UserNotice, "테스트") || !strings.Contains(firstResult.UserNotice, "오늘 오후 3시에 확인하자") {
		t.Fatalf("expected readable confirmation, got %q", firstResult.UserNotice)
	}
	firstTurnEvents := services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID)
	if !taskEventsContain(firstTurnEvents, "approval.pending_call", heldInput) {
		t.Fatalf("expected held call event with original input, events=%+v", firstTurnEvents)
	}
	if !taskEventsContain(firstTurnEvents, "confirmation.requested", "external_send") {
		t.Fatalf("expected confirmation request event, events=%+v", firstTurnEvents)
	}
	if taskEventsContain(firstTurnEvents, "agent.failure_debt_created", "") {
		t.Fatalf("approval_required must not create failure debt, events=%+v", firstTurnEvents)
	}

	secondResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:      "person-1",
		ExistingTaskRunID:      firstResult.TaskRun.TaskRunID,
		IsApprovalContinuation: true,
		ConversationID:         "conversation-1",
		Prompt:                 "확인",
		ResponseLanguage:       ResponseLanguageKorean,
		ToolSet:                toolRegistry,
		PinnedToolNames:        []string{"message.send"},
		RequiredEvidenceTools:  []string{"message.send"},
		WorkspaceRootPath:      t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected approval continuation to complete: %v", errorValue)
	}
	if secondResult.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", secondResult.TaskRun.Status, services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID))
	}
	if secondResult.FinishMessage != "테스트이에게 DM을 보냈습니다." {
		t.Fatalf("expected approval continuation final reply, got %q", secondResult.FinishMessage)
	}
	if secondResult.TaskRun.Result != secondResult.FinishMessage {
		t.Fatalf("expected persisted approval continuation reply, got %q", secondResult.TaskRun.Result)
	}
	if len(languageModel.requests) != 3 {
		t.Fatalf("expected confirmation and post-approval final model calls, got %d", len(languageModel.requests))
	}
	if len(invokedInputs) != 2 {
		t.Fatalf("expected original attempt and deterministic retry, got %d", len(invokedInputs))
	}
	if invokedInputs[0] != heldInput || invokedInputs[1] != heldInput {
		t.Fatalf("expected exact held input to be reused, got %+v", invokedInputs)
	}
	if !wasExecutedBeforeApprovalContinuationModel {
		t.Fatal("expected held call to execute before the approval-continuation model step")
	}
	secondTurnEvents := services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID)
	if !taskEventsContain(secondTurnEvents, "approval.executed", "message.send") {
		t.Fatalf("expected approval executed event, events=%+v", secondTurnEvents)
	}
	if !taskEventsContain(secondTurnEvents, "tool.message.send.result", "message-1") {
		t.Fatalf("expected deterministic send result, events=%+v", secondTurnEvents)
	}
}

func TestAgentTurnRunnerApprovalContinuationRequiresNewApprovalForDifferentHeldCallInput(t *testing.T) {
	heldInputA := `{"taskID":"task-A"}`
	heldInputB := `{"taskID":"task-B"}`
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "task.delete", heldInputA),
		`{"question":"task-A를 삭제할까요?"}`,
		directToolAction("continue", "", "task.list", `{}`),
		directToolAction("continue", "", "task.delete", heldInputB),
		`{"question":"task-B를 삭제할까요?"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"task.delete", "task.list"})
	invokedDeleteInputs := []string{}
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.delete", RequiresApproval: true}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedDeleteInputs = append(invokedDeleteInputs, string(invocation.Input))
		return testToolSuccess(`{"status":"deleted"}`), nil
	})

	firstResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "task-A 삭제해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"task.delete", "task.list"},
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected first turn to pause: %v", errorValue)
	}
	if firstResult.TaskRun.Status != task.TaskStatusWaitingApproval || len(invokedDeleteInputs) != 0 {
		t.Fatalf("expected held delete before any execution, calls=%+v result=%+v", invokedDeleteInputs, firstResult)
	}

	secondResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:      "person-1",
		ExistingTaskRunID:      firstResult.TaskRun.TaskRunID,
		IsApprovalContinuation: true,
		ConversationID:         "conversation-1",
		Prompt:                 "승인",
		ResponseLanguage:       ResponseLanguageKorean,
		ToolSet:                toolRegistry,
		PinnedToolNames:        []string{"task.delete", "task.list"},
		WorkspaceRootPath:      t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected approval continuation to pause again for the different held call: %v", errorValue)
	}
	if secondResult.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected a new approval pause for task-B, got %s events=%+v", secondResult.TaskRun.Status, services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID))
	}
	if len(invokedDeleteInputs) != 1 || invokedDeleteInputs[0] != heldInputA {
		t.Fatalf("expected only the verbatim held call for task-A to execute, got %+v", invokedDeleteInputs)
	}
	secondTurnEvents := services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID)
	if !taskEventsContain(secondTurnEvents, "approval.executed", "task-A") {
		t.Fatalf("expected verbatim task-A execution recorded, events=%+v", secondTurnEvents)
	}
	if !taskEventsContain(secondTurnEvents, "approval.pending_call", heldInputB) {
		t.Fatalf("expected a fresh approval hold for task-B, not a silent execution, events=%+v", secondTurnEvents)
	}
}

func TestNextApprovalExecutionObservationIDUsesHighestExistingObservationID(t *testing.T) {
	taskEvents := []task.TaskEvent{
		{Name: "tool.site.list.result", Body: `{"observationID":"obs-001"}`},
		{Name: "agent.checkpoint.sent", Body: `{"observationID":"obs-002"}`},
		{Name: "tool.message.send.result", Body: `{"observationID":"obs-003"}`},
	}

	observationID := nextApprovalExecutionObservationID(taskEvents)

	if observationID != "obs-004" {
		t.Fatalf("expected obs-004, got %q", observationID)
	}
}

func TestAgentTurnRunnerSteersStalledTurnBeforeStopping(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"unknown"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 40, MaxToolCallCount: 40})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
	})
	if errorValue != nil {
		t.Fatalf("expected terminal result, got error: %v", errorValue)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.stall_exit_directive", "") {
		t.Fatal("expected a stall-exit steer before terminating the no-progress loop")
	}
	if result.TaskRun.Status == task.TaskStatusRunning {
		t.Fatalf("expected the stalled turn to terminate, got status %s", result.TaskRun.Status)
	}
}

func TestAgentTurnRunnerFinalizesSatisfiedGoalAtIterationEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "browser.screenshot", `{}`),
		directToolAction("continue", "", "browser.screenshot", `{}`),
		finishMessageWithEvidence("캡처했습니다.", "obs-002", "browser.screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 2})
	toolRegistry := newTestCapabilityToolSet([]string{"browser.screenshot"})
	screenshotIndex := 0
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		screenshotIndex++
		filename := fmt.Sprintf("browser-screenshot-%d.png", screenshotIndex)
		return ToolResult{
			Output: ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/` + filename + `"}`},
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/" + filename,
				Filename:    filename,
				ContentType: "image/png",
				SizeBytes:   10,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "스크린샷 줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser.screenshot"},
	})
	if errorValue != nil {
		t.Fatalf("expected attachment completion, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "browser-screenshot-2.png" {
		t.Fatalf("expected latest screenshot attachment, got %+v", result.Attachments)
	}
	if result.FinishMessage != "캡처했습니다." {
		t.Fatalf("expected finalizer reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.finalizer_action", "obs-002") {
		t.Fatal("expected finalizer action with completion evidence")
	}
}

func TestAgentTurnRunnerFinalizesRepeatedSuccessfulSideEffectWithoutPlannedEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "task.add", `{"prompt":"보고서 작성"}`),
		directToolAction("continue", "", "task.add", `{"prompt":"보고서 작성"}`),
		finishMessageWithEvidence("업무를 등록했습니다.", "obs-001", "task.add", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"task.add"})
	toolCallCount := 0
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"task-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "보고서 작성 업무를 등록해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the side effect to run once, got %d", toolCallCount)
	}
	if result.FinishMessage != "업무를 등록했습니다." {
		t.Fatalf("expected finalizer reply, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerFinalizesRepeatedSuccessfulReadWithoutExecutingAgain(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.list","toolInput":{"weekFrom":0,"weekTo":0}}`,
		`{"action":"continue","toolName":"task.list","toolInput":{"weekFrom":0,"weekTo":0}}`,
		finishMessageWithEvidence("업무가 있습니다.", "obs-001", "task.list", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"task.list"})
	toolCallCount := 0
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"tasks":[{"taskID":"task-1"}]}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "업무를 조회해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one read before finalization, got %d", toolCallCount)
	}
	if result.FinishMessage != "업무가 있습니다." {
		t.Fatalf("expected finalizer reply, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerFinalizesReadAfterCorrectedInputRecovery(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.list","toolInput":{"query":"invalid"}}`,
		`{"action":"continue","toolName":"task.list","toolInput":{"weekFrom":0,"weekTo":0}}`,
		`{"action":"continue","toolName":"task.list","toolInput":{"weekFrom":0,"weekTo":0}}`,
		finishMessageWithEvidence("업무가 있습니다.", "obs-003", "task.list", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"task.list"})
	toolCallCount := 0
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.list"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		toolCallCount++
		if strings.Contains(string(invocation.Input), `"query"`) {
			return ToolInputFailure("input must be an object"), nil
		}
		return testToolSuccess(`{"tasks":[{"taskID":"task-1"}]}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "업무를 조회해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.list"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 2 {
		t.Fatalf("expected failed input and one corrected read, got %d calls", toolCallCount)
	}
}

func TestAgentTurnRunnerFinalizesSuccessfulReadDespiteUnsatisfiedReadHint(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.list","toolInput":{"weekFrom":0,"weekTo":0}}`,
		`{"action":"continue","toolName":"task.list","toolInput":{"weekFrom":0,"weekTo":0}}`,
		finishMessageWithEvidence("업무가 있습니다.", "obs-001", "task.list", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"task.list", "memory.search"})
	toolCallCount := 0
	registerTestTool(toolRegistry, ToolDefinition{Name: "task.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"tasks":[{"taskID":"task-1"}]}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "업무를 조회해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"memory.search"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one read before finalization, got %d", toolCallCount)
	}
}

func TestAgentTurnRunnerDoesNotDeliverAttachmentsWhenFinalizerFails(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "browser.screenshot", `{}`),
		`{"action":"fail","reason":"not complete"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestCapabilityToolSet([]string{"browser.screenshot"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/browser-screenshot.png"}`},
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/browser-screenshot.png",
				Filename:    "browser-screenshot.png",
				ContentType: "image/png",
				SizeBytes:   10,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "스크린샷 줘",
		TaskShape:             TaskShapeBrowserHandoffTask,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser.screenshot"},
	})
	if errorValue != nil {
		t.Fatalf("expected effort result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no secret attachment delivery, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.finalizer_rejected", "finalizer did not return finish") {
		t.Fatal("expected finalizer rejection event")
	}
}

func TestAgentTurnRunnerDoesNotCompleteEffortStopFromUnrequestedAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.pick","toolInput":{}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"file.pick"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "file.pick"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/report.txt"}`},
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/report.txt",
				Filename:    "report.txt",
				ContentType: "text/plain",
				SizeBytes:   10,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do some work",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected effort result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no delivery attachments, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerFailsWhenMaximumIterationsAreExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{"작업을 시작했지만 완료 전에 멈췄습니다. 다시 시도하면 이어서 처리할 수 있어요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("again"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected fallback result, got error: %v", errorValue)
	}
	if result.UserNotice != "작업을 시작했지만 완료 전에 멈췄습니다. 다시 시도하면 이어서 처리할 수 있어요." {
		t.Fatalf("expected generated limit reply, got %q", result.UserNotice)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %s", result.TaskRun.Status)
	}
}

func TestAgentTurnRunnerEscalatesIterationLimitAfterDurableProgress(t *testing.T) {
	// site.build no longer exists as a distinct native capability (build now
	// runs through an ordinary terminal.run, per commit d4a0e36); terminal.run
	// with a zero exit code is the current qualifying "durable progress" tool.
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/index.html","content":"one"}}`,
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"npm run build"}}`,
			finishMessageDocument("continued after escalation"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		TaskLevel:         TaskLevelXLow,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "terminal.run"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"path":"tmp/app/index.html"}`), nil
	})
	registerTestTool(toolRegistry, terminalRunTestToolDefinition(), func(context.Context, ToolInvocation) (ToolResult, error) {
		data := json.RawMessage(`{"mode":"command","completed":true,"exitCode":0,"stdout":"built","stderr":"","timedOut":false,"outputTrimmed":false}`)
		return ToolSuccessData(string(data), data), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build the site",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "terminal.run"},
	})

	if errorValue != nil {
		t.Fatalf("expected escalation run, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task after escalation, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"newTaskLevel":"low"`) {
		t.Fatalf("expected budget escalation event, got %+v", taskEvents)
	}
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"qualifyingEventIDs":["obs-001","obs-002"]`) {
		t.Fatalf("expected qualifying event IDs, got %+v", taskEvents)
	}
}

func TestAgentTurnRunnerDoesNotEscalateIterationLimitForInspectionOnlyProgress(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.read","toolInput":{"path":"tmp/app/index.html"}}`,
			`{"action":"continue","toolName":"site.list","toolInput":{"siteID":"site-1"}}`,
		},
		textResponses: []string{"progress saved"},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		TaskLevel:         TaskLevelXLow,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.read", "site.list"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "file.read"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"path":"tmp/app/index.html","content":"one"}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"status":"draft"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "inspect the site",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.read", "site.list"},
	})

	if errorValue != nil {
		t.Fatalf("expected blocked limit result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.budget_escalated", "") {
		t.Fatal("did not expect inspection-only progress to escalate")
	}
}

func TestAgentTurnRunnerEscalationIsOneDirectionalAndPersisted(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/a","content":"one"}}`,
			`{"action":"continue","toolName":"file.edit","toolInput":{"edits":[{"path":"tmp/app/a","oldText":"one","newText":"two"}]}}`,
			`{"action":"continue","toolName":"file.edit","toolInput":{"path":"tmp/app/a","oldText":"one","newText":"two"}}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
			finishMessageDocument("done"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		TaskLevel:         TaskLevelXLow,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "file.edit", "site.build"})
	for _, toolName := range []string{"file.write", "file.edit", "site.build"} {
		registerTestTool(toolRegistry, ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess(`{"ok":true}`), nil
		})
	}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "keep building",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "file.edit", "site.build"},
	})

	if errorValue != nil {
		t.Fatalf("expected completed run, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if countTaskEvents(taskEvents, "agent.budget_escalated") != 1 {
		t.Fatalf("expected exactly one persisted escalation, got %+v", taskEvents)
	}
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"previousTaskLevel":"xlow"`) ||
		!taskEventsContain(taskEvents, "agent.budget_escalated", `"newTaskLevel":"low"`) {
		t.Fatalf("expected xlow to low escalation, got %+v", taskEvents)
	}
}

func TestAgentTurnRunnerCheckpointsAtMaxIterationCeiling(t *testing.T) {
	// site.build no longer exists as a distinct native capability (build now
	// runs through an ordinary terminal.run, per commit d4a0e36); terminal.run
	// with a zero exit code is the current qualifying "durable progress" tool.
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/a","content":"one"}}`,
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"npm run build"}}`,
		},
		textResponses: []string{"progress saved"},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		TaskLevel:         TaskLevelMax,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "terminal.run"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"path":"tmp/app/a"}`), nil
	})
	registerTestTool(toolRegistry, terminalRunTestToolDefinition(), func(context.Context, ToolInvocation) (ToolResult, error) {
		data := json.RawMessage(`{"mode":"command","completed":true,"exitCode":0,"stdout":"built","stderr":"","timedOut":false,"outputTrimmed":false}`)
		return ToolSuccessData(string(data), data), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "finish extended work",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "terminal.run"},
	})

	if errorValue != nil {
		t.Fatalf("expected checkpoint result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked checkpoint task, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, "agent.budget_escalated", "") {
		t.Fatal("did not expect escalation past the max task level")
	}
	if !taskEventsContain(taskEvents, "agent.limit_checkpoint", `"qualifyingEventIDs":["obs-001","obs-002"]`) {
		t.Fatalf("expected limit checkpoint event, got %+v", taskEvents)
	}
}

func terminalRunTestToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name: "terminal.run",
		ResultContract: &ToolResultContract{
			Schema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"mode":{"const":"command"},
					"completed":{"const":true},
					"exitCode":{"type":"integer"},
					"stdout":{"type":"string"},
					"stderr":{"type":"string"},
					"timedOut":{"type":"boolean"},
					"outputTrimmed":{"type":"boolean"}
				},
				"required":["mode","completed","exitCode","stdout","stderr","timedOut","outputTrimmed"],
				"additionalProperties":false
			}`),
		},
	}
}

func TestAgentTurnRunnerStopsWhenToolEffortIsExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			directToolAction("continue", "", "loop", `{}`),
			directToolAction("continue", "", "loop", `{}`),
		},
		textResponses: []string{"도구 호출이 더 진행되기 전에 멈췄습니다. 확인된 내용까지만 바탕으로 다시 이어갈 수 있어요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3, MaxToolCallCount: 1})
	toolRegistry := newTestCapabilityToolSet([]string{"loop"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("again"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.UserNotice != "도구 호출이 더 진행되기 전에 멈췄습니다. 확인된 내용까지만 바탕으로 다시 이어갈 수 있어요." {
		t.Fatalf("expected generated limit reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_tool_calls") {
		t.Fatal("expected limit stop event")
	}
}

type turnRunnerTestServices struct {
	runner              *AgentTurnRunner
	taskRunService      *task.TaskRunService
	taskEventService    *task.TaskEventService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
}

type failingAttemptStartRepository struct {
	errorValue error
	taskRuns   map[string]task.TaskRun
}

func (repository failingAttemptStartRepository) SaveTaskRun(taskRun task.TaskRun) error {
	return nil
}

func (repository failingAttemptStartRepository) StartTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return repository.errorValue
}

func (repository failingAttemptStartRepository) FinishTaskRunAttempt(task.TaskRun, task.TaskAttempt) error {
	return nil
}

func (repository failingAttemptStartRepository) TransitionTaskRun(transition task.TaskRunTransition) (task.TaskRun, error) {
	if transition.StartedAttempt != nil {
		return task.TaskRun{}, repository.errorValue
	}
	return task.TaskRun{
		TaskRunID:        transition.TaskRunID,
		Status:           transition.ToState,
		FailureReason:    transition.FailureReason,
		UpdatedAt:        transition.UpdatedAt,
		CurrentAttemptID: "",
	}, nil
}

func (repository failingAttemptStartRepository) FindTaskRun(string) (task.TaskRun, bool, error) {
	return task.TaskRun{}, false, nil
}

func (repository failingAttemptStartRepository) FindTaskAttempt(string) (task.TaskAttempt, bool, error) {
	return task.TaskAttempt{}, false, nil
}

func (repository failingAttemptStartRepository) ListTaskRun() ([]task.TaskRun, error) {
	return nil, nil
}

func (repository failingAttemptStartRepository) ListTaskRunByPersonID(string) ([]task.TaskRun, error) {
	return nil, nil
}

func (repository failingAttemptStartRepository) DeleteTaskRun(string, []string) (bool, error) {
	return false, nil
}

func (repository failingAttemptStartRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func directToolAction(action string, message string, toolName string, input string) string {
	document := `{"action":"` + action + `"`
	if message != "" {
		document += `,"message":"` + message + `"`
	}
	document += `,"toolName":"` + toolName + `","toolInput":` + input + `}`
	return document
}

func newTurnRunnerTestServices(languageModel llm.LanguageModelProvider, options TurnOptions) turnRunnerTestServices {
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	return turnRunnerTestServices{
		runner:              NewAgentTurnRunner(taskRunService, taskStepService, taskArtifactService, languageModel, options),
		taskRunService:      taskRunService,
		taskEventService:    taskEventService,
		taskStepService:     taskStepService,
		taskArtifactService: taskArtifactService,
	}
}

type sequenceLanguageModel struct {
	modelTier     string
	contents      []string
	textResponses []string
	requests      []llm.StructuredResponseRequest
	textPrompts   []string
}

func recoveryDecisionDocument(nextAction string, userReplyIntent string) string {
	document, errorValue := json.Marshal(map[string]string{
		"nextAction":      nextAction,
		"userReplyIntent": userReplyIntent,
	})
	if errorValue != nil {
		return `{"nextAction":"retry","userReplyIntent":"report the failure"}`
	}
	return string(document)
}

func (languageModel *sequenceLanguageModel) GenerateResponse(_ context.Context, prompt string) (string, error) {
	return languageModel.nextTextResponse(prompt), nil
}

func (languageModel *sequenceLanguageModel) GenerateRecoveryChatCompletion(_ context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	prompt := ""
	if len(request.Messages) > 0 {
		prompt = request.Messages[len(request.Messages)-1].Content
	}
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: languageModel.nextTextResponse(prompt)},
	}, nil
}

func (languageModel *sequenceLanguageModel) nextTextResponse(prompt string) string {
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	index := len(languageModel.textPrompts) - 1
	if index >= len(languageModel.textResponses) {
		return ""
	}
	return languageModel.textResponses[index]
}

func (languageModel *sequenceLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "blueclaw_contract_skill_arbitration" {
		return llm.StructuredResponse{Content: contractSkillArbitrationTestDocument(request.StructuredOutputSchema.Document)}, nil
	}
	if request.StructuredOutputSchema.Name == completionJudgeSchemaName {
		return llm.StructuredResponse{Content: defaultCompletionJudgeTestDocument()}, nil
	}
	languageModel.requests = append(languageModel.requests, request)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{ModelTier: languageModel.modelTier, Content: languageModel.contents[index]}, nil
}

func contractSkillArbitrationTestDocument(schemaDocument string) string {
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if json.Unmarshal([]byte(schemaDocument), &schema) != nil {
		return `{}`
	}
	selectedSkillNames := firstSchemaEnumValue(schema.Properties["selectedSkillNames"].Items.Enum)
	rejectedSkillNames := remainingSchemaEnumValues(schema.Properties["selectedSkillNames"].Items.Enum)
	expectedEvidence := firstSchemaEnumValue(schema.Properties["expectedEvidence"].Items.Enum)
	document, _ := json.Marshal(map[string]any{
		"selectedSkillNames":    selectedSkillNames,
		"rejectedSkillNames":    rejectedSkillNames,
		"requiredNextToolNames": expectedEvidence,
		"expectedEvidence":      expectedEvidence,
		"unmetPreconditions":    []string{},
		"reason":                "test contract arbitration",
	})
	return string(document)
}

func defaultCompletionJudgeTestDocument() string {
	document, _ := json.Marshal(map[string]any{
		"satisfied":   true,
		"missingWork": []string{},
		"reason":      "scripted test default",
	})
	return string(document)
}

func firstSchemaEnumValue(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return []string{values[0]}
}

func remainingSchemaEnumValues(values []string) []string {
	if len(values) < 2 {
		return []string{}
	}
	return append([]string{}, values[1:]...)
}

type structuredFailureTextRecoveryLanguageModel struct {
	reply       string
	errorValue  error
	textPrompts []string
}

func (languageModel *structuredFailureTextRecoveryLanguageModel) GenerateResponse(_ context.Context, prompt string) (string, error) {
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	return languageModel.reply, nil
}

func (languageModel *structuredFailureTextRecoveryLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

func (languageModel *structuredFailureTextRecoveryLanguageModel) GenerateRecoveryChatCompletion(_ context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	prompt := ""
	if len(request.Messages) > 0 {
		prompt = request.Messages[len(request.Messages)-1].Content
	}
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: languageModel.reply},
	}, nil
}

type failingRecoveryLanguageModel struct {
	errorValue error
}

func (languageModel failingRecoveryLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel failingRecoveryLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

type localRecoveryFallbackLanguageModel struct {
	errorValue   error
	localReply   string
	localError   error
	localPrompts []string
	legacyCalls  int
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

func (languageModel *localRecoveryFallbackLanguageModel) GenerateRecoveryResponse(context.Context, string) (string, error) {
	languageModel.legacyCalls++
	return "", languageModel.errorValue
}

func (languageModel *localRecoveryFallbackLanguageModel) GenerateLocalRecoveryResponse(_ context.Context, prompt string) (string, error) {
	languageModel.legacyCalls++
	languageModel.localPrompts = append(languageModel.localPrompts, prompt)
	if languageModel.localError != nil {
		return "", languageModel.localError
	}
	return languageModel.localReply, nil
}

func (languageModel *localRecoveryFallbackLanguageModel) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{}, languageModel.errorValue
}

func (languageModel *localRecoveryFallbackLanguageModel) GenerateLocalRecoveryChatCompletion(_ context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	prompt := ""
	if len(request.Messages) > 0 {
		prompt = request.Messages[len(request.Messages)-1].Content
	}
	languageModel.localPrompts = append(languageModel.localPrompts, prompt)
	if languageModel.localError != nil {
		return llm.ChatCompletionResponse{}, languageModel.localError
	}
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "device",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: languageModel.localReply},
	}, nil
}

func taskEventsContain(taskEvents []task.TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func countTaskEvents(taskEvents []task.TaskEvent, name string) int {
	count := 0
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			count++
		}
	}
	return count
}

func messagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}

func countStructuredRequestsByName(requests []llm.StructuredResponseRequest, name string) int {
	count := 0
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == name {
			count++
		}
	}
	return count
}

func structuredRequestNames(requests []llm.StructuredResponseRequest) []string {
	names := []string{}
	for _, request := range requests {
		names = append(names, request.StructuredOutputSchema.Name)
	}
	return names
}

func assertTerminalNoToolsSchemasExcludeToolActions(t *testing.T, requests []llm.StructuredResponseRequest) {
	t.Helper()
	for _, request := range requests {
		if request.StructuredOutputSchema.Name != "blueclaw_agent_terminal_no_tools_action" {
			continue
		}
		if actionSchemaHasVariant(t, request.StructuredOutputSchema.Document, "continue") {
			t.Fatalf("terminal no-tools schema exposed continue: %s", request.StructuredOutputSchema.Document)
		}
		if actionSchemaHasVariant(t, request.StructuredOutputSchema.Document, "tool.request") {
			t.Fatalf("terminal no-tools schema exposed tool.request: %s", request.StructuredOutputSchema.Document)
		}
	}
}

func structuredRequestsContain(requests []llm.StructuredResponseRequest, fragment string) bool {
	for _, request := range requests {
		if messagesContain(request.Messages, fragment) {
			return true
		}
	}
	return false
}

func actionSchemaVariant(t *testing.T, schemaDocument string, actionName string) map[string]any {
	t.Helper()
	if variant, isFound := findActionSchemaVariant(t, schemaDocument, actionName); isFound {
		return variant
	}
	t.Fatalf("expected action schema variant %q in %s", actionName, schemaDocument)
	return nil
}

func actionSchemaHasVariant(t *testing.T, schemaDocument string, actionName string) bool {
	t.Helper()
	_, isFound := findActionSchemaVariant(t, schemaDocument, actionName)
	return isFound
}

// findActionSchemaVariant supports two schema shapes: a root-level oneOf of
// discriminated branches (the mid-turn action-loop schema, still built by
// buildActionSchemaFromToolDefinitions) and a flat closed object whose "action"
// property enumerates every allowed value (the finalizer and terminal-no-tools
// schemas). For the flat shape, the whole document is treated as the single
// candidate variant.
func findActionSchemaVariant(t *testing.T, schemaDocument string, actionName string) (map[string]any, bool) {
	t.Helper()
	var schema struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
		t.Fatalf("expected action schema json: %v", errorValue)
	}
	variants := schema.OneOf
	if len(variants) == 0 {
		var flatSchema map[string]any
		if errorValue := json.Unmarshal([]byte(schemaDocument), &flatSchema); errorValue != nil {
			t.Fatalf("expected action schema json: %v", errorValue)
		}
		variants = []map[string]any{flatSchema}
	}
	for _, variant := range variants {
		properties := mapFromAny(variant["properties"])
		actionProperty := mapFromAny(properties["action"])
		if containsString(stringSliceFromAny(actionProperty["enum"]), actionName) {
			return variant, true
		}
	}
	return nil, false
}

func mapFromAny(value any) map[string]any {
	typedValue, isMap := value.(map[string]any)
	if !isMap {
		return map[string]any{}
	}
	return typedValue
}

func stringSliceFromAny(value any) []string {
	values, isSlice := value.([]any)
	if !isSlice {
		return nil
	}
	result := []string{}
	for _, item := range values {
		stringValue, isString := item.(string)
		if isString {
			result = append(result, stringValue)
		}
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func countStringOccurrences(values []string, fragment string) int {
	count := 0
	for _, value := range values {
		if strings.Contains(value, fragment) {
			count++
		}
	}
	return count
}

func writeAgentTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func finishMessageDocument(reply string) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[]}`
}

func noToolFallbackFinishMessageDocument(reply string) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[],"failureResolution":"no_tool_fallback"}`
}

func failureReportDocument(reason string, toolName string, inputSummary string, errorCode string, failureStage string, message string) string {
	document, errorValue := json.Marshal(map[string]any{
		"action":            "fail",
		"reason":            reason,
		"goalStatus":        "blocked",
		"goalSatisfied":     false,
		"failureResolution": failureResolutionFailureReport,
		"usedFailureFacts": failureReportFacts{
			Attempts: []failureReportAttempt{{
				ToolName:     toolName,
				InputSummary: inputSummary,
				ErrorCode:    errorCode,
				FailureStage: failureStage,
				Message:      message,
			}},
			BudgetState: "failure_report_required",
		},
	})
	if errorValue != nil {
		return `{"action":"fail","reason":"failed","goalStatus":"blocked","goalSatisfied":false,"failureResolution":"failure_report","usedFailureFacts":{"attempts":[],"budgetState":"failure_report_required"}}`
	}
	return string(document)
}

func exhaustedRecoveryBudgetForTest() RecoveryBudget {
	return RecoveryBudget{CorrectedRetry: -1, AlternateRoute: -1, AdjacentTool: -1, NoToolFallback: -1}
}

func terminalNoToolRecoveryBudgetForTest() RecoveryBudget {
	return RecoveryBudget{CorrectedRetry: 0, AlternateRoute: 0, AdjacentTool: 0, NoToolFallback: 1}
}

func finishMessageWithEvidence(reply string, observationID string, toolName string, attachmentIndex int) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":` + strconv.Quote(observationID) + `,"toolName":` + strconv.Quote(toolName) + `,"attachmentIndex":` + strconv.Itoa(attachmentIndex) + `}],"qualityReview":[]}`
}

func TestApprovalObservationUserFacingMessageReadsConfirmQuestion(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"ask.confirm question", `{"kind":"confirm","question":"테스트 님에게 DM 보낼까요?","status":"waiting_approval"}`, "테스트 님에게 DM 보낼까요?"},
		{"userFacingMessage preferred", `{"userFacingMessage":"보낼까요?","question":"q"}`, "보낼까요?"},
		{"message fallback", `{"message":"확인할까요?"}`, "확인할까요?"},
		{"no prompt", `{"kind":"confirm","status":"waiting_approval"}`, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observation := turnObservation{Output: ToolOutput{Content: testCase.content}}
			if got := approvalObservationUserFacingMessage(observation); got != testCase.want {
				t.Fatalf("approvalObservationUserFacingMessage = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestTerminalStructuredRequestsCarryMaxTokensCap(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		noToolFallbackFinishMessageDocument("done"),
		noToolFallbackFinishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: defaultRecoveryBudget()})
	request := AgentTurnRequest{ToolSet: newTestToolSet(nil)}
	services.runner.finalizerAction(context.Background(), request, nil, ExecutionState{})
	services.runner.terminalNoToolsAction(context.Background(), request, nil, ExecutionState{}, "")
	capturedRequests := append([]llm.StructuredResponseRequest{}, languageModel.requests...)
	capturedRequests = append(capturedRequests, completionJudgeRequest(request, nil, nil, turnActionDocument{}))
	if len(capturedRequests) != 3 {
		t.Fatalf("expected finalizer, terminal, and judge requests, got %+v", structuredRequestNames(capturedRequests))
	}
	for _, structuredRequest := range capturedRequests {
		if structuredRequest.GenerationOptions.MaxTokens == nil || *structuredRequest.GenerationOptions.MaxTokens != terminalStructuredMaxTokens {
			t.Fatalf("expected %s request to cap maxTokens at %d", structuredRequest.StructuredOutputSchema.Name, terminalStructuredMaxTokens)
		}
	}
}
