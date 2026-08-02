package bluecollar

import (
	"context"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

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
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_report", "structured model failed") {
		t.Fatal("expected failure report event with the raw failure reason")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_report", `"generation"`) {
		t.Fatal("expected failure report event with generation status")
	}
}

func TestAgentTurnRunnerRepairsInvalidFailureReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"fail","reason":"pptx build failed"}`,
		recoveryDecisionDocument("check presentation temporary directory handling", "explain the exact failed stages"),
	}, textResponses: []string{
		"브라우저 연결 문제와 시스템 환경 오류가 있어 파일이 생성되지 않았습니다.",
		"PPTX는 첨부되지 않았습니다. 브라우저 열기는 Companion 미연결로 실패했고, 슬라이드 빌드는 Marp 임시 HTML 생성 권한 문제로 중단되어 presentation 임시 디렉터리 설정 확인이 필요합니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "https://example.com 보고 사업계획서 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	})

	if errorValue != nil {
		t.Fatalf("expected repaired failure result, got error: %v", errorValue)
	}
	if result.UserNotice != languageModel.textResponses[0] {
		t.Fatalf("expected generated failure reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
	}
	assertRecoveryDecisionTokenBudget(t, languageModel.requests)
}

func assertRecoveryDecisionTokenBudget(t *testing.T, requests []model.StructuredResponseRequest) {
	t.Helper()
	for _, request := range requests {
		if request.StructuredOutputSchema.Name != "blueclaw_recovery_decision" {
			continue
		}
		if request.GenerationOptions.MaxTokens == nil || *request.GenerationOptions.MaxTokens != recoveryDecisionMaxTokens {
			t.Fatalf("expected recovery decision max tokens %d, got %+v", recoveryDecisionMaxTokens, request.GenerationOptions)
		}
		return
	}
	t.Fatal("expected recovery decision request")
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
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "llm action failed: model unavailable") {
		t.Fatalf("expected raw error reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "raw_error") {
		t.Fatal("expected raw error failure reply event")
	}
}

func TestAgentTurnRunnerHonorsCallerDeadlineDuringFailureReply(t *testing.T) {
	languageModel := &blockingFailureWordingLanguageModel{failFirstStructuredCall: true}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 4,
	})
	runContext, cancelRun := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelRun()
	startedAt := time.Now()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "분기 결산 업무를 등록해줘",
		ResponseLanguage:  ResponseLanguageKorean,
	})

	if errorValue != nil {
		t.Fatalf("expected bounded failure result, got %v", errorValue)
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Fatalf("expected one bounded failure grace, took %s", elapsed)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed || result.FailureNotice.Source != "raw_error" {
		t.Fatalf("expected failed raw-error result, got %+v", result)
	}
	if result.UserNotice == "" || result.TaskRun.Result != result.UserNotice {
		t.Fatalf("expected persisted user notice, got result=%q notice=%q", result.TaskRun.Result, result.UserNotice)
	}
	assertSharedFailureReplyDeadline(t, languageModel)
	if languageModel.requesterPersonID != "person-1" {
		t.Fatalf("expected requester metadata in failure finalization, got %q", languageModel.requesterPersonID)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.failure_report", `"source":"raw_error"`) {
		t.Fatal("expected raw-error failure report event")
	}
	if !taskEventsContain(taskEvents, "agent.failure_reply", `"source":"raw_error"`) {
		t.Fatal("expected raw-error failure reply event")
	}
}

func TestAgentTurnRunnerFinalizesFailureNoticeBeforeTerminalStatus(t *testing.T) {
	recoveryChatStarted := make(chan struct{})
	recoveryChatRelease := make(chan struct{})
	languageModel := &blockingFailureWordingLanguageModel{
		failFirstStructuredCall: true,
		recoveryChatStarted:     recoveryChatStarted,
		recoveryChatRelease:     recoveryChatRelease,
		recoveryChatReply:       "업무를 추가하지 못했습니다. 같은 요청을 다시 보내주시면 재시도하겠습니다.",
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 4,
	})
	resultChannel := make(chan AgentTurnResult, 1)

	go func() {
		result, _ := services.runner.RunTurn(context.Background(), AgentTurnRequest{
			RequesterPersonID: "person-1",
			ConversationID:    "conversation-1",
			Prompt:            "분기 결산 업무를 등록해줘",
			ResponseLanguage:  ResponseLanguageKorean,
		})
		resultChannel <- result
	}()

	select {
	case <-recoveryChatStarted:
	case <-time.After(time.Second):
		t.Fatal("expected failure notice generation to start")
	}
	taskRuns := services.taskRunService.ListTaskRunByPersonID("person-1")
	if len(taskRuns) != 1 || taskRuns[0].Status != taskstate.TaskStatusRunning {
		t.Fatalf("expected task to remain running during failure notice generation, got %+v", taskRuns)
	}

	close(recoveryChatRelease)
	select {
	case result := <-resultChannel:
		if result.TaskRun.Status != taskstate.TaskStatusFailed || result.UserNotice != languageModel.recoveryChatReply {
			t.Fatalf("expected failed task with generated notice, got %+v", result)
		}
		if result.TaskRun.Result != result.UserNotice || result.FailureNotice.Source != "generated" {
			t.Fatalf("expected persisted generated failure notice, got %+v", result)
		}
		assertFailureNoticeEventsBeforeTerminalStatus(t, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	case <-time.After(time.Second):
		t.Fatal("expected failure finalization after wording completed")
	}
}

func assertFailureNoticeEventsBeforeTerminalStatus(t *testing.T, taskEvents []taskstate.TaskEvent) {
	t.Helper()
	eventIndexes := map[string]int{}
	for eventIndex, taskEvent := range taskEvents {
		if taskEvent.Name == "agent.failure_report" || taskEvent.Name == "agent.failure_reply" || taskEvent.Name == "task.paused" {
			eventIndexes[taskEvent.Name] = eventIndex
		}
	}
	failureReportIndex, hasFailureReport := eventIndexes["agent.failure_report"]
	failureReplyIndex, hasFailureReply := eventIndexes["agent.failure_reply"]
	terminalStatusIndex, hasTerminalStatus := eventIndexes["task.paused"]
	if !hasFailureReport || !hasFailureReply || !hasTerminalStatus {
		t.Fatalf("expected failure notice and terminal events, got %+v", eventIndexes)
	}
	if failureReportIndex >= terminalStatusIndex || failureReplyIndex >= terminalStatusIndex {
		t.Fatalf("expected failure notice events before terminal status, got %+v", eventIndexes)
	}
}

func TestAgentTurnRunnerHonorsCallerDeadlineDuringStallReply(t *testing.T) {
	languageModel := &blockingFailureWordingLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "분기 결산 업무를 등록해줘")
	replyContext, cancelReply := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelReply()
	startedAt := time.Now()

	notice, _, hasReply := services.runner.generateStallPauseNotice(replyContext, taskRun.TaskRunID, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            taskRun.Prompt,
		ResponseLanguage:  ResponseLanguageKorean,
	}, "repeated actions without progress", nil, nil, ExecutionState{})

	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Fatalf("expected bounded stall reply grace, took %s", elapsed)
	}
	if hasReply || notice.Source != "raw_error" {
		t.Fatalf("expected unsendable raw-error stall notice, got notice=%+v hasReply=%v", notice, hasReply)
	}
	assertSharedFailureReplyDeadline(t, languageModel)
}

func TestAgentTurnRunnerHonorsCallerDeadlineDuringMaxIterationsReply(t *testing.T) {
	languageModel := &blockingFailureWordingLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "분기 결산 업무를 등록해줘")
	if _, errorValue := services.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatalf("expected running task: %v", errorValue)
	}
	replyContext, cancelReply := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelReply()
	startedAt := time.Now()

	result, errorValue := services.runner.stopForLimit(replyContext, taskRun.TaskRunID, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            taskRun.Prompt,
		ResponseLanguage:  ResponseLanguageKorean,
	}, "max_iterations", nil, nil, ExecutionState{}, 4, 0)

	if errorValue != nil {
		t.Fatalf("expected bounded max-iterations result, got %v", errorValue)
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Fatalf("expected bounded max-iterations reply grace, took %s", elapsed)
	}
	if result.TaskRun.Status != taskstate.TaskStatusBlocked || result.FailureNotice.Source != "raw_error" {
		t.Fatalf("expected blocked raw-error limit result, got %+v", result)
	}
	if result.UserNotice == "" || result.TaskRun.Result != result.UserNotice {
		t.Fatalf("expected persisted limit notice, got result=%q notice=%q", result.TaskRun.Result, result.UserNotice)
	}
	assertSharedFailureReplyDeadline(t, languageModel)
}

func assertSharedFailureReplyDeadline(t *testing.T, languageModel *blockingFailureWordingLanguageModel) {
	t.Helper()
	if languageModel.recoveryChatCalls != 1 || languageModel.legacyCalls != 0 {
		t.Fatalf("expected one bounded recovery Chat and no legacy call, got chat=%d legacy=%d", languageModel.recoveryChatCalls, languageModel.legacyCalls)
	}
	if languageModel.decisionDeadline.IsZero() || !languageModel.decisionDeadline.Equal(languageModel.recoveryChatDeadline) {
		t.Fatalf("expected decision and wording to share one deadline, got %v and %v", languageModel.decisionDeadline, languageModel.recoveryChatDeadline)
	}
}

type blockingFailureWordingLanguageModel struct {
	failFirstStructuredCall bool
	structuredCalls         int
	recoveryChatCalls       int
	legacyCalls             int
	requesterPersonID       string
	decisionDeadline        time.Time
	recoveryChatDeadline    time.Time
	recoveryChatStarted     chan struct{}
	recoveryChatRelease     chan struct{}
	recoveryChatReply       string
}

func (languageModel *blockingFailureWordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	languageModel.legacyCalls++
	return "", errors.New("legacy recovery should not run after cancellation")
}

func (languageModel *blockingFailureWordingLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.structuredCalls++
	if languageModel.failFirstStructuredCall && languageModel.structuredCalls == 1 {
		return model.StructuredResponse{}, errors.New("action schema validation failed")
	}
	languageModel.requesterPersonID = model.RequestContextFromContext(responseContext).RequesterPersonID
	languageModel.decisionDeadline, _ = responseContext.Deadline()
	return model.StructuredResponse{Content: recoveryDecisionDocument(
		"같은 요청을 다시 시도한다",
		"업무를 추가하지 못한 사실과 재시도 방법을 설명한다",
	)}, nil
}

func (languageModel *blockingFailureWordingLanguageModel) GenerateRecoveryChatCompletion(responseContext context.Context, _ model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	languageModel.recoveryChatCalls++
	languageModel.recoveryChatDeadline, _ = responseContext.Deadline()
	if languageModel.recoveryChatStarted != nil {
		close(languageModel.recoveryChatStarted)
	}
	if languageModel.recoveryChatRelease != nil {
		select {
		case <-languageModel.recoveryChatRelease:
			return model.ChatCompletionResponse{
				FinishReason:    "stop",
				SelectedBackend: "remote",
				Message:         model.ChatCompletionMessage{Role: "assistant", Content: languageModel.recoveryChatReply},
			}, nil
		case <-responseContext.Done():
			return model.ChatCompletionResponse{}, responseContext.Err()
		}
	}
	<-responseContext.Done()
	return model.ChatCompletionResponse{}, responseContext.Err()
}

func TestAgentTurnRunnerUsesLocalRecoveryWhenRemoteAndRecoveryModelsFail(t *testing.T) {
	languageModel := &localRecoveryFallbackLanguageModel{
		errorValue: errors.New("model unavailable"),
		localReply: "I could not complete that request, but I can try again if you send it once more.",
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "Summarize the current thread.",
		ResponseLanguage:  ResponseLanguageEnglish,
	})
	if errorValue != nil {
		t.Fatalf("expected local recovery result: %v", errorValue)
	}
	if result.ReplySuppressed || result.UserNotice != languageModel.localReply {
		t.Fatalf("expected local recovery reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if len(languageModel.localPrompts) == 0 {
		t.Fatal("expected local recovery prompt")
	}
	if languageModel.legacyCalls != 0 {
		t.Fatalf("expected local recovery Chat without legacy text calls, got %d", languageModel.legacyCalls)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "local_generated") {
		t.Fatal("expected local generated failure reply event")
	}
}

func TestAgentTurnRunnerDoesNotUseDeterministicCapabilityFallbackWhenActionModelFails(t *testing.T) {
	languageModel := failingRecoveryLanguageModel{errorValue: errors.New("structured action unavailable")}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"schedule_list", "file_write", "schedule_create"})
	for _, toolName := range toolRegistry.ListToolNames() {
		currentToolName := toolName
		registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: currentToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("unused"), nil
		})
	}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:    "person-1",
		RequesterCallingName: "샘플",
		ConversationID:       "conversation-1",
		Prompt:               "너 뭐 할줄 알아? 짧게 설명해봐",
		ResponseLanguage:     ResponseLanguageKorean,
		ToolSet:              toolRegistry,
		PinnedToolNames:      toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected failed turn without deterministic capability reply: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed || result.ReplySuppressed || !strings.Contains(result.UserNotice, "structured action unavailable") {
		t.Fatalf("expected raw failed task notice, got status=%s reply=%q suppressed=%v", result.TaskRun.Status, result.UserNotice, result.ReplySuppressed)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.capability_fallback", "schedule_list") {
		t.Fatal("expected no deterministic capability fallback event")
	}
}

func TestAgentTurnRunnerUsesNaturalCaptchaFailureReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"browser_snapshot","toolInput":{}}`,
		`{"action":"fail","reason":"blocked_by_captcha"}`,
		recoveryDecisionDocument("ask for another source or direct access", "explain that automated access was blocked"),
	}, textResponses: []string{
		"샘플 님, 날씨를 확인하려고 시도했지만 페이지가 자동화 접근을 막아서 정확한 확인을 끝내지 못했어요. 다른 출처를 주시면 거기서 다시 확인해볼게요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"browser_snapshot"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "browser_snapshot"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolFailureResult(toolcontract.FailureInteractionRequired, toolcontract.FailureCodes.InteractionRequired, "browser_snapshot", "automated access requires user interaction"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterCallingName:  "샘플",
		ConversationID:        "conversation-1",
		Prompt:                "내일 서울 날씨 검색해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser_snapshot"},
	})
	if errorValue != nil {
		t.Fatalf("expected dynamic captcha result, got error: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "샘플 님") || !strings.Contains(result.UserNotice, "자동화 접근을 막아서") {
		t.Fatalf("expected natural captcha reply, got %q", result.UserNotice)
	}
	if strings.Contains(result.UserNotice, "처리할 수 없습니다") || strings.Contains(result.UserNotice, "오류가 발생했습니다") {
		t.Fatalf("expected non-mechanical captcha reply, got %q", result.UserNotice)
	}
}

func TestFailureReportRejectsMissingUsedFailureFacts(t *testing.T) {
	result := validateFailureReportAction(turnActionDocument{
		Action:            "fail",
		Reason:            "schedule lookup failed",
		FailureResolution: failureResolutionFailureReport,
	}, failureReportFacts{
		Attempts: []failureReportAttempt{{
			ToolName:     "schedule_list",
			InputSummary: "today",
			ErrorCode:    toolcontract.FailureCodes.OperationFailed.String(),
			FailureStage: "schedule_lookup",
			Message:      "schedule storage unavailable",
		}},
		BudgetState: "failure_report_required",
	})
	if result.IsSatisfied {
		t.Fatal("expected missing usedFailureFacts to be rejected")
	}
}

func TestAgentTurnRunnerPreservesStructuredToolFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}`,
		failureReportDocument("recipient missing", "message_send", "정국", toolcontract.FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
		recoveryDecisionDocument("inspect candidate recipients before retrying", "report the exact failure stage and code"),
	}, textResponses: []string{
		"recipient_resolve/not_found 단계에서 수신자를 찾지 못해 DM을 보내지 못했습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message_send", "message_context"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_send"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterName:         "이샘플",
		ConversationID:        "conversation-1",
		Prompt:                "정국에게 DM 보내줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message_send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message_send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "recipient_resolve/not_found") {
		t.Fatalf("expected structured failure in final reply, got %q", result.UserNotice)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "tool.message_send.result", toolcontract.FailureCodes.NotFound.String()) {
		t.Fatal("expected structured tool failure event")
	}
}

func TestAgentTurnRunnerDeliversSafeDegradedFailureReplyWithoutStageAndCode(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}`,
			failureReportDocument("recipient missing", "message_send", "정국", toolcontract.FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
			recoveryDecisionDocument("inspect candidate recipients before retrying", "report the exact failure stage and code"),
		},
		textResponses: []string{"요청을 처리하지 못했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message_send"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_send"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})
	existingTaskRun := services.taskRunService.CreateTaskRunWithOrigin("person-1", taskstate.TaskRunOrigin{ConversationID: "conversation-1"}, "정국에게 DM 보내줘")
	services.taskEventService.AppendTaskEvent(existingTaskRun.TaskRunID, "agent.no_progress_loop_paused", "previous stall pause")

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이샘플",
		ConversationID:    "conversation-1",
		Prompt:            "정국에게 DM 보내줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		ExistingTaskRunID: existingTaskRun.TaskRunID,
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task on exhausted recovery, got %s", result.TaskRun.Status)
	}
	if result.UserNotice != "요청을 처리하지 못했습니다." || result.ReplySuppressed {
		t.Fatalf("expected safe degraded reply to be delivered, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
	}
}

func TestAgentTurnRunnerAcceptsGeneratedStructuredFailureReplyWithStageAndCode(t *testing.T) {
	generatedReply := "recipient_resolve/not_found 단계에서 수신자를 찾지 못했습니다."
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}`,
			failureReportDocument("recipient missing", "message_send", "정국", toolcontract.FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
			recoveryDecisionDocument("inspect candidate recipients before retrying", "report the exact failure stage and code"),
		},
		textResponses: []string{generatedReply},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message_send"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_send"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterName:     "이샘플",
		ConversationID:    "conversation-1",
		Prompt:            "정국에게 DM 보내줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task after exhausted recovery, got %s", result.TaskRun.Status)
	}
	if result.UserNotice != generatedReply {
		t.Fatalf("expected generated reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
	}
}

func TestLimitFailureNoticePreservesTypedFailureFacts(t *testing.T) {
	observations := []turnObservation{
		newFailureObservation("obs-001", "continue", "terminal_run", `{"exitCode":1,"stderr":"mkdir: cannot create directory 'artifacts': Permission denied"}`, toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "terminal_run"),
	}
	report := buildFailureReport(AgentTurnRequest{Prompt: "pptx 만들어줘"}, "task-1", "limit", "max_iterations", observations, nil, ExecutionState{}, recoveryDecision{})
	prompt := buildFailureNoticePrompt(report)

	for _, expectedText := range []string{
		"terminal_run",
		"Permission denied",
	} {
		if !strings.Contains(prompt, expectedText) {
			t.Fatalf("expected limit prompt to contain %q, got %s", expectedText, prompt)
		}
	}
}

func TestRequiredArtifactFailureNoticeForbidsTextSubstitute(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:                     "사업계획서 발표 자료 pptx 만들어줘",
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}

	report := buildFailureReport(request, "task-1", "failure", "terminal_run failed", nil, nil, ExecutionState{}, recoveryDecision{})
	prompt := buildFailureNoticePrompt(report)

	if !strings.Contains(prompt, "Do not offer chat text as a substitute") {
		t.Fatalf("expected required artifact prompt to forbid chat text substitute, got %s", prompt)
	}
	if !strings.Contains(prompt, `"artifactRequired":true`) {
		t.Fatalf("expected typed artifact requirement in compact context, got %s", prompt)
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
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "loop"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("again"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글에서 검색해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
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

func TestAgentTurnRunnerLimitReplyPromptHidesUndeliveredAttachments(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"작업은 시작했지만 HTML 파일을 완성하기 전에 실행 한계에 걸렸습니다. 지금까지의 작업 상태는 저장되어 다시 이어서 시도할 수 있습니다.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "loop"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "started"},
			Attachments: []toolcontract.FileAttachment{{
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
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.UserNotice, "첨부") {
		t.Fatalf("expected generated reply without attachment claim, got %q", result.UserNotice)
	}
	if !strings.Contains(result.UserNotice, "저장") {
		t.Fatalf("expected contextual reply, got %q", result.UserNotice)
	}
	if len(languageModel.textPrompts) != 1 {
		t.Fatalf("expected one generation prompt, got %d prompts", len(languageModel.textPrompts))
	}
	if strings.Contains(languageModel.textPrompts[0], "deck.html") {
		t.Fatalf("expected blocked limit reply prompt to omit undeliverable attachments, got %s", languageModel.textPrompts[0])
	}
	if !strings.Contains(languageModel.textPrompts[0], "Do not claim an attachment or completed artifact exists unless attachment filenames are listed") {
		t.Fatalf("expected prompt to describe attachment evidence boundary, got %s", languageModel.textPrompts[0])
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected blocked task to deliver no attachments, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerDoesNotRegenerateLimitReplyFromStringPatterns(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"작업 결과는 sandbox:/mnt/data/Hermes_Agent_Slide_Part1.html에 있습니다.",
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 1})
	toolRegistry := newTestToolSet([]string{"loop"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "loop"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("started"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "html 파일 만들어줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "Hermes_Agent_Slide_Part1.html") {
		t.Fatalf("expected model wording to pass through unchanged, got %q", result.UserNotice)
	}
	if len(languageModel.textPrompts) != 1 {
		t.Fatalf("expected one model wording call without deterministic repair, got %d prompts", len(languageModel.textPrompts))
	}
}
