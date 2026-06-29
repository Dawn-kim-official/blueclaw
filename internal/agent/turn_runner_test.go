package agent

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

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
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
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"alpha","toolInput":{"value":"one"}}`,
		`{"action":"continue","toolName":"beta","toolInput":{"value":"two"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"alpha", "beta"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "beta"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("beta result"), nil
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
		t.Fatalf("expected three model calls, got %d", len(languageModel.requests))
	}
	llmCallEventCount := 0
	for _, taskEvent := range services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID) {
		if taskEvent.Name == "llm.call" {
			llmCallEventCount++
		}
	}
	if llmCallEventCount != 3 {
		t.Fatalf("expected three llm.call ledger events, got %d", llmCallEventCount)
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
	toolRegistry := newTestToolSet([]string{"alpha"})
	wasToolCalled := false
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		wasToolCalled = true
		return ToolSuccess("alpha result"), nil
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

func TestAgentTurnRunnerSuppressesCheckpointForSimpleTask(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"일정을 확인하겠습니다.","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("등록했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"alpha"})
	wasToolCalled := false
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		wasToolCalled = true
		return ToolSuccess("alpha result"), nil
	})
	checkpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "일정 등록해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		TaskComplexity:    TaskComplexitySimple,
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.checkpoint.skipped", "task_complexity_simple") {
		t.Fatal("expected simple task checkpoint skip event")
	}
}

func TestAgentTurnRunnerRunsToolWhenCheckpointFails(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","message":"작업 중입니다.","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"alpha"})
	wasToolCalled := false
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		wasToolCalled = true
		return ToolSuccess("alpha result"), nil
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
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
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

func TestAgentTurnRunnerSelectToolsPinsHiddenTool(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"need site creation"}`,
		`{"action":"continue","toolName":"site.create","toolInput":{"slug":"demo"}}`,
		finishMessageWithEvidence("created", "obs-002", "site.create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	siteCreateCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{
		Name:        "site.create",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		siteCreateCallCount++
		return ToolResult{Output: ToolOutput{Content: `{"siteID":"site-1"}`}, Attachments: []FileAttachment{{DevicePath: "site://site-1", Filename: "site.json"}}}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "create website",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if siteCreateCallCount != 1 {
		t.Fatalf("expected site.create to be invoked once, got %d", siteCreateCallCount)
	}
	if result.FinishMessage != "created" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_palette.applied", "site.create") {
		t.Fatal("expected tool palette apply event")
	}
}

func TestAgentTurnRunnerSelectToolsSuggestsCandidateForUnknownTool(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"tool.request","toolNames":["image.analyze"],"skillNames":[],"reason":"need image analysis"}`,
		`{"action":"tool.request","toolNames":["image.read"],"skillNames":[],"reason":"use the matching registered image tool"}`,
		`{"action":"continue","toolName":"image.read","toolInput":{"materialID":"mattermost:file-1"}}`,
		finishMessageWithEvidence("image described", "obs-003", "image.read", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := NewToolSet([]string{"skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	imageReadCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{
		Name:        "image.read",
		Description: "Read and analyze an image attachment by materialID.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"materialID":{"type":"string"}},"required":["materialID"]}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		imageReadCallCount++
		return ToolSuccess(`{"description":"mascot image"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "다시 이미지 봐봐",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if imageReadCallCount != 1 {
		t.Fatalf("expected image.read to be invoked once, got %d", imageReadCallCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.tool_palette.failed", "image.analyze") || !taskEventsContain(events, "agent.tool_palette.failed", "image.read") {
		t.Fatal("expected failed palette request to include image.read candidate")
	}
	if !taskEventsContain(events, "agent.tool_palette.applied", "image.read") {
		t.Fatal("expected exact image.read request to apply")
	}
}

func TestAgentTurnRunnerSelectToolsPinsSkillInstructionsAndExplicitTools(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":["site-prototype"],"reason":"need site workflow"}`,
		`{"action":"continue","toolName":"site.create","toolInput":{"slug":"demo"},"nextStepPlan":{"objective":"finish after creating the site","expectedTools":[],"expectedNextResults":["site created"],"doneCriteria":["site created"],"risk":"none","workingSetReason":"site.create should satisfy this test"}}`,
		finishMessageWithEvidence("created", "obs-002", "site.create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Output: ToolOutput{Content: `{"siteID":"site-1"}`}, Attachments: []FileAttachment{{DevicePath: "site://site-1", Filename: "site.json"}}}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "make site",
		WorkKinds:         []string{WorkKindSitePrototype},
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		AvailableSkills: []SkillInstruction{{
			Name:         "site-prototype",
			Prompt:       "SITE WORKFLOW BODY",
			AllowedTools: []string{"site.create"},
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "created" {
		t.Fatalf("expected final reply, got %q events=%+v userNotice=%q status=%s", result.FinishMessage, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), result.UserNotice, result.TaskRun.Status)
	}
	if len(languageModel.requests) < 2 || !strings.Contains(joinMessageContent(languageModel.requests[1].Messages), "SITE WORKFLOW BODY") {
		t.Fatalf("expected pinned skill instructions in next model request")
	}
}

func TestAgentTurnRunnerSteersRepeatedSelectToolsTowardConcreteToolUse(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"need site creation"}`,
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"still need site creation"}`,
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"still selecting"}`,
		`{"action":"continue","toolName":"site.create","toolInput":{"slug":"demo"}}`,
		finishMessageWithEvidence("created", "obs-005", "site.create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 10})
	toolRegistry := NewToolSet([]string{"skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"siteID":"site-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "create website",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover cleanly: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if countStructuredRequestsByName(languageModel.requests, "blueclaw_agent_turn_action") != 5 {
		t.Fatalf("expected request_tools loop to receive one steering turn, got %+v", structuredRequestNames(languageModel.requests))
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.stall_exit_directive", "Take one of two exits now") {
		t.Fatalf("expected stall exit directive before concrete tool use, got %+v", taskEvents)
	}
	if taskEventsContain(taskEvents, "agent.no_progress_loop_stopped", "") {
		t.Fatalf("expected no no-progress block after concrete tool use, got %+v", taskEvents)
	}
	if taskEventsContain(taskEvents, "max_iterations", "") {
		t.Fatal("expected request_tools loop breaker before max_iterations")
	}
}

func TestAgentTurnRunnerSelectToolsWithExhaustedFailureDebtRunsTerminalNoTools(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+2/4"}}`,
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"try another tool"}`,
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"still trying"}`,
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"still selecting"}`,
		`{"action":"tool.request","toolNames":["site.create"],"skillNames":[],"reason":"terminal fallback should run after this"}`,
		noToolFallbackFinishMessageDocument("I can answer from the failure context."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 10, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := NewToolSet([]string{"math.calculate", "skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"siteID":"site-1"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "calculate it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal no-tools result: %v", errorValue)
	}
	if result.FinishMessage != "I can answer from the failure context." {
		t.Fatalf("expected terminal no-tools finish, got %q", result.FinishMessage)
	}
	if countStructuredRequestsByName(languageModel.requests, "blueclaw_agent_terminal_no_tools_action") != 1 {
		t.Fatalf("expected one terminal no-tools request, got %+v", structuredRequestNames(languageModel.requests))
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "max_iterations", "") {
		t.Fatal("expected terminal no-tools route before max_iterations")
	}
}

func TestAgentTurnRunnerAuditsSelectedSkillDecisions(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"terminal.run"})
	for _, toolName := range []string{"terminal.run", "site.create"} {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:  "person-1",
		ConversationID:     "conversation-1",
		Prompt:             "피피티 만들어줘",
		ToolSet:            toolRegistry,
		PinnedToolNames:    toolRegistry.ListToolNames(),
		AvailableSkills:    []SkillInstruction{{Name: "simple-slides", AllowedTools: []string{"terminal.run", "site.create"}}},
		InstructionPrompt:  "Available skill index.\n\nSelected skill instructions:\nGenerate PPTX with Marp.",
		InstructionSources: []InstructionSource{{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides", SHA256: "abc"}},
		SkillDecisions: []SkillSelectionDecision{{
			Name:   "simple-slides",
			Status: "selected",
			Reason: "embedding_similarity",
			Source: InstructionSource{Path: "skills/simple-slides/SKILL.md", SkillName: "simple-slides", SHA256: "abc"},
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected turn result: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "simple-slides") {
		t.Fatal("expected selected skill in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "embedding_similarity") {
		t.Fatal("expected selected skill reason in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "skills/simple-slides/SKILL.md") {
		t.Fatal("expected selected skill source in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "registeredToolCount") ||
		!taskEventsContain(taskEvents, "agent.instructions_loaded", "hiddenDescribedToolNames") ||
		!taskEventsContain(taskEvents, "agent.instructions_loaded", "site.create") {
		t.Fatal("expected tool visibility debug fields in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "selectedSkillAllowedTools") {
		t.Fatal("expected selected skill allowed tools in instructions event")
	}
}

func TestActionSchemaRequiresFailureResolutionWhenFailureDebtActive(t *testing.T) {
	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{ToolSet: newTestToolSet(nil)},
		Options: TurnOptions{RecoveryBudget: defaultRecoveryBudget()},
		Observations: []turnObservation{{
			ObservationID:      "obs-001",
			Action:             "continue",
			Tool:               "math.calculate",
			Output:             ToolOutput{Content: "bc: command not found"},
			Failure:            &ToolFailure{Kind: FailureExternalService, Code: FailureCodes.OperationFailed.String(), Stage: "bc_execution", UserSafeSummary: "bc: command not found"},
			ToolInputKey:       "math.calculate\x00{\"expression\":\"1+2/4\"}",
			AttemptFingerprint: "math.calculate\x00{\"expression\":\"1+2/4\"}\x00operation_failed",
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
	if !booleanEnumHasOnly(finishGoalSatisfied["enum"], true) {
		t.Fatalf("finish schema must require goalSatisfied=true, got %+v", finishGoalSatisfied)
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
	if !booleanEnumHasOnly(failGoalSatisfied["enum"], false) {
		t.Fatalf("fail schema must require goalSatisfied=false, got %+v", failGoalSatisfied)
	}
	usedFailureFacts := mapFromAny(failProperties["usedFailureFacts"])
	attempts := mapFromAny(mapFromAny(usedFailureFacts["properties"])["attempts"])
	if attempts["type"] != "array" {
		t.Fatalf("expected usedFailureFacts.attempts array schema, got %+v", attempts)
	}
}

func TestActionSchemaHidesFailWhileRecoveryBudgetRemains(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"site.publish", "file.write"})
	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{ToolSet: toolRegistry},
		Options: TurnOptions{RecoveryBudget: defaultRecoveryBudget()},
		Observations: []turnObservation{{
			ObservationID:      "obs-001",
			Action:             "continue",
			Tool:               "site.publish",
			Output:             ToolOutput{Content: "starter scaffold remains"},
			Failure:            &ToolFailure{Kind: FailureInvalidInput, Code: FailureCodes.InvalidInput.String(), Stage: "site_publish", UserSafeSummary: "starter scaffold remains"},
			ToolInputKey:       "site.publish\x00{\"siteID\":\"site-1\"}",
			AttemptFingerprint: "site.publish\x00{\"siteID\":\"site-1\"}\x00invalid_input",
		}},
	})
	schemaDocument := request.StructuredOutputSchema.Document
	if actionSchemaHasVariant(t, schemaDocument, "fail") {
		t.Fatalf("expected fail action to be hidden while recovery budget remains, got %s", schemaDocument)
	}
	if !actionSchemaHasVariant(t, schemaDocument, "finish") {
		t.Fatalf("expected finish fallback to remain available, got %s", schemaDocument)
	}
	if !actionSchemaHasVariant(t, schemaDocument, "continue") {
		t.Fatalf("expected recovery tool actions to remain available, got %s", schemaDocument)
	}
}

func TestAgentTurnRunnerBudgetExhaustedContinueTriggersSingleTerminalNoToolsCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+2/4"}}`,
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"2+2"}}`,
		noToolFallbackFinishMessageDocument("I can still answer from the available context."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "calculate it",
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
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+2/4"}}`,
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"2+2"}}`,
		noToolFallbackFinishMessageDocument("The available context is enough to answer without another tool."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "calculate it",
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
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+2/4"}}`,
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"2+2"}}`,
		failureReportDocument("Calculator execution is blocked because bc_execution returned operation_failed.", "math.calculate", "1+2/4", FailureCodes.OperationFailed.String(), "bc_execution", "bc: command not found"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "calculate it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected terminal failure report result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.UserNotice, "bc_execution") {
		t.Fatalf("expected failure report reason to be delivered, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_report_facts_used", "bc_execution") {
		t.Fatal("expected used failure facts event")
	}
	assertTerminalNoToolsSchemasExcludeToolActions(t, languageModel.requests)
}

func TestAgentTurnRunnerTerminalNoToolsRepairsInvalidOutputWithoutReopeningTools(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+2/4"}}`,
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"2+2"}}`,
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"3+3"}}`,
		noToolFallbackFinishMessageDocument("I repaired the terminal answer without another tool."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, RecoveryBudget: terminalNoToolRecoveryBudgetForTest()})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "calculate it",
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

func TestAgentTurnRunnerAutoCompletesSimpleBrowserOpen(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.open","toolInput":{"url":"https://www.google.com"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"url":"https://www.google.com/"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "브라우저 열어줘.",
		TaskComplexity:    TaskComplexitySimple,
		WorkKinds:         []string{WorkKindBrowserSession},
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "완료") && !strings.Contains(result.FinishMessage, "열") {
		t.Fatalf("expected browser-open completion reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected no extra model calls after browser.open, got %d", len(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "evidenceCount") {
		t.Fatal("expected completion state finalization event")
	}
}

func TestAgentTurnRunnerRejectsBrowserFollowUpReplyWithoutToolEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("말로만 답변"),
		`{"action":"continue","toolName":"browser.open","toolInput":{"url":"https://console.cloud.google.com/"}}`,
		finishMessageWithEvidence("열었습니다", "obs-002", "browser.open", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"url":"https://console.cloud.google.com/"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "다시 열어봐",
		WorkKinds:         []string{WorkKindBrowserSession},
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
		ToolSet:         toolRegistry,
		PinnedToolNames: toolRegistry.ListToolNames(),
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
	for _, toolName := range []string{"browser.open", "browser.click", "browser.fill", "browser.select", "browser.wait"} {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
			if fieldName == "required" || fieldName == "additionalProperties" || fieldName == "maxItems" {
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
		`{"action":"continue","toolName":"site.create","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"build the created site","expectedTools":["site.build","artifact.review"],"doneCriteria":["site build succeeds"],"risk":"draft may be incomplete","workingSetReason":"creation must lead into build and review"}}`,
		`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"review the built artifact","expectedTools":["artifact.review","site.publish"],"doneCriteria":["review passes"],"risk":"visual issues may block publish","workingSetReason":"build output needs review before publish"}}`,
		`{"action":"continue","toolName":"artifact.review","toolInput":{"path":"home/sites/site-1/app/dist/index.html"},"nextStepPlan":{"objective":"publish reviewed site","expectedTools":["site.publish","site.status"],"doneCriteria":["publish succeeds"],"risk":"publish may reject stale build","workingSetReason":"review evidence allows publish"}}`,
		`{"action":"continue","toolName":"site.publish","toolInput":{"siteID":"site-1","message":"Publish portfolio"},"nextStepPlan":{"objective":"confirm final status","expectedTools":["site.status"],"doneCriteria":["status shows published URL"],"risk":"status may not reflect latest version","workingSetReason":"final status is required evidence"}}`,
		`{"action":"continue","toolName":"site.status","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish with status evidence","expectedTools":[],"doneCriteria":["finish with published URL"],"risk":"none","workingSetReason":"all required evidence has been collected"}}`,
		`{"action":"finish","message":"같은 URL에 배포했습니다: https://portfolio.example","replyParts":[{"type":"text","text":"같은 URL에 배포했습니다: https://portfolio.example"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"site.build"},{"observationID":"obs-003","toolName":"artifact.review"},{"observationID":"obs-004","toolName":"site.publish"},{"observationID":"obs-005","toolName":"site.status"}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 8})
	toolRegistry := newTestToolSet([]string{"site.status", "site.create", "site.build", "artifact.review", "site.publish"})
	toolCalls := []string{}
	hasBuildQuality := false
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.status"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.status")
		return ToolSuccess(`{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example","revisionCount":1}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.create")
		return ToolSuccess(`{"siteID":"site-1","sourceWorkspacePath":"home/sites/site-1","appWorkspacePath":"home/sites/site-1/app","publishedURL":"https://portfolio.example"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.build")
		hasBuildQuality = true
		return ToolSuccess(`{"qualityPath":"home/sites/site-1/.internkim/build-quality.json","distPath":"home/sites/site-1/app/dist"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "artifact.review"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "artifact.review")
		return ToolSuccess(`{"status":"passed","blockingIssueCount":0}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.publish")
		if !hasBuildQuality {
			return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "site_publish", "missing build-quality.json"), nil
		}
		return ToolSuccess(`{"siteID":"site-1","publishedURL":"https://portfolio.example","currentVersionID":"rev-2"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 만들고 배포해줘",
		WorkKinds:             []string{WorkKindSitePrototype},
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.status", "site.build", "artifact.review", "site.publish"},
		AvailableSkills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.status", "site.create", "site.build", "artifact.review", "site.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	})
	if errorValue != nil {
		t.Fatalf("expected site loop to succeed: %v", errorValue)
	}
	expectedCalls := []string{"site.create", "site.build", "artifact.review", "site.publish", "site.status"}
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
		"site.status",
		"site.create",
		"file.write",
		"terminal.run",
		"site.build",
		"artifact.review",
		"site.publish",
		"file.attach",
	})
	request := AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "김인턴 너의 개인 홈페이지 하나 만들어서 배포해봐.",
		ToolSet:               toolRegistry,
		PinnedToolNames:       []string{"site.status", "site.create", "file.write", "site.build", "artifact.review", "site.publish"},
		RequiredEvidenceTools: []string{"site.status", "site.build", "site.publish", "file.attach"},
		AvailableSkills: []SkillInstruction{{
			Name: "site-prototype",
			AllowedTools: []string{
				"site.status",
				"site.create",
				"file.write",
				"terminal.run",
				"site.build",
				"artifact.review",
				"site.publish",
				"file.attach",
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "김인턴 너의 개인 홈페이지 하나 만들어서 배포해봐.",
			Status:              ActiveGoalStatusActive,
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"site.status", "site.build", "site.publish", "file.attach"},
				ArtifactRequirement:   ArtifactRequirementRequired,
			},
		},
	}

	stepRequest := services.runner.requestForStep(context.Background(), request, agentTaskState{Request: request})
	for _, toolName := range []string{"site.status", "site.create", "file.write", "site.build", "artifact.review", "site.publish"} {
		if !stepRequest.ToolSet.CanExpose(toolName) {
			t.Fatalf("expected initial site working set to expose %s, got %+v", toolName, stepRequest.ToolExposure.ExposedToolIDs)
		}
	}
}

func TestAgentTurnRunnerReselectsToolsAfterRejectedSiteFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"site.create","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"build the draft before finishing","expectedTools":["site.build"],"doneCriteria":["build evidence exists"],"risk":"draft creation alone is not completion","workingSetReason":"site.build is required evidence"}}`,
			`{"action":"finish","message":"초안이 만들어졌습니다.","replyParts":[{"type":"text","text":"초안이 만들어졌습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.create"}]}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish after build evidence","expectedTools":[],"doneCriteria":["build observation exists"],"risk":"none","workingSetReason":"required evidence has been collected"}}`,
			finishMessageWithEvidence("빌드까지 완료했습니다.", "obs-003", "site.build", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 4})
	toolRegistry := newTestToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "site.create", "site.build"})
	toolCalls := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.create")
		return ToolSuccess(`{"siteID":"site-1","status":"draft"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.build")
		return ToolSuccess(`{"siteID":"site-1","distPath":"home/sites/site-1/app/dist"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 만들고 배포해줘",
		WorkKinds:             []string{WorkKindSitePrototype},
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.build"},
		AvailableSkills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.create", "site.build"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	})
	if errorValue != nil {
		t.Fatalf("expected rejected finish to recover into build: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Join(toolCalls, ",") != "site.create,site.build" {
		t.Fatalf("expected create then build, got %+v", toolCalls)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "site.build") {
		t.Fatal("expected early finish to be rejected by completion gate")
	}
	if !taskEventsContain(events, "agent.tool_palette.built", "site.build") {
		t.Fatal("expected build tool to be exposed after rejected finish")
	}
	if !taskEventsContain(events, "agent.tool_palette.built", "deterministic") {
		t.Fatal("expected deterministic per-iteration tool exposure without selector LLM calls")
	}
}

func TestAgentTurnRunnerRejectsFailAfterSiteSourceWriteBeforeBuildPublish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"/workspace/sites/site-1/draft/app/src/App.tsx","content":"export default function App(){return <main>Pretty</main>}"}}`,
			`{"action":"fail","reason":"cannot continue","goalStatus":"blocked","goalSatisfied":false,"remainingWork":"build and publish still needed"}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
			`{"action":"continue","toolName":"site.publish","toolInput":{"siteID":"site-1"}}`,
			finishMessageWithEvidence("배포했습니다: https://pretty.example", "obs-004", "site.publish", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 8})
	toolRegistry := newTestToolSet([]string{"file.write", "site.build", "site.publish"})
	toolCalls := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "file.write")
		return ToolSuccess(`{"path":"/workspace/sites/site-1/draft/app/src/App.tsx"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.build")
		return ToolSuccess(`{"siteID":"site-1","distPath":"/workspace/sites/site-1/draft/app/dist"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.publish")
		return ToolSuccess(`{"siteID":"site-1","publishedURL":"https://pretty.example"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "사이트 더 예쁘게 수정하고 배포해줘",
		WorkKinds:             []string{WorkKindSitePrototype},
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site.publish"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.publish"},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected recoverable fail to continue: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Join(toolCalls, ",") != "file.write,site.build,site.publish" {
		t.Fatalf("expected write then build/publish, got %+v", toolCalls)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recoverable_fail_rejected", "site.build") {
		t.Fatal("expected recoverable fail rejection to suggest build")
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

func TestAgentTurnRunnerDoesNotPauseBeforeRequiresApprovalToolInvoke(t *testing.T) {
	heldInput := `{"eventID":"event-1"}`
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calendar.delete","toolInput":` + heldInput + `}`,
		`{"action":"finish","message":"일정을 삭제했습니다.","replyParts":[{"type":"text","text":"일정을 삭제했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"calendar.delete"}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"calendar.delete"})
	invokedInputs := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "calendar.delete", RequiresApproval: true}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		return ToolSuccess(`{"eventID":"event-1","status":"deleted"}`), nil
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
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(invokedInputs) != 1 || invokedInputs[0] != heldInput {
		t.Fatalf("expected tool to be invoked once with original input, got %+v", invokedInputs)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(events, "approval.pending_call", "") {
		t.Fatalf("requiresApproval descriptor should not create pre-approval hold, events=%+v", events)
	}
	if !taskEventsContain(events, "tool.calendar.delete.requested", heldInput) {
		t.Fatalf("expected tool request event, events=%+v", events)
	}
	if !taskEventsContain(events, "tool.calendar.delete.result", "deleted") {
		t.Fatalf("expected tool result event, events=%+v", events)
	}
}

func TestAgentTurnRunnerApprovalRequiredPausesAndExecutesHeldCall(t *testing.T) {
	heldInput := `{"deliveryTarget":{"type":"directMessage","personHint":"우경"},"message":"오늘 오후 3시에 확인하자"}`
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":` + heldInput + `}`,
		`{"action":"finish","message":"우경이에게 DM을 보냈습니다.","replyParts":[{"type":"text","text":"우경이에게 DM을 보냈습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"message.send"}],"qualityReview":[]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"message.send"})
	invokedInputs := []string{}
	wasExecutedBeforeSecondModel := false
	toolRegistry.RegisterTool(ToolDefinition{Name: "message.send"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		if len(invokedInputs) == 1 {
			return ToolResult{
				Output: ToolOutput{Content: "requires approval"},
				Failure: &ToolFailure{
					Kind:            FailureExternalService,
					Code:            "approval_required",
					Stage:           "authorization",
					UserSafeSummary: "requires approval",
				},
			}, nil
		}
		wasExecutedBeforeSecondModel = len(languageModel.requests) == 1
		return ToolSuccess(`{"messageID":"message-1","deliveryStatus":"sent"}`), nil
	})

	firstResult, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "우경에게 DM 보내줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"message.send"},
		WorkKinds:         []string{WorkKindExternalSend},
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected first turn to pause: %v", errorValue)
	}
	if firstResult.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval task, got %s events=%+v", firstResult.TaskRun.Status, services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID))
	}
	if !strings.Contains(firstResult.UserNotice, "우경") || !strings.Contains(firstResult.UserNotice, "오늘 오후 3시에 확인하자") {
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
		WorkKinds:              []string{WorkKindExternalSend},
		WorkspaceRootPath:      t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected approval continuation to complete: %v", errorValue)
	}
	if secondResult.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", secondResult.TaskRun.Status, services.taskEventService.ListTaskEvent(firstResult.TaskRun.TaskRunID))
	}
	if len(invokedInputs) != 2 {
		t.Fatalf("expected original attempt and deterministic retry, got %d", len(invokedInputs))
	}
	if invokedInputs[0] != heldInput || invokedInputs[1] != heldInput {
		t.Fatalf("expected exact held input to be reused, got %+v", invokedInputs)
	}
	if !wasExecutedBeforeSecondModel {
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
		`{"action":"continue","toolName":"browser.screenshot","toolInput":{}}`,
		`{"action":"continue","toolName":"browser.screenshot","toolInput":{}}`,
		finishMessageWithEvidence("캡처했습니다.", "obs-003", "browser.screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 2})
	toolRegistry := newTestToolSet([]string{"browser.screenshot"})
	screenshotIndex := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.finalizer_action", "obs-003") {
		t.Fatal("expected finalizer action with completion evidence")
	}
}

func TestAgentTurnRunnerDoesNotDeliverAttachmentsWhenFinalizerFails(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.screenshot","toolInput":{}}`,
		`{"action":"fail","reason":"not complete"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"browser.screenshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "스크린샷 줘",
		WorkKinds:         []string{WorkKindBrowserSession},
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
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.pick"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("again"), nil
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
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/index.html","content":"one"}}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
			finishMessageDocument("continued after escalation"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		EffortLevel:       EffortLevelQuick,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "site.build"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/app/index.html"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"status":"built"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build the site",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "site.build"},
	})

	if errorValue != nil {
		t.Fatalf("expected escalation run, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task after escalation, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"newEffortLevel":"standard"`) {
		t.Fatalf("expected budget escalation event, got %+v", taskEvents)
	}
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"qualifyingEventIDs":["obs-001","obs-003"]`) {
		t.Fatalf("expected qualifying event IDs, got %+v", taskEvents)
	}
}

func TestAgentTurnRunnerDoesNotEscalateIterationLimitForInspectionOnlyProgress(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.read","toolInput":{"path":"tmp/app/index.html"}}`,
			`{"action":"continue","toolName":"site.status","toolInput":{"siteID":"site-1"}}`,
		},
		textResponses: []string{"progress saved"},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		EffortLevel:       EffortLevelQuick,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.read", "site.status"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.read"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/app/index.html","content":"one"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.status"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"status":"draft"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "inspect the site",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.read", "site.status"},
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
			`{"action":"continue","toolName":"file.patch","toolInput":{"path":"tmp/app/a","patch":"two"}}`,
			`{"action":"continue","toolName":"file.edit","toolInput":{"path":"tmp/app/a","oldText":"one","newText":"two"}}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
			finishMessageDocument("done"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		EffortLevel:       EffortLevelQuick,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "file.patch", "file.edit", "site.build"})
	for _, toolName := range []string{"file.write", "file.patch", "file.edit", "site.build"} {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess(`{"ok":true}`), nil
		})
	}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "keep building",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "file.patch", "file.edit", "site.build"},
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
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"previousEffortLevel":"quick"`) ||
		!taskEventsContain(taskEvents, "agent.budget_escalated", `"newEffortLevel":"standard"`) {
		t.Fatalf("expected quick to standard escalation, got %+v", taskEvents)
	}
}

func TestAgentTurnRunnerCheckpointsAtExtendedIterationCeiling(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/a","content":"one"}}`,
			`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
		},
		textResponses: []string{"progress saved"},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		EffortLevel:       EffortLevelExtended,
		MaxIterationCount: 2,
		MaxToolCallCount:  10,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "site.build"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/app/a"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"status":"built"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "finish extended work",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "site.build"},
	})

	if errorValue != nil {
		t.Fatalf("expected checkpoint result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked checkpoint task, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, "agent.budget_escalated", "") {
		t.Fatal("did not expect escalation past extended")
	}
	if !taskEventsContain(taskEvents, "agent.limit_checkpoint", `"qualifyingEventIDs":["obs-001","obs-003"]`) {
		t.Fatalf("expected limit checkpoint event, got %+v", taskEvents)
	}
}

func TestAgentTurnRunnerStopsWhenToolEffortIsExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{"도구 호출이 더 진행되기 전에 멈췄습니다. 확인된 내용까지만 바탕으로 다시 이어갈 수 있어요."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3, MaxToolCallCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("again"), nil
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
	contents              []string
	resultVerifications   []string
	contractVerifications []string
	textResponses         []string
	requests              []llm.StructuredResponseRequest
	verificationRequests  []llm.StructuredResponseRequest
	contractRequests      []llm.StructuredResponseRequest
	textPrompts           []string
}

func recoveryDecisionDocument(whatFailed string, whatWasKnown string, nextAction string, userReplyIntent string) string {
	document, errorValue := json.Marshal(map[string]string{
		"whatFailed":      whatFailed,
		"whatWasKnown":    whatWasKnown,
		"nextAction":      nextAction,
		"userReplyIntent": userReplyIntent,
	})
	if errorValue != nil {
		return `{"whatFailed":"failed","whatWasKnown":"unknown","nextAction":"retry","userReplyIntent":"report the failure"}`
	}
	return string(document)
}

func (languageModel *sequenceLanguageModel) GenerateResponse(_ context.Context, prompt string) (string, error) {
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	index := len(languageModel.textPrompts) - 1
	if index >= len(languageModel.textResponses) {
		return "", nil
	}
	return languageModel.textResponses[index], nil
}

func (languageModel *sequenceLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if strings.TrimSpace(request.StructuredOutputSchema.Name) == "blueclaw_result_verifier" {
		languageModel.verificationRequests = append(languageModel.verificationRequests, request)
		index := len(languageModel.verificationRequests) - 1
		if index < len(languageModel.resultVerifications) {
			return llm.StructuredResponse{Content: languageModel.resultVerifications[index]}, nil
		}
		return llm.StructuredResponse{Content: defaultResultVerificationResponse(request)}, nil
	}
	if strings.TrimSpace(request.StructuredOutputSchema.Name) == "blueclaw_contract_verifier" {
		languageModel.contractRequests = append(languageModel.contractRequests, request)
		index := len(languageModel.contractRequests) - 1
		if index < len(languageModel.contractVerifications) {
			return llm.StructuredResponse{Content: languageModel.contractVerifications[index]}, nil
		}
		return llm.StructuredResponse{Content: `{"satisfied":true,"reason":"test default","missingDescription":"","suggestedNextTools":[]}`}, nil
	}
	languageModel.requests = append(languageModel.requests, request)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{Content: languageModel.contents[index]}, nil
}

func defaultResultVerificationResponse(request llm.StructuredResponseRequest) string {
	expectedResults := expectedResultsFromVerifierRequest(request)
	results := []map[string]any{}
	for _, expectedResult := range expectedResults {
		results = append(results, map[string]any{
			"id":                  expectedResult.ID,
			"status":              "satisfied",
			"reason":              "test default",
			"citedObservationIDs": []string{},
			"missingDescription":  "",
			"suggestedNextTools":  []string{},
		})
	}
	document, errorValue := json.Marshal(map[string]any{
		"overallStatus": "satisfied",
		"summary":       "test default",
		"results":       results,
	})
	if errorValue != nil {
		return `{"overallStatus":"satisfied","summary":"test default","results":[]}`
	}
	return string(document)
}

func expectedResultsFromVerifierRequest(request llm.StructuredResponseRequest) []ExpectedResult {
	for _, message := range request.Messages {
		content := strings.TrimSpace(message.Content)
		if !strings.HasPrefix(content, "Expected results:\n") {
			continue
		}
		var expectedResults []ExpectedResult
		if json.Unmarshal([]byte(strings.TrimPrefix(content, "Expected results:\n")), &expectedResults) == nil {
			return normalizeExpectedResults(expectedResults)
		}
	}
	return nil
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
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

func (languageModel *localRecoveryFallbackLanguageModel) GenerateRecoveryResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel *localRecoveryFallbackLanguageModel) GenerateLocalRecoveryResponse(_ context.Context, prompt string) (string, error) {
	languageModel.localPrompts = append(languageModel.localPrompts, prompt)
	if languageModel.localError != nil {
		return "", languageModel.localError
	}
	return languageModel.localReply, nil
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

func findActionSchemaVariant(t *testing.T, schemaDocument string, actionName string) (map[string]any, bool) {
	t.Helper()
	var schema struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
		t.Fatalf("expected action schema json: %v", errorValue)
	}
	for _, variant := range schema.OneOf {
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

func booleanEnumHasOnly(value any, expected bool) bool {
	values, isSlice := value.([]any)
	if !isSlice || len(values) != 1 {
		return false
	}
	boolValue, isBool := values[0].(bool)
	return isBool && boolValue == expected
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
