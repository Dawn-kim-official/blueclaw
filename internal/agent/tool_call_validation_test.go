package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/task"
)

func TestAgentTurnRunnerRecordsDeniedToolAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"forbidden","toolInput":{}}`,
		noToolFallbackFinishMessageDocument("recovered"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"allowed"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "forbidden"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("should not run"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinishMessage != "recovered" {
		t.Fatalf("expected recovered reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.forbidden.result", FailureCodes.PolicyBlocked.String()) {
		t.Fatal("expected denied tool result event")
	}
}

func TestAgentTurnRunnerRejectsMalformedInputBeforeApproval(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "site.delete", `{"siteID":42}`),
		noToolFallbackFinishMessageDocument("삭제 요청 형식을 확인하지 못했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"site.delete"})
	handlerCallCount := 0
	registerTestTool(toolRegistry, ToolDefinition{
		Name:             "site.delete",
		RequiresApproval: true,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"siteID":{"type":"string"}},
			"required":["siteID"],
			"additionalProperties":false
		}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		handlerCallCount++
		return testToolSuccess("deleted"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "사이트를 삭제해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"site.delete"},
	})
	if errorValue != nil {
		t.Fatalf("expected malformed call recovery: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatal("expected malformed input to stay outside the approval flow")
	}
	if handlerCallCount != 0 {
		t.Fatalf("expected malformed input to stay outside the handler, got %d calls", handlerCallCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.tool_input_malformed", "site.delete") {
		t.Fatalf("expected malformed input event, got %+v", events)
	}
	if taskEventsContain(events, "approval.pending_call", "") {
		t.Fatalf("expected no held approval call, got %+v", events)
	}
}

func TestValidateTerminalToolInputRejectsRegisteredToolNameAsCommand(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"terminal.run"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "site.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("created"), nil
	})
	input := MarshalToolInput(map[string]any{"command": "site.create --slug demo"})

	errorValue := validateTerminalToolInput("terminal.run", input, toolRegistry)

	if errorValue == nil || !isTerminalToolNameError(errorValue) {
		t.Fatalf("expected terminal tool-name error, got %v", errorValue)
	}
}

func TestAgentTurnRunnerRejectsSecondDMSendAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"첫 번째"}}`,
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"두 번째"}}`,
		finishMessageWithEvidence("첫 번째 메시지를 보냈습니다.", "obs-001", "message.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	sendCallCount := 0
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(context.Context, ToolInvocation) (ToolResult, error) {
		sendCallCount++
		return testToolSuccess("sent"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "동하에게 DM 보내줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to complete from first send: %v", errorValue)
	}
	if sendCallCount != 1 {
		t.Fatalf("expected exactly one DM send, got %d", sendCallCount)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.external_send_repeat_rejected", "obs-001") {
		t.Fatal("expected second DM send to be rejected")
	}
}

func TestAgentTurnRunnerAllowsSendToDifferentRecipients(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}`,
		finishMessageWithEvidence("동하와 정국에게 DM을 보냈습니다.", "obs-001", "message.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	sendCallCount := 0
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(context.Context, ToolInvocation) (ToolResult, error) {
		sendCallCount++
		return testToolSuccess("sent"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "동하와 정국에게 DM 보내줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected fan-out turn to complete: %v", errorValue)
	}
	if sendCallCount != 2 {
		t.Fatalf("expected two DM sends to different recipients, got %d", sendCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.external_send_repeat_rejected", "") {
		t.Fatal("send to a different recipient must not be rejected as a repeat")
	}
}

func TestAgentTurnRunnerRejectsMessageSendWithoutExternalSendIntent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"휴게소 가도 돼요."}}`,
		noToolFallbackFinishMessageDocument("휴게소 들러도 괜찮습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(context.Context, ToolInvocation) (ToolResult, error) {
		t.Fatal("message.send must not run without external send intent")
		return ToolResult{}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "휴게소 가야해?",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover from rejected message.send: %v", errorValue)
	}
	if result.FinishMessage != "휴게소 들러도 괜찮습니다." {
		t.Fatalf("expected final reply in current conversation, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.external_send_intent_rejected", "finish.message") {
		t.Fatal("expected external send intent rejection event")
	}
}

func TestAgentTurnRunnerAllowsCurrentThreadSendWithoutExternalSendContract(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"currentThread","message":"메모: 주간 고객지원 체크 완료"}}`,
		finishMessageWithEvidence("이 대화에 메모를 남겼습니다.", "obs-001", "message.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	sendCallCount := 0
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(context.Context, ToolInvocation) (ToolResult, error) {
		sendCallCount++
		return testToolSuccess("sent"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "이 대화에 메모 남겨줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected current-thread send turn to complete: %v", errorValue)
	}
	if sendCallCount != 1 {
		t.Fatalf("expected the current-thread send to run without an external-send contract, got %d calls", sendCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.external_send_intent_rejected", "") {
		t.Fatal("a send into the current conversation must not be rejected as an external send")
	}
}

func TestAgentTurnRunnerRejectsChannelSendWithoutExternalSendIntent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"channel","channelName":"announcements","message":"공지입니다."}}`,
		noToolFallbackFinishMessageDocument("현재 대화에서 답변드립니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(context.Context, ToolInvocation) (ToolResult, error) {
		t.Fatal("channel message.send must not run without external send intent")
		return ToolResult{}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "질문 있어요",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover from rejected channel send: %v", errorValue)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.external_send_intent_rejected", "finish.message") {
		t.Fatal("expected external send intent rejection event for a channel target")
	}
}

func TestAgentTurnRunnerRejectsRepeatedFailedFingerprint(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"message.context","toolInput":{}}`,
		`{"action":"continue","toolName":"message.context","toolInput":{}}`,
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}`,
		failureReportDocument("mattermost still unavailable", "message.send", "정국", FailureCodes.Unavailable.String(), "mattermost_lookup", "temporary user lookup timeout"),
		recoveryDecisionDocument("check Mattermost availability before retrying", "report the failed stage and code"),
	}, textResponses: []string{
		"mattermost_lookup/unavailable 단계에서 Mattermost 조회가 계속 실패해 DM을 보내지 못했습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, RecoveryAttemptLimit: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send", "message.context"})
	callCount := 0
	sendInputs := []string{}
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		callCount++
		sendInputs = append(sendInputs, string(invocation.Input))
		return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "message.context"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("mattermost still unavailable", "mattermost still unavailable", "mattermost_unavailable", "mattermost_lookup", true, true), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterName:         "이동하",
		ConversationID:        "conversation-1",
		Prompt:                "동하에게 DM 보내줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message.send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected exhausted retry failure result: %v", errorValue)
	}
	if countStringOccurrences(sendInputs, `"personHint":"동하"`) != 1 {
		t.Fatalf("expected repeated fingerprint to be rejected before invoke, got inputs %+v", sendInputs)
	}
	if !strings.Contains(result.UserNotice, "mattermost_lookup/unavailable") {
		t.Fatalf("expected final reply to report lookup failure, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failed_fingerprint_rejected", "already failed") {
		t.Fatal("expected failed fingerprint rejection event")
	}
}

func TestAgentTurnRunnerRejectsUnsafeRepeatedExternalSend(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"message.send","toolInput":{"targetType":"directMessage","personHint":"동하","message":"확인 부탁해"}}`,
		failureReportDocument("send failed", "message.send", "동하", FailureCodes.OperationFailed.String(), "message_send", "Mattermost returned 503 after post create"),
		recoveryDecisionDocument("inspect delivery state before retrying", "report the failed stage and avoid duplicate send claims"),
	}, textResponses: []string{
		"message_send/operation_failed 단계에서 전송이 실패했습니다. 중복 전송 위험 때문에 같은 메시지를 다시 보내지는 않았습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 2, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send", "message.context"})
	callCount := 0
	registerTestTool(toolRegistry, testExternalSendToolDefinition("message.send"), func(context.Context, ToolInvocation) (ToolResult, error) {
		callCount++
		return structuredFailureToolResult("Mattermost returned 503 after post create", "Mattermost returned 503 after post create", "send_failed", "message_send", true, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterName:         "이동하",
		ConversationID:        "conversation-1",
		Prompt:                "동하에게 DM 보내줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message.send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected safe failure: %v", errorValue)
	}
	if callCount != 1 {
		t.Fatalf("expected unsafe repeat to be rejected before second send, got %d calls", callCount)
	}
	if !strings.Contains(result.UserNotice, "message_send/operation_failed") {
		t.Fatalf("expected final reply to report send failure, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failed_fingerprint_rejected", "already failed") {
		t.Fatal("expected failed fingerprint rejection event")
	}
}

func TestAgentTurnRunnerRejectsUnavailableToolBeforeInvoke(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calculation_tool","toolInput":{"expression":"1+1"}}`,
		noToolFallbackFinishMessageDocument("I can answer without that unavailable tool."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		t.Fatal("unexpected math.calculate invocation")
		return ToolResult{}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1+1=",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover from unavailable tool: %v", errorValue)
	}
	if result.FinishMessage != "I can answer without that unavailable tool." {
		t.Fatalf("expected final reply after unavailable tool observation, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.calculation_tool.requested", "calculation_tool") {
		t.Fatal("expected unavailable tool request event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.calculation_tool.result", FailureCodes.PolicyBlocked.String()) {
		t.Fatal("expected unavailable tool result event")
	}
}

func TestAgentTurnRunnerRejectsEmptyBrowserPressAfterFill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.fill","toolInput":{"target":"@e5","text":"hello world"}}`,
		`{"action":"continue","toolName":"browser.press","toolInput":{}}`,
		finishMessageWithEvidence("searched", "obs-001", "browser.fill", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	pressCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"browser.fill", "browser.press"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"ok":true}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.press"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		pressCallCount++
		return testToolSuccess(`{"ok":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "입력칸에 hello world라고 입력해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "searched" {
		t.Fatalf("expected searched reply, got %q", result.FinishMessage)
	}
	if pressCallCount != 0 {
		t.Fatalf("expected malformed press input not to invoke tool, got %d calls", pressCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "browser.press") {
		t.Fatal("expected malformed browser press event")
	}
}

func TestAgentTurnRunnerRejectsBrowserFillWithoutRequiredInput(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.snapshot","toolInput":{}}`,
		`{"action":"continue","toolName":"browser.fill","toolInput":{}}`,
		finishMessageWithEvidence("filled", "obs-001", "browser.snapshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	fillCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"browser.snapshot", "browser.fill"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"snapshotText":"- textbox \"Google 검색\" [ref=e5]"}`), nil
	})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.fill"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return testToolSuccess(`{"ok":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "입력칸에 hello world라고 입력해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "filled" {
		t.Fatalf("expected filled reply, got %q", result.FinishMessage)
	}
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "target/ref/selector, text") {
		t.Fatal("expected malformed browser fill event")
	}
}

func TestAgentTurnRunnerRejectsEmptyGoogleNavigate(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.open","toolInput":{}}`,
		`{"action":"continue","toolName":"browser.open","toolInput":{"url":"https://www.google.com"}}`,
		finishMessageWithEvidence("opened", "obs-002", "browser.open", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	navigateCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"browser.open"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		navigateCallCount++
		return testToolSuccess(`{"url":"https://www.google.com"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "opened" {
		t.Fatalf("expected opened reply, got %q", result.FinishMessage)
	}
	if navigateCallCount != 1 {
		t.Fatalf("expected only valid navigate input to invoke tool, got %d calls", navigateCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "url") {
		t.Fatal("expected malformed browser navigate event")
	}
}

func TestAgentTurnRunnerStopsRepeatedMalformedToolInputByLimit(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"continue","toolName":"browser.fill","toolInput":{}}`,
		recoveryDecisionDocument("ask the model to retry with valid input", "explain that the run stopped before completion"),
	}, textResponses: []string{
		"I could not finish the browser fill request before this run stopped. Please try again with the current page still open.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 40})
	fillCallCount := 0
	toolRegistry := newTestToolSet([]string{"browser.fill"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return testToolSuccess(`{"ok":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "fill the search box",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusRunning {
		t.Fatalf("expected the malformed-input loop to terminate, got status %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.stall_exit_directive", "") {
		t.Fatal("expected a stall-exit steer before terminating the malformed-input loop")
	}
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
}

func TestAgentTurnRunnerDoesNotChargeMalformedInputToToolEffort(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"continue","toolName":"alpha","toolInput":{}}`,
		`{"action":"continue","toolName":"beta","toolInput":{}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 2})
	toolRegistry := newTestToolSet([]string{"browser.fill", "alpha", "beta"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"ok":true}`), nil
	})
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "browser.fill") {
		t.Fatal("expected malformed tool event")
	}
}

func TestRepeatedSuccessfulCompletionCandidateUsesPersistedObservation(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	toolInput := json.RawMessage(`{"title":"결산 확인"}`)
	toolInputKey := canonicalToolCallKey("task.add", toolInput)
	state := &agentTaskState{Request: AgentTurnRequest{ToolSet: toolSet}, Observations: []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "task.add",
		ToolInputKey:  toolInputKey,
		Output:        ToolOutput{Content: `{"taskID":"a1"}`},
	}}}

	observation, isFound := repeatedSuccessfulCompletionCandidate(state, turnActionDocument{
		ToolName:  "task.add",
		ToolInput: toolInput,
	}, map[string]turnObservation{})

	if !isFound || observation.ObservationID != "obs-001" {
		t.Fatalf("expected persisted successful observation, got %+v found=%v", observation, isFound)
	}
}

func TestRepeatedSuccessfulReadIsNotACompletionCandidateWhenContractExpectsMutation(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	toolInput := json.RawMessage(`{"query":"결산"}`)
	toolInputKey := canonicalToolCallKey("task.list", toolInput)
	state := &agentTaskState{Request: AgentTurnRequest{
		ToolSet:         toolSet,
		OutcomeContract: OutcomeContract{RequiredEvidenceAnyOf: [][]string{{"task.add"}}},
	}, Observations: []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "task.list",
		ToolInputKey:  toolInputKey,
		Output:        ToolOutput{Content: `{"tasks":[]}`},
	}}}

	_, isFound := repeatedSuccessfulCompletionCandidate(state, turnActionDocument{
		ToolName:  "task.list",
		ToolInput: toolInput,
	}, map[string]turnObservation{})

	if isFound {
		t.Fatal("expected a repeated read never to trigger completion finalization")
	}
}

func TestAgentTurnRunnerRejectsRepeatedSuccessfulToolCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"marp --version"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"marp --version"}}`,
		finishMessageDocument("명령 실행은 완료됐습니다.\n\n@marp-team/marp-cli v4.3.1"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run"})
	registerTestTool(toolRegistry, ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"@marp-team/marp-cli v4.3.1\n","stderr":"","timedOut":false}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "marp 버전 확인해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected duplicate completion: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if toolCallCount != 1 {
		t.Fatalf("expected duplicate tool call not to execute, got %d calls", toolCallCount)
	}
	if !strings.Contains(result.FinishMessage, "@marp-team/marp-cli v4.3.1") {
		t.Fatalf("expected final reply from successful observation, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.duplicate_tool_call_rejected", "obs-001") {
		t.Fatal("expected duplicate rejection event")
	}
}

func TestRepeatedFileReadObservationReturnsCachedCoveredRange(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.read",
		Output:        ToolOutput{Content: `{"path":"home/sites/site-1/draft/app/src/prototype-data.ts","content":"export const PROFILE = {}","startLine":1,"endLine":162,"totalLines":162,"sizeBytes":1000}`},
	}}
	actionDocument := turnActionDocument{
		ToolName:  "file.read",
		ToolInput: json.RawMessage(`{"path":"home/sites/site-1/draft/app/src/prototype-data.ts","startLine":120,"lineCount":40}`),
	}

	observation, isRepeated := repeatedFileReadObservation(observations, actionDocument, "obs-002")

	if !isRepeated {
		t.Fatal("expected covered file.read range to use cached context")
	}
	if observation.Failure != nil {
		t.Fatalf("expected cached read not to fail, got %+v", observation)
	}
	if !strings.Contains(observation.ContentText(), `"cacheStatus":"hit"`) || !strings.Contains(observation.ContentText(), "export const PROFILE") {
		t.Fatalf("expected cached content, got %s", observation.ContentText())
	}
}

func TestRepeatedFileReadObservationReturnsCachedOverlappingRange(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.read",
		Output:        ToolOutput{Content: `{"path":"home/sites/site-1/draft/app/src/prototype-data.ts","content":"export const PROFILE = {}","startLine":1,"endLine":120,"totalLines":180,"sizeBytes":1000}`},
	}}
	actionDocument := turnActionDocument{
		ToolName:  "file.read",
		ToolInput: json.RawMessage(`{"path":"home/sites/site-1/draft/app/src/prototype-data.ts","startLine":1,"lineCount":150}`),
	}

	observation, isRepeated := repeatedFileReadObservation(observations, actionDocument, "obs-002")

	if !isRepeated {
		t.Fatal("expected overlapping file.read range to use cached context")
	}
	if observation.Failure != nil {
		t.Fatalf("expected cached read not to fail, got %+v", observation)
	}
	if !strings.Contains(observation.ContentText(), "121-150") || !strings.Contains(observation.ContentText(), `"cacheStatus":"hit"`) {
		t.Fatalf("expected guidance to request uncovered range, got %s", observation.ContentText())
	}
}

func TestRepeatedFileReadObservationIgnoresCacheAfterFileWrite(t *testing.T) {
	path := "home/sites/site-1/draft/app/src/prototype-data.ts"
	observations := []turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          FileReadToolName,
			Output:        ToolOutput{Content: `{"path":"` + path + `","content":"old","startLine":1,"endLine":20,"totalLines":20,"sizeBytes":1000}`},
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          FileWriteToolName,
			Output:        ToolOutput{Content: `{"path":"` + path + `","sizeBytes":1200}`},
		},
	}
	actionDocument := turnActionDocument{
		ToolName:  FileReadToolName,
		ToolInput: json.RawMessage(`{"path":"` + path + `","startLine":1,"lineCount":20}`),
	}

	_, isRepeated := repeatedFileReadObservation(observations, actionDocument, "obs-003")

	if isRepeated {
		t.Fatal("expected file.read cache to be ignored after a newer file.write")
	}
}

func TestAgentTurnRunnerRejectsRepeatedScheduleCreateWithoutExecutingAgain(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule.create","toolInput":{"taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}}`,
		`{"action":"continue","toolName":"schedule.create","toolInput":{"timeZone":"Asia/Seoul","maxRunCount":10,"repeatPolicy":"finite","intervalSecond":60,"kind":"interval","taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다."}}`,
		finishMessageDocument("예약을 만들었습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"schedule.create"})
	registerTestTool(toolRegistry, ToolDefinition{
		Name:            "schedule.create",
		Namespace:       "schedule",
		SideEffectClass: ToolSideEffectStateChange,
		Completion:      ToolCompletion{Mode: ToolCompletionObservation, Action: "create_schedule", TargetKind: "schedule"},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskScheduleID":"schedule-1","taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1분에 한 번씩 나한테 죄송합니다 10번 해봐",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected duplicate schedule turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected duplicate schedule create not to execute, got %d calls", toolCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.duplicate_tool_call_rejected", "obs-001") {
		t.Fatal("expected duplicate schedule rejection event")
	}
}
