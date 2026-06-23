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
	toolRegistry.RegisterTool(ToolDefinition{Name: "forbidden"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("should not run"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
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

func TestValidateTerminalToolInputRejectsRegisteredToolNameAsCommand(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"terminal.run"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("created"), nil
	})
	input := MarshalToolInput(map[string]any{"command": "site.app.create --slug demo"})

	errorValue := validateTerminalToolInput("terminal.run", input, toolRegistry)

	if errorValue == nil || !isTerminalToolNameError(errorValue) {
		t.Fatalf("expected terminal tool-name error, got %v", errorValue)
	}
}

func TestAgentTurnRunnerRejectsSecondDMSendAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"첫 번째"}}`,
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"두 번째"}}`,
		finishMessageWithEvidence("첫 번째 메시지를 보냈습니다.", "obs-001", "platform.message.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"platform.message.send"})
	sendCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		sendCallCount++
		return ToolSuccess("sent"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "샘플에게 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"platform.message.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"platform.message.send"}},
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
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"정국"},"message":"확인 부탁해"}}`,
		finishMessageWithEvidence("샘플와 정국에게 DM을 보냈습니다.", "obs-001", "platform.message.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"platform.message.send"})
	sendCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		sendCallCount++
		return ToolSuccess("sent"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "샘플와 정국에게 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"platform.message.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"platform.message.send"}},
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

func TestAgentTurnRunnerRejectsRepeatedFailedFingerprint(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.message.context","toolInput":{}}`,
		`{"action":"continue","toolName":"platform.message.context","toolInput":{}}`,
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"정국"},"message":"확인 부탁해"}}`,
		failureReportDocument("mattermost still unavailable", "platform.message.send", "정국", FailureCodes.Unavailable.String(), "mattermost_lookup", "temporary user lookup timeout"),
		recoveryDecisionDocument("Mattermost lookup failed after retry", "mattermost_lookup/unavailable was returned twice", "check Mattermost availability before retrying", "report the failed stage and code"),
	}, textResponses: []string{
		"mattermost_lookup/unavailable 단계에서 Mattermost 조회가 계속 실패해 DM을 보내지 못했습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, RecoveryAttemptLimit: 3})
	toolRegistry := newTestToolSet([]string{"platform.message.send", "platform.message.context"})
	callCount := 0
	sendInputs := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.message.send"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		callCount++
		sendInputs = append(sendInputs, string(invocation.Input))
		return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.message.context"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("mattermost still unavailable", "mattermost still unavailable", "mattermost_unavailable", "mattermost_lookup", true, true), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterName:         "이샘플",
		ConversationID:        "conversation-1",
		Prompt:                "샘플에게 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"platform.message.send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"platform.message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected exhausted retry failure result: %v", errorValue)
	}
	if countStringOccurrences(sendInputs, `"personHint":"샘플"`) != 1 {
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
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"샘플"},"message":"확인 부탁해"}}`,
		failureReportDocument("send failed", "platform.message.send", "샘플", FailureCodes.OperationFailed.String(), "message_send", "Mattermost returned 503 after post create"),
		recoveryDecisionDocument("message send failed", "message_send/operation_failed was returned", "inspect delivery state before retrying", "report the failed stage and avoid duplicate send claims"),
	}, textResponses: []string{
		"message_send/operation_failed 단계에서 전송이 실패했습니다. 중복 전송 위험 때문에 같은 메시지를 다시 보내지는 않았습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 2, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"platform.message.send", "platform.message.context"})
	callCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		callCount++
		return structuredFailureToolResult("Mattermost returned 503 after post create", "Mattermost returned 503 after post create", "send_failed", "message_send", true, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterName:         "이샘플",
		ConversationID:        "conversation-1",
		Prompt:                "샘플에게 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"platform.message.send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"platform.message.send"}},
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
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		t.Fatal("unexpected math.calculate invocation")
		return ToolResult{}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1+1=",
		ToolSet:           toolRegistry,
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
	toolRegistry := newTestToolSet([]string{"browser.fill", "browser.press"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"ok":true}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.press"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		pressCallCount++
		return ToolSuccess(`{"ok":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "입력칸에 hello world라고 입력해줘",
		ToolSet:           toolRegistry,
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
	toolRegistry := newTestToolSet([]string{"browser.snapshot", "browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"snapshotText":"- textbox \"Google 검색\" [ref=e5]"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return ToolSuccess(`{"ok":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "입력칸에 hello world라고 입력해줘",
		ToolSet:           toolRegistry,
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
	toolRegistry := newTestToolSet([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		navigateCallCount++
		return ToolSuccess(`{"url":"https://www.google.com"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolSet:           toolRegistry,
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
		recoveryDecisionDocument("browser.fill input stayed malformed", "the tool was not invoked", "ask the model to retry with valid input", "explain that the run stopped before completion"),
	}, textResponses: []string{
		"I could not finish the browser fill request before this run stopped. Please try again with the current page still open.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 40})
	fillCallCount := 0
	toolRegistry := newTestToolSet([]string{"browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return ToolSuccess(`{"ok":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "fill the search box",
		ToolSet:           toolRegistry,
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
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"ok":true}`), nil
	})
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

func TestAgentTurnRunnerRejectsRepeatedSuccessfulToolCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"marp --version"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"marp --version"}}`,
		finishMessageDocument("명령 실행은 완료됐습니다.\n\n@marp-team/marp-cli v4.3.1"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"@marp-team/marp-cli v4.3.1\n","stderr":"","timedOut":false}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "marp 버전 확인해줘",
		ToolSet:           toolRegistry,
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

func TestShouldRejectUnnecessaryAcknowledgementApprovalReturnsTrueForMemoryConfirm(t *testing.T) {
	toolInput := json.RawMessage(`{"userFacingMessage":"안젤라 바보라는 내용을 기억하고 있습니다. 맞나요?","reasonCode":"destructive_action"}`)

	result := shouldRejectUnnecessaryAcknowledgementApproval("ask.confirm", toolInput)

	if !result {
		t.Fatal("expected acknowledgement confirm wrapping a memory note to be rejected")
	}
}

func TestShouldRejectUnnecessaryAcknowledgementApprovalReturnsFalseForExternalSend(t *testing.T) {
	toolInput := json.RawMessage(`{"userFacingMessage":"이 메시지를 외부로 전송할까요?","reasonCode":"external_send"}`)

	result := shouldRejectUnnecessaryAcknowledgementApproval("ask.confirm", toolInput)

	if result {
		t.Fatal("expected external send confirm not to be rejected")
	}
}

func TestShouldRejectUnnecessaryAcknowledgementApprovalReturnsFalseForNonAskConfirmTool(t *testing.T) {
	toolInput := json.RawMessage(`{"userFacingMessage":"기억하고 있습니다. 맞나요?","reasonCode":"destructive_action"}`)

	result := shouldRejectUnnecessaryAcknowledgementApproval("memory.remember", toolInput)

	if result {
		t.Fatal("expected non-ask.confirm tool not to be rejected")
	}
}

func TestShouldRejectUnnecessaryAcknowledgementApprovalReturnsFalseForUnrelatedConfirm(t *testing.T) {
	toolInput := json.RawMessage(`{"userFacingMessage":"계속 진행할까요?","reasonCode":"paid_action"}`)

	result := shouldRejectUnnecessaryAcknowledgementApproval("ask.confirm", toolInput)

	if result {
		t.Fatal("expected unrelated paid action confirm not to be rejected")
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
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskScheduleID":"schedule-1","taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "1분에 한 번씩 나한테 죄송합니다 10번 해봐",
		ToolSet:           toolRegistry,
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
