package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestCompletionGateRejectsFutureWorkPromiseWithoutScheduleEvidence(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionGate(nil, nil, nil, turnActionDocument{
		Action:             "finish",
		FinishMessage:      "지금부터 고치겠습니다. 완료 후 공유하겠습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
	})
	if result.IsSatisfied {
		t.Fatal("expected completion gate to reject future work promise")
	}
	if !strings.Contains(result.Message, "schedule.create") {
		t.Fatalf("expected schedule evidence guidance, got %q", result.Message)
	}
}

func TestAgentTurnRunnerRejectsAttachmentClaimWithoutAttachmentEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("첨부된 파일들을 확인해 주세요."),
		`{"action":"fail","reason":"attachment evidence missing"}`,
		recoveryDecisionDocument("attachment evidence was missing", "no attachment was available", "ask the user to retry file generation", "explain that attachment evidence was missing"),
	}, textResponses: []string{
		"첨부 파일을 만들거나 보냈다고 확인할 근거가 없어 여기서 멈췄어요. 파일이 필요하면 다시 시도해 주세요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, RecoveryBudget: exhaustedRecoveryBudgetForTest()})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "파일 만들어서 보내줘",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after unsupported attachment claim, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(result.UserNotice, "근거가 없어") {
		t.Fatalf("expected generated failure reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "claims attached files") {
		t.Fatal("expected completion gate to reject attachment claim without evidence")
	}
}

func TestAgentTurnRunnerGeneratesFailureReplyAfterStructuredModelFailure(t *testing.T) {
	languageModel := &structuredFailureTextRecoveryLanguageModel{
		reply:      "지금은 요청을 이어갈 모델 호출이 실패해서 작업을 끝내지 못했어요. 다시 시도하면 현재 제한이 풀렸는지 확인해 볼게요.",
		errorValue: errors.New("structured model failed"),
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "서울 날씨 알려줘",
	})
	if errorValue != nil {
		t.Fatalf("expected generated failure result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if result.UserNotice != languageModel.reply {
		t.Fatalf("expected generated failure reply, got %q", result.UserNotice)
	}
	if len(languageModel.textPrompts) != 1 {
		t.Fatalf("expected one recovery text prompt, got %d", len(languageModel.textPrompts))
	}
	if !strings.Contains(languageModel.textPrompts[0], "structured model failed") {
		t.Fatalf("expected recovery prompt to include failure reason, got %q", languageModel.textPrompts[0])
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
	}
}

func TestAgentTurnRunnerRepairsInvalidFailureReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"pptx build failed"}`,
		recoveryDecisionDocument("browser and slide build failed", "no PPTX attachment exists", "check simple-slides temporary directory handling", "explain the exact failed stages"),
	}, textResponses: []string{
		"브라우저 연결 문제와 시스템 환경 오류가 있어 파일이 생성되지 않았습니다.",
		"PPTX는 첨부되지 않았습니다. 브라우저 열기는 Companion 미연결로 실패했고, 슬라이드 빌드는 Marp 임시 HTML 생성 권한 문제로 중단되어 simple-slides 임시 디렉터리 설정 확인이 필요합니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "https://dawn.kim 보고 사업계획서 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	})

	if errorValue != nil {
		t.Fatalf("expected repaired failure result, got error: %v", errorValue)
	}
	if result.UserNotice != languageModel.textResponses[1] {
		t.Fatalf("expected repaired failure reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated_repair") {
		t.Fatal("expected generated repair failure reply event")
	}
}

func TestAgentTurnRunnerReportsRawErrorWhenAllModelCallsFail(t *testing.T) {
	languageModel := failingRecoveryLanguageModel{errorValue: errors.New("model unavailable")}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "서울 날씨 알려줘",
	})
	if errorValue != nil {
		t.Fatalf("expected dynamic failure result, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "model unavailable") {
		t.Fatalf("expected raw error reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.llm_unavailable", "model unavailable") {
		t.Fatal("expected admin diagnostic event")
	}
}

func TestAgentTurnRunnerUsesLocalRecoveryNoticeWhenRemoteModelCallsFail(t *testing.T) {
	languageModel := localRecoveryFallbackLanguageModel{
		errorValue:         errors.New("model unavailable"),
		localRecoveryReply: "The request could not be answered because the model call returned no usable action. The next check is the model routing log.",
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "Summarize the current thread.",
		ResponseLanguage:  ResponseLanguageEnglish,
	})
	if errorValue != nil {
		t.Fatalf("expected local recovery reply: %v", errorValue)
	}
	if result.UserNotice != languageModel.localRecoveryReply || result.ReplySuppressed {
		t.Fatalf("expected local recovery reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_notice", "local_llm") {
		t.Fatal("expected local LLM notice event")
	}
}

func TestAgentTurnRunnerDoesNotUseDeterministicCapabilityFallbackWhenActionModelFails(t *testing.T) {
	languageModel := failingRecoveryLanguageModel{errorValue: errors.New("structured action unavailable")}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"math.calculate", "file.write", "schedule.create"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		toolRegistry.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("unused"), nil
		})
	}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:    "person-1",
		RequesterCallingName: "동하",
		ConversationID:       "conversation-1",
		Prompt:               "너 뭐 할줄 알아? 짧게 설명해봐",
		ResponseLanguage:     ResponseLanguageKorean,
		ToolSet:              toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected failed turn without deterministic capability reply: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed || result.ReplySuppressed || !strings.Contains(result.UserNotice, "structured action unavailable") {
		t.Fatalf("expected raw failed task notice, got status=%s reply=%q suppressed=%v", result.TaskRun.Status, result.UserNotice, result.ReplySuppressed)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.capability_fallback", "math.calculate") {
		t.Fatal("expected no deterministic capability fallback event")
	}
}

func TestAgentTurnRunnerUsesNaturalCaptchaFailureReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser.snapshot","toolInput":{}}`,
		`{"action":"fail","reason":"blocked_by_captcha"}`,
		recoveryDecisionDocument("browser access was blocked", "captcha or bot detection was observed", "ask for another source or direct access", "explain that automated access was blocked"),
	}, textResponses: []string{
		"동하 님, 날씨를 확인하려고 시도했지만 페이지가 자동화 접근을 막아서 정확한 확인을 끝내지 못했어요. 다른 출처를 주시면 거기서 다시 확인해볼게요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"browser.snapshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "browser_snapshot", "blocked_by_captcha: bot-detection wall"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterCallingName:  "동하",
		ConversationID:        "conversation-1",
		Prompt:                "내일 서울 날씨 검색해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"browser.snapshot"},
	})
	if errorValue != nil {
		t.Fatalf("expected dynamic captcha result, got error: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "동하 님") || !strings.Contains(result.UserNotice, "자동화 접근을 막아서") {
		t.Fatalf("expected natural captcha reply, got %q", result.UserNotice)
	}
	if strings.Contains(result.UserNotice, "처리할 수 없습니다") || strings.Contains(result.UserNotice, "오류가 발생했습니다") {
		t.Fatalf("expected non-mechanical captcha reply, got %q", result.UserNotice)
	}
}

func TestAgentTurnRunnerRejectsHtmlClaimBackedByMarkdownAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"DESIGN.md"}}`,
		finishMessageWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file.attach", 0),
		`{"action":"fail","reason":"html attachment missing"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/DESIGN.md",
				Filename:   "DESIGN.md",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after mismatched attachment claim, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".html") {
		t.Fatal("expected completion gate to reject missing html attachment")
	}
}

func TestAgentTurnRunnerAcceptsHtmlRequestWithHtmlAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 전달해 드립니다.", "obs-001", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
				SizeBytes:  12,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
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

func TestAgentTurnRunnerRequireCapabilitiesPinsHiddenTool(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"require_capabilities","toolNames":["site.app.create"],"skillNames":[],"reason":"need site creation"}`,
		`{"action":"continue","toolName":"site.app.create","toolInput":{"slug":"demo"}}`,
		finishMessageWithEvidence("created", "obs-002", "site.app.create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	siteCreateCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{
		Name:        "site.app.create",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		siteCreateCallCount++
		return ToolResult{Output: ToolOutput{Content: `{"siteID":"site-1"}`}, Attachments: []FileAttachment{{DevicePath: "site://site-1", Filename: "site.json"}}}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "make site",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if siteCreateCallCount != 1 {
		t.Fatalf("expected site.app.create to be invoked once, got %d", siteCreateCallCount)
	}
	if result.FinishMessage != "created" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.capabilities_required", "site.app.create") {
		t.Fatal("expected capability require event")
	}
}

func TestAgentTurnRunnerRequireCapabilitiesPinsSkillInstructionsAndTools(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"require_capabilities","toolNames":[],"skillNames":["site-prototype"],"reason":"need site workflow"}`,
		`{"action":"continue","toolName":"site.app.create","toolInput":{"slug":"demo"}}`,
		finishMessageWithEvidence("created", "obs-002", "site.app.create", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"skill.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "skill.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"skills":[]}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Output: ToolOutput{Content: `{"siteID":"site-1"}`}, Attachments: []FileAttachment{{DevicePath: "site://site-1", Filename: "site.json"}}}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "make site",
		ToolSet:           toolRegistry,
		AvailableSkills: []SkillInstruction{{
			Name:         "site-prototype",
			Prompt:       "SITE WORKFLOW BODY",
			AllowedTools: []string{"site.app.create"},
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "created" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) < 2 || !strings.Contains(joinMessageContent(languageModel.requests[1].Messages), "SITE WORKFLOW BODY") {
		t.Fatalf("expected pinned skill instructions in next model request")
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

func TestAgentTurnRunnerRecordsToolRequestedEvent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.requested", `"value":"one"`) {
		t.Fatal("expected requested tool event with input summary")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.result", "alpha result") {
		t.Fatal("expected result tool event")
	}
}

func TestAgentTurnRunnerRequiresToolEvidenceBeforeFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("browser tool is unavailable"),
		`{"action":"continue","toolName":"memory.search","toolInput":{}}`,
		finishMessageDocument("still no screenshot"),
		`{"action":"continue","toolName":"browser.screenshot","toolInput":{}}`,
		finishMessageWithEvidence("observed", "obs-004", "browser.screenshot", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"browser.screenshot", "memory.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "memory.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`[]`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/screenshot.png"}`},
			Attachments: []FileAttachment{{
				DevicePath: "/tmp/internkim-companion-files/screenshot.png",
				Filename:   "screenshot.png",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected browser tool requirement to recover: %v", errorValue)
	}
	if result.FinishMessage != "observed" {
		t.Fatalf("expected final reply after tool use, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "browser.screenshot") {
		t.Fatal("expected completion requirement event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.memory.search.result", "[]") {
		t.Fatal("expected memory search observation before screenshot")
	}
	if len(result.Attachments) != 1 || result.Attachments[0].DevicePath != "/tmp/internkim-companion-files/screenshot.png" {
		t.Fatalf("expected screenshot attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.browser.screenshot.result", "/tmp/internkim-companion-files/screenshot.png") {
		t.Fatal("expected browser screenshot observation")
	}
}

func TestAgentTurnRunnerRequiresSelectedSkillEvidenceBeforeFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("PPT 못 만들어요"),
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("PPTX를 첨부했습니다.", "obs-002", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "피피티 만들어줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.attach"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q", result.FinishMessage)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file.attach") {
		t.Fatal("expected completion required event for selected skill evidence")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.evidence_missing", "evidence_missing") {
		t.Fatal("expected structured evidence missing event")
	}
}

func TestAgentTurnRunnerDoesNotRequireNonAttachmentToolInCompletionEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"deck.html"}}`,
		finishMessageWithEvidence("HTML 파일을 첨부했습니다: deck.html", "obs-002", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.write", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/deck/presentation.md","sizeBytes":6}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.write", "file.attach"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerRequiresAttachmentSuffixEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"DESIGN.md"}}`,
		finishMessageWithEvidence("첨부했습니다.", "obs-001", "file.attach", 0),
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"deck.pptx"}}`,
		finishMessageWithEvidence("PPTX를 첨부했습니다.", "obs-003", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/" + request.Path,
				Filename:   request.Path,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected required suffix evidence to recover: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") {
		t.Fatalf("expected artifact-aware reply, got %q", result.FinishMessage)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected pptx attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", ".pptx") {
		t.Fatal("expected completion required event for missing attachment suffix")
	}
}

func TestAgentTurnRunnerAcceptsReadableFileAttachObservation(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.html"), "<html><body>Hermes Agent 장단점 분석</body></html>")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/deck/deck.html"}}`,
		finishMessageWithEvidence("deck.html 파일을 첨부했습니다.", "obs-001", "file.attach", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		WorkspaceRootPath:     workspaceRootPath,
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.attach"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn without runner error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected readable attachment to be delivered, got %+v", result.Attachments)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.artifact_attach_rejected", "deck intent manifest is missing") {
		t.Fatal("did not expect intent manifest rejection event")
	}
}

func TestAgentTurnRunnerAutoAttachesRequiredWorkspaceArtifacts(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	languageModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("unused")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{{
			DevicePath: request.Path,
			Filename:   filepath.Base(request.Path),
		}}
		return ToolResult{Output: ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto attachment evidence to succeed: %v", errorValue)
	}
	if !strings.Contains(result.FinishMessage, "deck.pptx") || !strings.Contains(result.FinishMessage, "deck.pdf") {
		t.Fatalf("expected artifact-aware final reply, got %q", result.FinishMessage)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.completion_state_transition", "attach_existing_artifacts") {
		t.Fatal("expected completion state attachment transition")
	}
	if !taskEventsContain(taskEvents, "tool.file.attach.requested", "deck.pptx") {
		t.Fatal("expected automatic file.attach request")
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected completion state to avoid model calls, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerCompletesAfterRequiredArtifactsExist(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build deck"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build another deck"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.attach"})
	terminalCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
			return ToolResult{}, errorValue
		}
		writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
		writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{{DevicePath: request.Path, Filename: filepath.Base(request.Path)}}
		return ToolResult{Output: ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected auto artifact completion: %v", errorValue)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected one build command before auto completion, got %d", terminalCallCount)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected two attachments, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "attachmentCount") {
		t.Fatal("expected completion state finalization event")
	}
}

func TestAgentTurnRunnerDoesNotRepeatFailedAutomaticAttachment(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"attachment unavailable"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	attachmentCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		attachmentCallCount++
		return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", "attachment unavailable"), nil
	})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected failed turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount != 1 {
		t.Fatalf("expected one automatic attachment attempt, got %d", attachmentCallCount)
	}
}

func TestAgentTurnRunnerAttachesReadableImperfectArtifactCandidate(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"), "not a valid pptx")

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"artifact invalid"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	attachmentCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		attachmentCallCount++
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath: "/workspace/private/people/person-1/artifacts/deck/deck.pptx",
				Filename:   "deck.pptx",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected invalid artifact turn to return result without runner error: %v", errorValue)
	}
	if attachmentCallCount == 0 {
		t.Fatal("expected readable imperfect artifact to be attached")
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected imperfect artifact attachment, got %+v", result.Attachments)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.validity_review", `"passed":true`) {
		t.Fatal("expected passing basic validity review event")
	}
}

func TestAgentTurnRunnerAutoCompletionKeepsQualityOutOfCorePolicy(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnStartedAt := time.Now().Add(-time.Minute)
	writeValidPPTXTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pptx"))
	writeValidPDFTestFile(t, filepath.Join(artifactDirectoryPath, "deck.pdf"))

	languageModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("unused")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var request struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &request); errorValue != nil {
			return ToolResult{}, errorValue
		}
		attachments := []FileAttachment{{DevicePath: request.Path, Filename: filepath.Base(request.Path)}}
		return ToolResult{Output: ToolOutput{Content: "file attached"}, Attachments: attachments}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		WorkspaceRootPath:          workspaceRootPath,
		TurnStartedAt:              turnStartedAt,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx", ".pdf"},
	})
	if errorValue != nil {
		t.Fatalf("expected artifact validity completion without core quality checks: %v", errorValue)
	}
	if len(result.Attachments) != 2 {
		t.Fatalf("expected attachments, got %+v", result.Attachments)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.quality_review", "marp_build_log_success") {
		t.Fatal("expected no hard-coded slide quality check event")
	}
}

func TestAgentTurnRunnerAuditsSelectedSkillDecisions(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolSet([]string{"terminal.run"})
	for _, toolName := range []string{"terminal.run", "site.app.create"} {
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
		AvailableSkills:    []SkillInstruction{{Name: "simple-slides", AllowedTools: []string{"terminal.run", "site.app.create"}}},
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
		!taskEventsContain(taskEvents, "agent.instructions_loaded", "site.app.create") {
		t.Fatal("expected tool visibility debug fields in instructions event")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", "selectedSkillAllowedTools") {
		t.Fatal("expected selected skill allowed tools in instructions event")
	}
}

func TestAgentTurnRunnerRejectsUnsatisfiedFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"finish","goalStatus":"in_progress","goalSatisfied":false,"completionEvidence":[],"finishMessage":"done"}`,
		finishMessageDocument("now done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "say hello",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinishMessage != "now done" {
		t.Fatalf("expected recovered final reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "goalSatisfied=true") {
		t.Fatal("expected goalSatisfied completion gate event")
	}
}

func TestAgentTurnRunnerRejectsCompletionEvidenceFromErrorObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"unstable","toolInput":{}}`,
		finishMessageWithEvidence("done", "obs-001", "unstable", 0),
		failureReportDocument("tool failed", "unstable", "{}", FailureCodes.OperationFailed.String(), "unstable", "failed"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "unstable", "failed"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to fail safely: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "unknown or failed observation") {
		t.Fatal("expected failed evidence gate event")
	}
}

func TestAgentTurnRunnerTreatsToolFailureAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"unstable","toolInput":{}}`,
		finishMessageDocument("handled failure"),
		noToolFallbackFinishMessageDocument("handled failure"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, errors.New("tool failed")
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
	if result.FinishMessage != "handled failure" {
		t.Fatalf("expected final reply after failure, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "FailureDebt") {
		t.Fatal("expected final reply to be locked until fallback resolution")
	}
}

func TestAgentTurnRunnerNoToolFallbackWaivesFailedRequiredEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"math.calculate","toolInput":{"expression":"1+2/4"}}`,
		noToolFallbackFinishMessageDocument("1 + 2/4 = 1.5"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{"math.calculate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "math.calculate"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("exec: \"bc\": executable file not found in $PATH", "bc: command not found", "calculator_failed", "bc_execution", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "1+2/4=",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"math.calculate"},
	})
	if errorValue != nil {
		t.Fatalf("expected no-tool fallback to complete: %v", errorValue)
	}
	if result.FinishMessage != "1 + 2/4 = 1.5" {
		t.Fatalf("expected direct fallback answer, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.math.calculate.result", FailureCodes.OperationFailed.String()) {
		t.Fatal("expected internal tool failure event to remain recorded")
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
	failVariant := actionSchemaVariant(t, schemaDocument, "fail")
	failRequired := stringSliceFromAny(failVariant["required"])
	for _, fieldName := range []string{"reason", "goalStatus", "goalSatisfied", "failureResolution", "usedFailureFacts"} {
		if !containsString(failRequired, fieldName) {
			t.Fatalf("expected fail schema to require %s, got %+v", fieldName, failRequired)
		}
	}
	failProperties := mapFromAny(failVariant["properties"])
	usedFailureFacts := mapFromAny(failProperties["usedFailureFacts"])
	attempts := mapFromAny(mapFromAny(usedFailureFacts["properties"])["attempts"])
	if attempts["type"] != "array" {
		t.Fatalf("expected usedFailureFacts.attempts array schema, got %+v", attempts)
	}
}

func TestActionSchemaHidesFailWhileRecoveryBudgetRemains(t *testing.T) {
	toolRegistry := newTestToolSet([]string{"site.app.publish", "file.write"})
	request := BuildAgentActionRequest(agentTaskState{
		Request: AgentTurnRequest{ToolSet: toolRegistry},
		Options: TurnOptions{RecoveryBudget: defaultRecoveryBudget()},
		Observations: []turnObservation{{
			ObservationID:      "obs-001",
			Action:             "continue",
			Tool:               "site.app.publish",
			Output:             ToolOutput{Content: "starter scaffold remains"},
			Failure:            &ToolFailure{Kind: FailureInvalidInput, Code: FailureCodes.InvalidInput.String(), Stage: "site_publish", UserSafeSummary: "starter scaffold remains"},
			ToolInputKey:       "site.app.publish\x00{\"siteID\":\"site-1\"}",
			AttemptFingerprint: "site.app.publish\x00{\"siteID\":\"site-1\"}\x00invalid_input",
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

func TestFailureReportRejectsMissingUsedFailureFacts(t *testing.T) {
	result := validateFailureReportAction(turnActionDocument{
		Action:            "fail",
		Reason:            "calculator failed",
		FailureResolution: failureResolutionFailureReport,
	}, failureReportFacts{
		Attempts: []failureReportAttempt{{
			ToolName:     "math.calculate",
			InputSummary: "1+2/4",
			ErrorCode:    FailureCodes.OperationFailed.String(),
			FailureStage: "bc_execution",
			Message:      "bc: command not found",
		}},
		BudgetState: "failure_report_required",
	})
	if result.IsSatisfied {
		t.Fatal("expected missing usedFailureFacts to be rejected")
	}
}

func TestAgentTurnRunnerPreservesStructuredToolFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"정국","message":"확인 부탁해"}}`,
		failureReportDocument("recipient missing", "platform.dm.send", "정국", FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
		recoveryDecisionDocument("recipient lookup failed", "recipient_resolve/not_found was returned", "inspect candidate recipients before retrying", "report the exact failure stage and code"),
	}, textResponses: []string{
		"recipient_resolve/not_found 단계에서 수신자를 찾지 못해 DM을 보내지 못했습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"platform.dm.send", "platform.dm.inspect"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이동하",
		ConversationID:    "conversation-1",
		Prompt:            "정국에게 DM 보내줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "recipient_resolve/not_found") {
		t.Fatalf("expected structured failure in final reply, got %q", result.UserNotice)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "tool.platform.dm.send.result", FailureCodes.NotFound.String()) {
		t.Fatal("expected structured tool failure event")
	}
}

func TestAgentTurnRunnerDeliversSafeDegradedFailureReplyWithoutStageAndCode(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"정국","message":"확인 부탁해"}}`,
			failureReportDocument("recipient missing", "platform.dm.send", "정국", FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
			recoveryDecisionDocument("recipient lookup failed", "recipient_resolve/not_found was returned", "inspect candidate recipients before retrying", "report the exact failure stage and code"),
		},
		textResponses: []string{"요청을 처리하지 못했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"platform.dm.send"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이동하",
		ConversationID:    "conversation-1",
		Prompt:            "정국에게 DM 보내줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if result.UserNotice != "요청을 처리하지 못했습니다." || result.ReplySuppressed {
		t.Fatalf("expected safe degraded reply to be delivered, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated_degraded") {
		t.Fatal("expected degraded failure reply event")
	}
}

func TestAgentTurnRunnerAcceptsGeneratedStructuredFailureReplyWithStageAndCode(t *testing.T) {
	generatedReply := "recipient_resolve/not_found 단계에서 수신자를 찾지 못했습니다."
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"정국","message":"확인 부탁해"}}`,
			failureReportDocument("recipient missing", "platform.dm.send", "정국", FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
			recoveryDecisionDocument("recipient lookup failed", "recipient_resolve/not_found was returned", "inspect candidate recipients before retrying", "report the exact failure stage and code"),
		},
		textResponses: []string{generatedReply},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"platform.dm.send"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이동하",
		ConversationID:    "conversation-1",
		Prompt:            "정국에게 DM 보내줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if result.UserNotice != generatedReply {
		t.Fatalf("expected generated reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
	}
}

func TestAgentTurnRunnerAllowsCorrectedRetryAfterSafeFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"이동하","message":"확인 부탁해"}}`,
		finishMessageWithEvidence("sent", "obs-003", "platform.dm.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 3})
	toolRegistry := newTestToolSet([]string{"platform.dm.send"})
	callCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		callCount++
		if callCount == 1 {
			return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
		}
		return ToolSuccess(`{"dispatchID":"post-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "send dm",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected retry recovery: %v", errorValue)
	}
	if callCount != 2 {
		t.Fatalf("expected corrected retry, got %d calls", callCount)
	}
	if result.FinishMessage != "sent" {
		t.Fatalf("expected final reply after corrected retry, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_attempt", "corrected_retry") {
		t.Fatal("expected corrected retry event")
	}
}

func TestAgentTurnRunnerRejectsSecondDMSendAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"첫 번째"}}`,
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"두 번째"}}`,
		finishMessageWithEvidence("첫 번째 메시지를 보냈습니다.", "obs-001", "platform.dm.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"platform.dm.send"})
	sendCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		sendCallCount++
		return ToolSuccess("sent"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "동하에게 DM 보내줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"platform.dm.send"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
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

func TestRecoveryAttemptCountOnlyIncludesSpentInterventions(t *testing.T) {
	failure := newFailureObservation("obs-001", "continue", "platform.dm.send", "failed", FailureExternalService, FailureCodes.OperationFailed, "message_send")
	passiveGuidance := recoveryGuidanceObservation(2, failure)
	spentGuidance := recoveryGuidanceObservation(3, failure)
	spentGuidance.RecoveryAttemptSpent = true
	retryObservation := failure
	retryObservation.ObservationID = "obs-004"
	retryObservation.RecoveryAttemptKey = "platform.dm.send\x00{}"
	retryObservation.RecoveryAttemptSpent = true

	if count := recoveryAttemptCount([]turnObservation{failure, passiveGuidance}); count != 0 {
		t.Fatalf("expected passive guidance not to spend recovery budget, got %d", count)
	}
	if count := recoveryAttemptCount([]turnObservation{failure, passiveGuidance, spentGuidance, retryObservation}); count != 2 {
		t.Fatalf("expected spent guidance and retry to consume budget, got %d", count)
	}
}

func TestAgentTurnRunnerRejectsRepeatedFailedFingerprint(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.dm.inspect","toolInput":{"recipientHint":"동하"}}`,
		`{"action":"continue","toolName":"platform.dm.inspect","toolInput":{"recipientHint":"이동하"}}`,
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"정국","message":"확인 부탁해"}}`,
		failureReportDocument("mattermost still unavailable", "platform.dm.send", "정국", FailureCodes.Unavailable.String(), "mattermost_lookup", "temporary user lookup timeout"),
		recoveryDecisionDocument("Mattermost lookup failed after retry", "mattermost_lookup/unavailable was returned twice", "check Mattermost availability before retrying", "report the failed stage and code"),
	}, textResponses: []string{
		"mattermost_lookup/unavailable 단계에서 Mattermost 조회가 계속 실패해 DM을 보내지 못했습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, RecoveryAttemptLimit: 3})
	toolRegistry := newTestToolSet([]string{"platform.dm.send", "platform.dm.inspect"})
	callCount := 0
	sendInputs := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		callCount++
		sendInputs = append(sendInputs, string(invocation.Input))
		return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.inspect"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("mattermost still unavailable", "mattermost still unavailable", "mattermost_unavailable", "mattermost_lookup", true, true), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이동하",
		ConversationID:    "conversation-1",
		Prompt:            "동하에게 DM 보내줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected exhausted retry failure result: %v", errorValue)
	}
	if countStringOccurrences(sendInputs, `"recipientHint":"동하"`) != 1 {
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
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"platform.dm.send","toolInput":{"recipientHint":"동하","message":"확인 부탁해"}}`,
		failureReportDocument("send failed", "platform.dm.send", "동하", FailureCodes.OperationFailed.String(), "message_send", "Mattermost returned 503 after post create"),
		recoveryDecisionDocument("message send failed", "message_send/operation_failed was returned", "inspect delivery state before retrying", "report the failed stage and avoid duplicate send claims"),
	}, textResponses: []string{
		"message_send/operation_failed 단계에서 전송이 실패했습니다. 중복 전송 위험 때문에 같은 메시지를 다시 보내지는 않았습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 2, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestToolSet([]string{"platform.dm.send", "platform.dm.inspect"})
	callCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "platform.dm.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		callCount++
		return structuredFailureToolResult("Mattermost returned 503 after post create", "Mattermost returned 503 after post create", "send_failed", "message_send", true, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이동하",
		ConversationID:    "conversation-1",
		Prompt:            "동하에게 DM 보내줘",
		ToolSet:           toolRegistry,
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
		ToolSet:           toolRegistry,
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
		VisibleContext: VisibleContext{Messages: []VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
		ToolSet: toolRegistry,
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

func TestAgentTurnRunnerRemovesQualityCriteriaActionAfterCriteriaAreSet(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":[{"id":"done-once","description":"criteria are declared","required":true}],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"alpha","toolInput":{}}`,
		`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"alpha"}],"qualityReview":[{"id":"done-once","passed":true,"evidence":[{"observationID":"obs-002","toolName":"alpha"}]}],"finishMessage":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:         "person-1",
		ConversationID:            "conversation-1",
		Prompt:                    "make an artifact",
		ToolSet:                   toolRegistry,
		QualityAcceptanceGuidance: []string{"declare criteria first"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) < 2 {
		t.Fatalf("expected at least two model requests, got %d", len(languageModel.requests))
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected initial schema to allow quality criteria, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if strings.Contains(languageModel.requests[1].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected next schema to remove quality criteria, got %s", languageModel.requests[1].StructuredOutputSchema.Document)
	}
}

func TestAgentTurnRunnerDoesNotBlockFinishedExpectedResultForMissingQualityReview(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":[{"id":"visual-review","description":"review the artifact","required":true}],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"site.app.publish","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish with the public URL","expectedTools":[],"expectedNextResults":["public URL"],"doneCriteria":["public URL is available"],"risk":"none","workingSetReason":"publish satisfies the link expected result"}}`,
		`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"site.app.publish"}],"finishMessage":"배포했습니다: https://portfolio.example"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestToolSet([]string{"site.app.publish"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"publishedURL":"https://portfolio.example","status":"published"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "사이트를 배포해줘",
		ToolSet:           toolRegistry,
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{
			ID:          "site-public-link",
			Type:        ExpectedResultTypeLink,
			Description: "사용자가 열 수 있는 public URL의 웹사이트",
			Required:    true,
		}}},
	})
	if errorValue != nil {
		t.Fatalf("expected finish to pass without qualityReview hard gate: %v", errorValue)
	}
	if result.FinishMessage != "배포했습니다: https://portfolio.example" {
		t.Fatalf("expected final publish message, got %q", result.FinishMessage)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "qualityReview") {
		t.Fatal("expected missing qualityReview to remain a review hint, not a completion blocker")
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
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
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
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.no_progress_loop_stopped", "3 consecutive") {
		t.Fatal("expected no-progress loop stop event")
	}
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
}

func TestLimitReachedPromptPreservesFailureReportFacts(t *testing.T) {
	observations := []turnObservation{
		newFailureObservation("obs-001", "continue", "terminal.run", `{"exitCode":1,"stderr":"mkdir: cannot create directory 'artifacts': Permission denied"}`, FailureExternalService, FailureCodes.OperationFailed, "terminal_run"),
	}
	prompt := buildLimitReachedPrompt(AgentTurnRequest{Prompt: "pptx 만들어줘"}, "max_iterations", observations, nil, ExecutionState{}, recoveryDecision{})

	for _, expectedText := range []string{
		"FailureReportFacts that must be reflected accurately",
		"terminal.run",
		"operation_failed",
		"terminal_run",
		"Permission denied",
	} {
		if !strings.Contains(prompt, expectedText) {
			t.Fatalf("expected limit prompt to contain %q, got %s", expectedText, prompt)
		}
	}
}

func TestRequiredArtifactFailureReplyRejectsTextFallbackOffer(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:                     "사업계획서 발표 자료 pptx 만들어줘",
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}
	reply := "스크립트 실행 권한 오류(errorCode: operation_failed)가 발생하여 최종 파일 생성 단계가 중단되었습니다. 준비된 발표 자료의 전체 기획안을 텍스트로 이곳에 바로 정리해 드릴까요?"

	if !failureReplyIsInvalidForRequest(reply, request, "operation_failed", nil, nil) {
		t.Fatal("expected required artifact failure reply with raw code and text fallback to be rejected")
	}
	if !limitReachedReplyIsInvalid(reply, request, nil) {
		t.Fatal("expected required artifact limit reply with raw code and text fallback to be rejected")
	}
}

func TestRequiredArtifactFailureReplyRejectsGenericFailureSummary(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:                     "https://dawn.kim 보고 사업계획서 발표자료 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}
	observations := []turnObservation{
		newFailureObservation("obs-001", "continue", "browser_handoff.openURL", "Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.", FailureExternalService, FailureCodes.OperationFailed, "browser_handoff"),
		newFailureObservation("obs-002", "continue", "terminal.run", `[ ERROR ] Failed converting Markdown. (EACCES: permission denied, open '/workspace/tmp-457-sVDK32cv3ara-.html')`, FailureExternalService, FailureCodes.OperationFailed, "terminal_run"),
	}
	reply := "요청하신 웹사이트 접속과 최종 PPT 변환 도구 실행 과정에서 오류가 발생하여 파일이 생성되지 않았습니다. 현재 브라우저 연결 문제와 프레젠테이션 생성을 위한 시스템 환경 오류가 확인되었으며, 이에 대한 추가적인 엔지니어링 확인이 필요한 상황입니다."

	if !failureReplyIsInvalidForRequest(reply, request, "no artifact attached", observations, nil) {
		t.Fatal("expected required artifact failure reply with generic browser/system summary to be rejected")
	}
}

func TestRequiredArtifactFailureReplyAcceptsConcreteNaturalSummary(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:                     "https://dawn.kim 보고 사업계획서 발표자료 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}
	observations := []turnObservation{
		newFailureObservation("obs-001", "continue", "browser_handoff.openURL", "Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.", FailureExternalService, FailureCodes.OperationFailed, "browser_handoff"),
		newFailureObservation("obs-002", "continue", "terminal.run", `[ ERROR ] Failed converting Markdown. (EACCES: permission denied, open '/workspace/tmp-457-sVDK32cv3ara-.html')`, FailureExternalService, FailureCodes.OperationFailed, "terminal_run"),
	}
	reply := "PPTX는 첨부되지 않았습니다. 브라우저 열기는 Companion 미연결로 실패했고, 슬라이드 빌드는 Marp 임시 HTML 생성 권한 문제로 중단되어 simple-slides 임시 디렉터리 설정 확인이 필요합니다."

	if failureReplyIsInvalidForRequest(reply, request, "no artifact attached", observations, nil) {
		t.Fatal("expected required artifact failure reply with concrete natural facts to be accepted")
	}
}

func TestRequiredArtifactPromptsForbidTextSubstitute(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:                     "사업계획서 발표 자료 pptx 만들어줘",
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}

	failurePrompt := buildFailureReplyPrompt(request, "terminal.run failed", nil, nil, ExecutionState{}, recoveryDecision{})
	limitPrompt := buildLimitReachedPrompt(request, "max_iterations", nil, nil, ExecutionState{}, recoveryDecision{})
	repairPrompt := buildLimitReachedRepairPrompt(limitPrompt, "텍스트로 정리해 드릴까요?", request, nil, 1)

	for _, prompt := range []string{failurePrompt, limitPrompt, repairPrompt} {
		if !strings.Contains(prompt, "Do not offer chat text as a substitute") {
			t.Fatalf("expected required artifact prompt to forbid chat text substitute, got %s", prompt)
		}
		if !strings.Contains(prompt, "errorCode") {
			t.Fatalf("expected required artifact prompt to mention raw diagnostic identifiers, got %s", prompt)
		}
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

func TestAgentTurnRunnerRejectsUnnecessarySitePublishApproval(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"ask.confirm","toolInput":{"userFacingMessage":"배포는 외부 영향이 있는 작업이므로 확인이 필요합니다.","reasonCode":"external_send"}}`,
		`{"action":"continue","toolName":"site.app.publish","toolInput":{"siteID":"site-1","message":"Publish prototype"}}`,
		finishMessageWithEvidence("배포했습니다.", "obs-002", "site.app.publish", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 4})
	publishCallCount := 0
	toolRegistry := newTestToolSet([]string{"ask.confirm", "terminal.run", "site.app.create", "site.app.publish"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		publishCallCount++
		return ToolSuccess(`{"siteID":"site-1","status":"published","publishedURL":"https://demo.example"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "웹사이트 만들어서 배포해",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"site.app.publish"},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		WorkspaceRootPath:     t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected site publish to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if publishCallCount != 1 {
		t.Fatalf("expected site.app.publish to run once, got %d", publishCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "confirmation.requested", "") {
		t.Fatal("unexpected waiting approval request")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.approval_request_rejected", "site.app.publish") {
		t.Fatal("expected unnecessary approval rejection event")
	}
}

func TestAgentTurnRunnerSiteLoopBuildsReviewsPublishesBeforeFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.app.create","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"build the created site","expectedTools":["site.app.build","artifact.review"],"doneCriteria":["site build succeeds"],"risk":"draft may be incomplete","workingSetReason":"creation must lead into build and review"}}`,
		`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"review the built artifact","expectedTools":["artifact.review","site.app.publish"],"doneCriteria":["review passes"],"risk":"visual issues may block publish","workingSetReason":"build output needs review before publish"}}`,
		`{"action":"continue","toolName":"artifact.review","toolInput":{"path":"home/sites/site-1/app/dist/index.html"},"nextStepPlan":{"objective":"publish reviewed site","expectedTools":["site.app.publish","site.app.status"],"doneCriteria":["publish succeeds"],"risk":"publish may reject stale build","workingSetReason":"review evidence allows publish"}}`,
		`{"action":"continue","toolName":"site.app.publish","toolInput":{"siteID":"site-1","message":"Publish portfolio"},"nextStepPlan":{"objective":"confirm final status","expectedTools":["site.app.status"],"doneCriteria":["status shows published URL"],"risk":"status may not reflect latest version","workingSetReason":"final status is required evidence"}}`,
		`{"action":"continue","toolName":"site.app.status","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish with status evidence","expectedTools":[],"doneCriteria":["finish with published URL"],"risk":"none","workingSetReason":"all required evidence has been collected"}}`,
		`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"site.app.build"},{"observationID":"obs-003","toolName":"artifact.review"},{"observationID":"obs-004","toolName":"site.app.publish"},{"observationID":"obs-005","toolName":"site.app.status"}],"finishMessage":"같은 URL에 배포했습니다: https://portfolio.example"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 8})
	toolRegistry := newTestToolSet([]string{"site.app.status", "site.app.create", "site.app.build", "artifact.review", "site.app.publish"})
	toolCalls := []string{}
	hasBuildQuality := false
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.status"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.status")
		return ToolSuccess(`{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example","revisionCount":1}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.create")
		return ToolSuccess(`{"siteID":"site-1","sourceWorkspacePath":"home/sites/site-1","appWorkspacePath":"home/sites/site-1/app","publishedURL":"https://portfolio.example"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.build")
		hasBuildQuality = true
		return ToolSuccess(`{"qualityPath":"home/sites/site-1/.internkim/build-quality.json","distPath":"home/sites/site-1/app/dist"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "artifact.review"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "artifact.review")
		return ToolSuccess(`{"status":"passed","blockingIssueCount":0}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.publish")
		if !hasBuildQuality {
			return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "site_publish", "missing build-quality.json"), nil
		}
		return ToolSuccess(`{"siteID":"site-1","publishedURL":"https://portfolio.example","currentVersionID":"rev-2"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 만들고 배포해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"site.app.status", "site.app.build", "artifact.review", "site.app.publish"},
		AvailableSkills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.app.status", "site.app.create", "site.app.build", "artifact.review", "site.app.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	})
	if errorValue != nil {
		t.Fatalf("expected site loop to succeed: %v", errorValue)
	}
	expectedCalls := []string{"site.app.create", "site.app.build", "artifact.review", "site.app.publish", "site.app.status"}
	if strings.Join(toolCalls, ",") != strings.Join(expectedCalls, ",") {
		t.Fatalf("expected site tool loop %v, got %v", expectedCalls, toolCalls)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted || !strings.Contains(result.FinishMessage, "배포") {
		t.Fatalf("expected completed publish finish, got status=%s message=%q", result.TaskRun.Status, result.FinishMessage)
	}
}

func TestAgentTurnRunnerSiteWorkingSetKeepsCreationRouteWithRequiredEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{toolSelections: []string{
		`{"selectedToolIDs":["site.app.status","site.app.create","file.write","site.app.build","artifact.review","site.app.publish"],"reason":"model chooses the tools that satisfy the site link expected result"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestToolSet([]string{
		"site.app.status",
		"site.app.create",
		"file.write",
		"terminal.run",
		"site.app.build",
		"artifact.review",
		"site.app.publish",
		"file.attach",
	})
	request := AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "김인턴 너의 개인 홈페이지 하나 만들어서 배포해봐.",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"site.app.status", "site.app.build", "site.app.publish", "file.attach"},
		AvailableSkills: []SkillInstruction{{
			Name: "site-prototype",
			AllowedTools: []string{
				"site.app.status",
				"site.app.create",
				"file.write",
				"terminal.run",
				"site.app.build",
				"artifact.review",
				"site.app.publish",
				"file.attach",
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "김인턴 너의 개인 홈페이지 하나 만들어서 배포해봐.",
			Status:              ActiveGoalStatusActive,
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"site.app.status", "site.app.build", "site.app.publish", "file.attach"},
				ArtifactRequirement:   ArtifactRequirementRequired,
			},
		},
	}

	stepRequest := services.runner.requestForStep(context.Background(), request, agentTaskState{Request: request})
	for _, toolName := range []string{"site.app.status", "site.app.create", "file.write", "site.app.build", "artifact.review", "site.app.publish"} {
		if !stepRequest.ToolSet.CanExpose(toolName) {
			t.Fatalf("expected initial site working set to expose %s, got %+v", toolName, stepRequest.ToolExposure.ExposedToolIDs)
		}
	}
}

func TestAgentTurnRunnerNextStepPlanExpandsWorkingSet(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	toolRegistry := newTestToolSet([]string{
		"site.app.status",
		"site.app.create",
		"file.read",
		"file.write",
		"terminal.run",
		"site.app.build",
		"artifact.review",
		"site.app.publish",
	})
	request := AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "개인 홈페이지 만들고 배포해줘",
		ToolSet:           toolRegistry,
	}
	state := agentTaskState{
		Request: request,
		NextStepPlan: NextStepPlan{
			Objective:           "prepare a publishable site draft",
			ExpectedTools:       []string{"site.app.create", "file.write", "terminal.run", "site.app.build"},
			ExpectedNextResults: []string{"a draft workspace exists", "a build result exists"},
			DoneCriteria:        []string{"draft and build expected results exist"},
			Risk:                "site may not exist yet",
			WorkingSetReason:    "next result expectations explain why the tool set is needed",
		},
		Observations: []turnObservation{{
			ObservationID:  "obs-001",
			Action:         "expected_result_missing",
			RecoveryPacket: &RecoveryPacket{WhyLikely: "A public URL is still missing."},
		}},
	}

	stepRequest := services.runner.requestForStep(context.Background(), request, state)
	for _, toolName := range []string{"site.app.create", "file.write", "terminal.run", "site.app.build"} {
		if !stepRequest.ToolSet.CanExpose(toolName) {
			t.Fatalf("expected evidence-driven working set to expose %s, got %+v", toolName, stepRequest.ToolExposure.ExposedToolIDs)
		}
	}
}

func TestAgentTurnRunnerExpectedResultVerifierBlocksEarlyFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		resultVerifications: []string{
			`{"overallStatus":"missing","summary":"missing public URL","results":[{"id":"site-public-link","status":"missing","reason":"Only a draft exists.","citedObservationIDs":["obs-001"],"missingDescription":"A public URL is still missing.","suggestedNextTools":["site.app.publish"]}]}`,
			`{"overallStatus":"satisfied","summary":"public URL exists","results":[{"id":"site-public-link","status":"satisfied","reason":"Publish returned a public URL.","citedObservationIDs":["obs-003"],"missingDescription":"","suggestedNextTools":[]}]}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"site.app.create","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"create draft","expectedTools":[],"expectedNextResults":["draft site project exists"],"doneCriteria":["draft exists"],"risk":"none","workingSetReason":"create prepares the project"}}`,
			`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.app.create"}],"finishMessage":"초안을 만들었습니다."}`,
			`{"action":"continue","toolName":"site.app.publish","toolInput":{"siteID":"site-1","message":"Publish"},"nextStepPlan":{"objective":"finish after public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"publish should satisfy the expected result"}}`,
			`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-003","toolName":"site.app.publish"}],"finishMessage":"배포했습니다: https://portfolio.example"}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestToolSet([]string{"site.app.create", "site.app.publish"})
	toolCalls := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.create")
		return ToolSuccess(`{"siteID":"site-1","status":"draft"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.publish")
		return ToolSuccess(`{"siteID":"site-1","publishedURL":"https://portfolio.example"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "개인 홈페이지 만들어서 배포해줘",
		ToolSet:           toolRegistry,
		OutcomeContract: OutcomeContract{
			ExpectedResults: []ExpectedResult{{
				ID:          "site-public-link",
				Type:        ExpectedResultTypeLink,
				Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete after verifier-guided recovery: %v", errorValue)
	}
	if strings.Join(toolCalls, ",") != "site.app.create,site.app.publish" {
		t.Fatalf("expected create then publish, got %+v", toolCalls)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.expected_result_verification", "missing public URL") {
		t.Fatal("expected result verification event")
	}
}

func TestAgentTurnRunnerExpectedResultsDoNotRequireLegacyToolEvidenceFirst(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		resultVerifications: []string{
			`{"overallStatus":"satisfied","summary":"public URL exists","results":[{"id":"site-public-link","status":"satisfied","reason":"Publish returned a public URL.","citedObservationIDs":["obs-001"],"missingDescription":"","suggestedNextTools":[]}]}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"site.app.publish","toolInput":{"siteID":"site-1","message":"Publish"},"nextStepPlan":{"objective":"finish with public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"publish should satisfy the expected result"}}`,
			`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.app.publish"}],"finishMessage":"배포했습니다: https://portfolio.example"}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"site.app.publish", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.publish"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"publishedURL":"https://portfolio.example"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 배포해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.attach"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file.attach"},
			ExpectedResults: []ExpectedResult{{
				ID:          "site-public-link",
				Type:        ExpectedResultTypeLink,
				Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Contains(result.FinishMessage, "첨부") {
		t.Fatalf("expected link result, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerFileExpectedResultRequiresAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		resultVerifications: []string{
			`{"overallStatus":"satisfied","summary":"PPTX attached","results":[{"id":"attached-file","status":"satisfied","reason":"file.attach returned an attachment.","citedObservationIDs":["obs-003"],"missingDescription":"","suggestedNextTools":[]}]}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"file.promote","toolInput":{"path":"tmp/deck/build/deck.pptx","destinationDirectoryPath":"artifacts/deck","overwrite":true},"nextStepPlan":{"objective":"attach promoted file","expectedTools":["file.attach"],"expectedNextResults":["attached pptx"],"doneCriteria":["file attached"],"risk":"none","workingSetReason":"file deliverable requires attachment"}}`,
			`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"file.promote"}],"finishMessage":"PPTX를 첨부했습니다."}`,
			`{"action":"continue","toolName":"file.attach","toolInput":{"path":"artifacts/deck/deck.pptx"},"nextStepPlan":{"objective":"finish","expectedTools":[],"expectedNextResults":["final message"],"doneCriteria":["attached file delivered"],"risk":"none","workingSetReason":"attachment now exists"}}`,
			finishMessageWithEvidence("PPTX를 첨부했습니다.", "obs-003", "file.attach", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestToolSet([]string{"file.promote", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.promote"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"artifacts/deck/deck.pptx"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.attach"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "file attached"},
			Attachments: []FileAttachment{{
				DevicePath:  "/tmp/deck.pptx",
				Filename:    "deck.pptx",
				ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "PPTX 파일로 첨부해줘",
		ToolSet:           toolRegistry,
		OutcomeContract: OutcomeContract{
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".pptx"},
			ExpectedResults: []ExpectedResult{{
				ID:          "attached-file",
				Type:        ExpectedResultTypeFile,
				Description: "수정 가능한 PPTX 파일 한 개",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete after attachment: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "file.attach") {
		t.Fatal("expected completion gate to require file.attach")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.file.attach.requested", "deck.pptx") {
		t.Fatal("expected file.attach after promoted-only finish was rejected")
	}
}

func TestAgentTurnRunnerReselectsToolsAfterRejectedSiteFinish(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		toolSelections: []string{
			`{"selectedToolIDs":["site.app.create"],"reason":"create draft first"}`,
			`{"selectedToolIDs":["site.app.build"],"reason":"build is still required"}`,
			`{"selectedToolIDs":["site.app.build"],"reason":"run required build after rejected finish"}`,
		},
		contents: []string{
			`{"action":"continue","toolName":"site.app.create","toolInput":{"slug":"portfolio","title":"Portfolio"},"nextStepPlan":{"objective":"build the draft before finishing","expectedTools":["site.app.build"],"doneCriteria":["build evidence exists"],"risk":"draft creation alone is not completion","workingSetReason":"site.app.build is required evidence"}}`,
			`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-001","toolName":"site.app.create"}],"finishMessage":"초안이 만들어졌습니다."}`,
			`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish after build evidence","expectedTools":[],"doneCriteria":["build observation exists"],"risk":"none","workingSetReason":"required evidence has been collected"}}`,
			finishMessageWithEvidence("빌드까지 완료했습니다.", "obs-003", "site.app.build", 0),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 4})
	toolRegistry := newTestToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "site.app.create", "site.app.build"})
	toolCalls := []string{}
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.create")
		return ToolSuccess(`{"siteID":"site-1","status":"draft"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCalls = append(toolCalls, "site.app.build")
		return ToolSuccess(`{"siteID":"site-1","distPath":"home/sites/site-1/app/dist"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 만들고 배포해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"site.app.build"},
		AvailableSkills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.app.create", "site.app.build"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	})
	if errorValue != nil {
		t.Fatalf("expected rejected finish to recover into build: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Join(toolCalls, ",") != "site.app.create,site.app.build" {
		t.Fatalf("expected create then build, got %+v", toolCalls)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.completion_required", "site.app.build") {
		t.Fatal("expected early finish to be rejected by completion gate")
	}
	if !taskEventsContain(events, "agent.tool_exposure", "site.app.build") {
		t.Fatal("expected build tool to be exposed after rejected finish")
	}
	if !taskEventsContain(events, "agent.tool_exposure", "deterministic") {
		t.Fatal("expected deterministic per-iteration tool exposure without selector LLM calls")
	}
	if !taskEventsContain(events, "agent.next_step_plan", "site.app.build") {
		t.Fatal("expected next Step plan to be recorded for working set selection")
	}
	if !taskEventsContain(events, "agent.step_working_set", "site.app.build") {
		t.Fatal("expected Step working set event to include planned build tool")
	}
}

func TestRepeatedFileReadObservationRejectsCoveredRange(t *testing.T) {
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
		t.Fatal("expected covered file.read range to be rejected")
	}
	if observation.Failure == nil || observation.Failure.Stage != "file_read_repeat" {
		t.Fatalf("expected file_read_repeat failure, got %+v", observation)
	}
	if !strings.Contains(observation.ContentText(), "Recent file context") {
		t.Fatalf("expected guidance to reuse recent file context, got %s", observation.ContentText())
	}
}

func TestRepeatedFileReadObservationRejectsOverlappingRange(t *testing.T) {
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
		t.Fatal("expected overlapping file.read range to be rejected")
	}
	if !strings.Contains(observation.ContentText(), "121-150") {
		t.Fatalf("expected guidance to request uncovered range, got %s", observation.ContentText())
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

	updatedRequest := requestWithStepWorkingSetTools(request, NextStepPlan{ExpectedTools: []string{"site.app.status"}}, nil)

	if stringSliceContains(updatedRequest.PinnedToolNames, "calendar.event.add") || stringSliceContains(updatedRequest.PinnedToolNames, "calendar.event.delete") {
		t.Fatalf("did not expect calendar tools pinned for site content mention, got %+v", updatedRequest.PinnedToolNames)
	}
	if !stringSliceContains(updatedRequest.PinnedToolNames, "site.app.status") {
		t.Fatalf("expected next step tool to remain pinned, got %+v", updatedRequest.PinnedToolNames)
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

	updatedRequest := requestWithStepWorkingSetTools(request, NextStepPlan{ExpectedTools: []string{"file.write"}}, nil)

	if stringSliceContains(updatedRequest.PinnedToolNames, "calendar.event.add") || stringSliceContains(updatedRequest.PinnedToolNames, "calendar.event.delete") {
		t.Fatalf("did not expect calendar tools pinned for slides content mention, got %+v", updatedRequest.PinnedToolNames)
	}
	if !stringSliceContains(updatedRequest.PinnedToolNames, "file.write") {
		t.Fatalf("expected next step tool to remain pinned, got %+v", updatedRequest.PinnedToolNames)
	}
}

func TestCompletedInspectionToolDoesNotPinSameNextStepTool(t *testing.T) {
	request := AgentTurnRequest{}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.read",
		Summary:       "path=tmp/deck/presentation.md; range=1-100",
	}}

	updatedRequest := requestWithStepWorkingSetTools(request, NextStepPlan{
		ExpectedTools: []string{"file.read", "file.edit"},
	}, observations)

	if stringSliceContains(updatedRequest.PinnedToolNames, "file.read") {
		t.Fatalf("did not expect the completed inspection tool to be pinned again, got %+v", updatedRequest.PinnedToolNames)
	}
	if !stringSliceContains(updatedRequest.PinnedToolNames, "file.edit") {
		t.Fatalf("expected next non-inspection tool to stay pinned, got %+v", updatedRequest.PinnedToolNames)
	}
}

func TestAgentTurnRunnerRejectsQualityGateRetryUntilSourceChanges(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"}}`,
		`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"/workspace/sites/site-1/app/src/App.tsx","content":"export default function App(){return <main>Portfolio</main>}"}}`,
		`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"}}`,
		finishMessageWithEvidence("수정 후 빌드했습니다.", "obs-004", "site.app.build", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 6})
	toolRegistry := newTestToolSet([]string{"site.app.build", "file.write"})
	buildCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		buildCallCount++
		if buildCallCount == 1 {
			return ToolResult{
				Output: ToolOutput{Content: `{"qualityPath":"/workspace/sites/site-1/.internkim/build-quality.json","issues":[{"target":"app/src/App.tsx"}]}`},
				Failure: &ToolFailure{
					Kind:                  FailureInvalidInput,
					Code:                  "site_quality_gate_failed",
					Stage:                 "site_build_quality_gate",
					UserSafeSummary:       "quality gate failed",
					Retryable:             true,
					FailureClass:          failureClassQuality,
					RetryPolicy:           retryPolicyAfterPrecondition,
					RequiredPreconditions: []string{"source_changed"},
					RecoveryHints:         []RecoveryHint{{Action: "edit_resource", ToolNames: []string{"file.write", "file.read"}}},
					AffectedResources:     []AffectedResource{{Path: "app/src/App.tsx", Role: "source"}},
				},
			}, nil
		}
		return ToolSuccess(`{"qualityPath":"/workspace/sites/site-1/.internkim/build-quality.json","distPath":"/workspace/sites/site-1/app/dist"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"/workspace/sites/site-1/app/src/App.tsx","bytesWritten":64}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "사이트 빌드해서 배포 준비해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"site.app.build"},
	})
	if errorValue != nil {
		t.Fatalf("expected recovery loop to finish: %v", errorValue)
	}
	if buildCallCount != 2 {
		t.Fatalf("expected duplicate build retry to be rejected before tool invocation, got %d build calls", buildCallCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, "agent.recovery_choice_rejected", "source_changed") {
		t.Fatal("expected retry without source change to be rejected")
	}
	if !taskEventsContain(events, "agent.recovery_guidance", "RecoveryPacket") {
		t.Fatal("expected model-visible recovery packet")
	}
}

func TestAgentTurnRunnerAllowsInspectionAfterAdjacentRecoveryBudgetExhausted(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"}}`,
		`{"action":"continue","toolName":"file.read","toolInput":{"path":"home/sites/site-1/draft/app/src/App.tsx"}}`,
		`{"action":"continue","toolName":"file.edit","toolInput":{"path":"home/sites/site-1/draft/app/src/App.tsx","oldText":"broken","newText":"fixed"}}`,
		finishMessageDocument("확인했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 6,
		MaxToolCallCount:  4,
		RecoveryBudget: RecoveryBudget{
			CorrectedRetry: 0,
			AlternateRoute: 0,
			AdjacentTool:   -1,
			NoToolFallback: 0,
		},
	})
	toolRegistry := newTestToolSet([]string{"site.app.build", "file.read", "file.edit"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "source failed"},
			Failure: &ToolFailure{
				Kind:                  FailureInvalidInput,
				Code:                  FailureCodes.InvalidInput.String(),
				Stage:                 "site_build_source",
				UserSafeSummary:       "site source failed",
				Retryable:             true,
				FailureClass:          failureClassQuality,
				RetryPolicy:           retryPolicyAfterPrecondition,
				RequiredPreconditions: []string{"source_changed"},
				RecoveryHints:         []RecoveryHint{{Action: "edit_resource", ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"}}},
			},
		}, nil
	})
	fileReadCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.read"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fileReadCount++
		return ToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","content":"broken"}`), nil
	})
	fileEditCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.edit"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fileEditCount++
		return ToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","matchCount":1}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "사이트 빌드 문제 확인해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"site.app.build", "file.read", "file.edit"},
	})
	if errorValue != nil {
		t.Fatalf("expected inspection recovery to continue: %v", errorValue)
	}
	if fileReadCount != 1 {
		t.Fatalf("expected file.read to run despite exhausted adjacent budget, got %d", fileReadCount)
	}
	if fileEditCount != 1 {
		t.Fatalf("expected file.edit precondition action to run despite exhausted adjacent budget, got %d", fileEditCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(events, "agent.recovery_budget_exhausted", "file.read") {
		t.Fatal("did not expect inspection tool to be blocked by adjacent recovery budget")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "inspection") {
		t.Fatal("expected inspection recovery event")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "precondition") {
		t.Fatal("expected precondition recovery event")
	}
}

func TestAgentTurnRunnerDoesNotApplySiteApprovalRejectToDirectMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"ask.confirm","toolInput":{"userFacingMessage":"동하 님에게 DM을 보내도 될까요?","reasonCode":"external_send","reasonDetail":"external send"}}`,
		finishMessageDocument("승인 요청했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"ask.confirm", "terminal.run", "site.app.create", "site.app.publish", "platform.dm.send"})
	approvalCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "ask.confirm"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		approvalCallCount++
		return ToolSuccess("approval requested"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "동하에게 DM 보내줘",
		ToolSet:           toolRegistry,
		SkillDecisions:    []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected approval request to pause: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if approvalCallCount != 1 {
		t.Fatalf("expected approval request tool to run, got %d", approvalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.approval_request_rejected", "") {
		t.Fatal("site approval rejection must not apply to direct-message tasks")
	}
}

func TestAgentTurnRunnerWaitingApprovalUsesOnlyUserFacingMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"ask.confirm","toolInput":{"userFacingMessage":"동하 님에게 다음 DM을 보내도 될까요?\n\n테스트","reasonCode":"external_send","reasonDetail":"Direct messages are external sends and require approval before immediate delivery."}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 2})
	toolRegistry := newTestToolSet([]string{"ask.confirm"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "ask.confirm"}, func(toolContext context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			UserFacingMessage string `json:"userFacingMessage"`
			ReasonCode        string `json:"reasonCode"`
			ReasonDetail      string `json:"reasonDetail"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolResult{}, errorValue
		}
		taskRunID := TaskRunIDFromContext(toolContext)
		_, _ = services.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingApproval, input.ReasonDetail)
		return ToolSuccess(marshalEventBody(map[string]string{
			"userFacingMessage": input.UserFacingMessage,
			"reasonCode":        input.ReasonCode,
			"reasonDetail":      input.ReasonDetail,
		})), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "동하에게 테스트라고 DM 보내줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		SkillDecisions:    []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		WorkspaceRootPath: t.TempDir(),
	})
	if errorValue != nil {
		t.Fatalf("expected approval request to pause: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval task, got %s", result.TaskRun.Status)
	}
	if result.UserNotice != "동하 님에게 다음 DM을 보내도 될까요?\n\n테스트" {
		t.Fatalf("expected user-facing approval message, got %q", result.UserNotice)
	}
	if strings.Contains(result.UserNotice, "Direct messages are external sends") {
		t.Fatalf("internal reason detail leaked into reply: %q", result.UserNotice)
	}
}

func TestAgentTurnRunnerFinalizesOneShotEvidenceToolAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calendar.event.add","toolInput":{"title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}`,
		`{"action":"continue","toolName":"calendar.event.add","toolInput":{"title":"휴가","startISO":"2026-05-11T00:00:00+09:00","endISO":"2026-05-14T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"calendar.event.add"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "calendar.event.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"id":"event-1","title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "내일부터 화요일까지 휴가 등록해줘",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"calendar.event.add"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed calendar turn: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one calendar write, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected no second model action after evidence success, got %d requests", len(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "calendar.event.add") {
		t.Fatal("expected completion state finalization with calendar evidence")
	}
}

func TestAgentTurnRunnerFinalizesScheduleCreateAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule.create","toolInput":{"prompt":"죄송합니다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"timeZone":"Asia/Seoul"}}`,
		`{"action":"continue","toolName":"schedule.create","toolInput":{"prompt":"죄송합니다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"timeZone":"Asia/Seoul"}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskScheduleID":"schedule-1","prompt":"죄송합니다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"nextRunAt":"2026-05-09T05:07:00Z"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "1분에 한 번씩 나한테 죄송합니다 10번 해봐",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"schedule.create"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed schedule turn: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one schedule create, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected no second model action after schedule success, got %d requests", len(languageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "schedule.create") {
		t.Fatal("expected completion state finalization with schedule evidence")
	}
}

func TestAgentTurnRunnerRejectsRepeatedScheduleCreateWithoutExecutingAgain(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule.create","toolInput":{"prompt":"죄송합니다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"timeZone":"Asia/Seoul"}}`,
		`{"action":"continue","toolName":"schedule.create","toolInput":{"timeZone":"Asia/Seoul","maxRunCount":10,"intervalSecond":60,"kind":"interval","prompt":"죄송합니다."}}`,
		finishMessageDocument("예약을 만들었습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestToolSet([]string{"schedule.create"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "schedule.create"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskScheduleID":"schedule-1","prompt":"죄송합니다.","kind":"interval","intervalSecond":60,"maxRunCount":10}`), nil
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

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stdout":"","stderr":"Error: presentation.md not found. Create presentation.md or set SRC=yourfile.md\n","timedOut":false}`), nil
		}
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return ToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "presentation.md") {
		t.Fatal("did not expect terminal precondition block event")
	}
}

func TestAgentTurnRunnerStopsRepeatedMissingEvidenceState(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build deck"}}`,
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			noToolFallbackFinishMessageDocument("텍스트로 대신 드립니다."),
			recoveryDecisionDocument("file attachment missing", "deck build failed", "stop the repeated state", "report the missing artifact"),
		},
		textResponses: []string{"PPTX 첨부를 완료하지 못했습니다. 빌드 실패 뒤에도 필수 첨부 증거가 없어 작업을 중단했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 10, RecoveryAttemptLimit: 3})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.attach"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stderr":"EACCES: permission denied, open 'deck.html'"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "피피티 만들어줘",
		ToolSet:                    toolRegistry,
		RequiredEvidenceTools:      []string{"file.attach"},
		RequiredAttachmentSuffixes: []string{".pptx"},
	})
	if errorValue != nil {
		t.Fatalf("expected repeated state stop without error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after repeated recovery state, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected no repeated terminal command, got %d calls", terminalCallCount)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.no_progress_loop_stopped", "3 consecutive") {
		t.Fatal("expected no-progress loop stop event")
	}
	if taskEventsContain(taskEvents, "max_iterations", "") {
		t.Fatal("expected loop breaker before max_iterations")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingDesignFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/DESIGN.md","content":"colors: blue"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "terminal_run", `{"exitCode":1,"stdout":"","stderr":"DESIGN.md is missing colors:\n","timedOut":false}`), nil
		}
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return ToolSuccess(`{"path":"` + input.Path + `","sizeBytes":12}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "DESIGN.md") {
		t.Fatal("did not expect DESIGN.md precondition block event")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalBeforeRequiredFileWrite(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-002","toolName":"file.write"}],"finishMessage":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 5})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(_ context.Context, invocation ToolInvocation) (ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return ToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "build deck",
		ToolSet:               toolRegistry,
		RequiredEvidenceTools: []string{"file.write"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 1 {
		t.Fatalf("expected terminal call to remain available, got %d calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "first required workspace file") {
		t.Fatal("did not expect required file.write precondition block event")
	}
}

func TestAgentTurnRunnerUsesContextualLimitReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{"검색은 시작했지만 결과 정리는 아직 남았습니다. 지금 확인된 내용은 다시 이어서 처리할 수 있게 저장했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("again"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글에서 검색해줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.UserNotice, "예산") || strings.Contains(result.UserNotice, "budget") {
		t.Fatalf("expected reply without budget wording, got %q", result.UserNotice)
	}
	if !strings.Contains(result.UserNotice, "남았습니다") {
		t.Fatalf("expected contextual limit reply, got %q", result.UserNotice)
	}
}

func TestAgentTurnRunnerRegeneratesLimitReplyWhenItClaimsAttachments(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"요청하신 HTML 파일을 생성해 첨부했습니다.",
			"작업은 시작했지만 HTML 파일을 완성하기 전에 실행 한계에 걸렸습니다. 지금까지의 작업 상태는 저장되어 다시 이어서 시도할 수 있습니다.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "started"},
			Attachments: []FileAttachment{{
				Filename:   "deck.html",
				DevicePath: "/tmp/deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html 파일 만들어줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.UserNotice, "첨부") {
		t.Fatalf("expected generated reply without attachment claim, got %q", result.UserNotice)
	}
	if !strings.Contains(result.UserNotice, "저장") {
		t.Fatalf("expected regenerated contextual reply, got %q", result.UserNotice)
	}
	if len(languageModel.textPrompts) != 2 {
		t.Fatalf("expected repair generation prompt, got %d prompts", len(languageModel.textPrompts))
	}
	if strings.Contains(languageModel.textPrompts[0], "deck.html") {
		t.Fatalf("expected blocked limit reply prompt to omit undeliverable attachments, got %s", languageModel.textPrompts[0])
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected blocked task to deliver no attachments, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerRegeneratesLimitReplyWhenItMentionsUnattachedFilename(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"아래 파일을 확인해 주세요.\n[Hermes_Agent_Slide_Part1.html]",
			"작업은 시작했지만 HTML 파일을 완성하기 전에 중단되었습니다. 다시 시도할 수 있게 상태를 저장했습니다.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("started"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html 파일 만들어줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.UserNotice, "Hermes_Agent_Slide_Part1.html") {
		t.Fatalf("expected generated reply without unattached filename, got %q", result.UserNotice)
	}
	if len(languageModel.textPrompts) != 2 {
		t.Fatalf("expected repair generation prompt, got %d prompts", len(languageModel.textPrompts))
	}
}

func TestAgentTurnRunnerReportsRawLimitErrorWhenGenerationKeepsLeakingDiagnostics(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"I used 10 minutes and 7 iterations before the budget stopped.",
			"I still used 10 minutes and 7 iterations before the budget stopped.",
			"I still used 10 minutes and 7 iterations before the budget stopped.",
		},
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
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "invalid_repair") {
		t.Fatalf("expected raw invalid limit reply error, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.llm_unavailable", "invalid_repair") {
		t.Fatal("expected admin diagnostic for invalid limit reply")
	}
}

func TestLastResortFailureNoticeDoesNotExposeFullReplyStatus(t *testing.T) {
	status := limitReplyStatus{
		Source:            "suppressed",
		Reason:            "text_recovery_failed",
		TextRecoveryError: "context deadline exceeded",
		Decision: recoveryDecision{
			WhatFailed:   strings.Repeat("build failed ", 100),
			WhatWasKnown: "source files were created",
			NextAction:   "run build again",
		},
	}
	reply, noticeStatus := (&AgentTurnRunner{}).generateLastResortFailureNotice(AgentTurnRequest{
		Prompt:           "사이트 만들어줘",
		ResponseLanguage: ResponseLanguageKorean,
	}, "max_tool_calls", status, "limit")

	if noticeStatus.Source != "raw_error" {
		t.Fatalf("expected raw fallback, got %+v", noticeStatus)
	}
	if strings.Contains(reply, "replyStatus") || strings.Contains(reply, "userReplyIntent") || len([]rune(reply)) > 520 {
		t.Fatalf("expected compact raw fallback, got %q", reply)
	}
	if !strings.Contains(reply, "max_tool_calls") || !strings.Contains(reply, "context deadline exceeded") {
		t.Fatalf("expected useful compact failure facts, got %q", reply)
	}
}

func TestAgentTurnRunnerAddsModelFacingLimitPressureWarnings(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"unknown"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 10, MaxToolCallCount: 8})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.no_progress_loop_stopped", "3 consecutive") {
		t.Fatal("expected no-progress loop stop event")
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
		ToolSet:           toolRegistry,
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

func TestAgentTurnRunnerStoresLargeToolResultAsArtifact(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"large","toolInput":{}}`,
		finishMessageDocument("summarized"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ToolResultMaxBytes: 8})
	toolRegistry := newTestToolSet([]string{"large"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "large"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(strings.Repeat("x", 32)), nil
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
	if len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)) != 1 {
		t.Fatalf("expected one task artifact, got %d", len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)))
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
	contents             []string
	toolSelections       []string
	resultVerifications  []string
	textResponses        []string
	requests             []llm.StructuredResponseRequest
	selectionRequests    []llm.StructuredResponseRequest
	verificationRequests []llm.StructuredResponseRequest
	textPrompts          []string
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
	if strings.TrimSpace(request.StructuredOutputSchema.Name) == "blueclaw_tool_selection" {
		languageModel.selectionRequests = append(languageModel.selectionRequests, request)
		index := len(languageModel.selectionRequests) - 1
		if index < len(languageModel.toolSelections) {
			return llm.StructuredResponse{Content: languageModel.toolSelections[index]}, nil
		}
		return llm.StructuredResponse{Content: `{"selectedToolIDs":[],"reason":"test default"}`}, nil
	}
	if strings.TrimSpace(request.StructuredOutputSchema.Name) == "blueclaw_result_verifier" {
		languageModel.verificationRequests = append(languageModel.verificationRequests, request)
		index := len(languageModel.verificationRequests) - 1
		if index < len(languageModel.resultVerifications) {
			return llm.StructuredResponse{Content: languageModel.resultVerifications[index]}, nil
		}
		return llm.StructuredResponse{Content: defaultResultVerificationResponse(request)}, nil
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
	errorValue         error
	localRecoveryReply string
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", languageModel.errorValue
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, languageModel.errorValue
}

func (languageModel localRecoveryFallbackLanguageModel) GenerateLocalRecoveryResponse(context.Context, string) (string, error) {
	return languageModel.localRecoveryReply, nil
}

func taskEventsContain(taskEvents []task.TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func messagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
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
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[]}`
}

func noToolFallbackFinishMessageDocument(reply string) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[],"failureResolution":"no_tool_fallback"}`
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

func finishMessageWithEvidence(reply string, observationID string, toolName string, attachmentIndex int) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":` + strconv.Quote(observationID) + `,"toolName":` + strconv.Quote(toolName) + `,"attachmentIndex":` + strconv.Itoa(attachmentIndex) + `}],"qualityReview":[]}`
}
