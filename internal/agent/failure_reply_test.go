package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/task"
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
		recoveryDecisionDocument("browser and slide build failed", "no PPTX attachment exists", "check presentation temporary directory handling", "explain the exact failed stages"),
	}, textResponses: []string{
		"브라우저 연결 문제와 시스템 환경 오류가 있어 파일이 생성되지 않았습니다.",
		"PPTX는 첨부되지 않았습니다. 브라우저 열기는 Companion 미연결로 실패했고, 슬라이드 빌드는 Marp 임시 HTML 생성 권한 문제로 중단되어 presentation 임시 디렉터리 설정 확인이 필요합니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "https://example.com 보고 사업계획서 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file.deliver"},
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
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "llm action failed: model unavailable") {
		t.Fatalf("expected raw error reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "raw_error") {
		t.Fatal("expected raw error failure reply event")
	}
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "local_generated") {
		t.Fatal("expected local generated failure reply event")
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
		"샘플 님, 날씨를 확인하려고 시도했지만 페이지가 자동화 접근을 막아서 정확한 확인을 끝내지 못했어요. 다른 출처를 주시면 거기서 다시 확인해볼게요.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"browser.snapshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "browser_snapshot", "blocked_by_captcha: bot-detection wall"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterCallingName:  "샘플",
		ConversationID:        "conversation-1",
		Prompt:                "내일 서울 날씨 검색해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"browser.snapshot"},
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
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"message.send","input":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}}`,
		failureReportDocument("recipient missing", "message.send", "정국", FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
		recoveryDecisionDocument("recipient lookup failed", "recipient_resolve/not_found was returned", "inspect candidate recipients before retrying", "report the exact failure stage and code"),
	}, textResponses: []string{
		"recipient_resolve/not_found 단계에서 수신자를 찾지 못해 DM을 보내지 못했습니다.",
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send", "message.context"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		RequesterName:         "이샘플",
		ConversationID:        "conversation-1",
		Prompt:                "정국에게 DM 보내줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message.send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected structured failure result: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "recipient_resolve/not_found") {
		t.Fatalf("expected structured failure in final reply, got %q", result.UserNotice)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "tool.capability.invoke.result", FailureCodes.NotFound.String()) {
		t.Fatal("expected structured tool failure event")
	}
}

func TestAgentTurnRunnerDeliversSafeDegradedFailureReplyWithoutStageAndCode(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"message.send","input":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}}`,
			failureReportDocument("recipient missing", "message.send", "정국", FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
			recoveryDecisionDocument("recipient lookup failed", "recipient_resolve/not_found was returned", "inspect candidate recipients before retrying", "report the exact failure stage and code"),
		},
		textResponses: []string{"요청을 처리하지 못했습니다."},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return structuredFailureToolResult("recipient not found", "approved active Mattermost recipient was not found", "recipient_not_found", "recipient_resolve", false, false), nil
	})
	existingTaskRun := services.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "conversation-1"}, "정국에게 DM 보내줘")
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
	if result.TaskRun.Status != task.TaskStatusFailed {
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
			`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"message.send","input":{"targetType":"directMessage","personHint":"정국","message":"확인 부탁해"}}}`,
			failureReportDocument("recipient missing", "message.send", "정국", FailureCodes.NotFound.String(), "recipient_resolve", "approved active Mattermost recipient was not found"),
			recoveryDecisionDocument("recipient lookup failed", "recipient_resolve/not_found was returned", "inspect candidate recipients before retrying", "report the exact failure stage and code"),
		},
		textResponses: []string{generatedReply},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 1, RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"message.send"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task after exhausted recovery, got %s", result.TaskRun.Status)
	}
	if result.UserNotice != generatedReply {
		t.Fatalf("expected generated reply, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_reply", "generated") {
		t.Fatal("expected generated failure reply event")
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
		RequiredEvidenceTools:      []string{"file.deliver"},
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
		Prompt:                     "https://example.com 보고 사업계획서 발표자료 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file.deliver"},
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
		Prompt:                     "https://example.com 보고 사업계획서 발표자료 ppt로 만들어줘",
		RequiredEvidenceTools:      []string{"file.deliver"},
		RequiredAttachmentSuffixes: []string{".pptx"},
		OutcomeContract:            OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}
	observations := []turnObservation{
		newFailureObservation("obs-001", "continue", "browser_handoff.openURL", "Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.", FailureExternalService, FailureCodes.OperationFailed, "browser_handoff"),
		newFailureObservation("obs-002", "continue", "terminal.run", `[ ERROR ] Failed converting Markdown. (EACCES: permission denied, open '/workspace/tmp-457-sVDK32cv3ara-.html')`, FailureExternalService, FailureCodes.OperationFailed, "terminal_run"),
	}
	reply := "PPTX는 첨부되지 않았습니다. 브라우저 열기는 Companion 미연결로 실패했고, 슬라이드 빌드는 Marp 임시 HTML 생성 권한 문제로 중단되어 presentation 임시 디렉터리 설정 확인이 필요합니다."

	if failureReplyIsInvalidForRequest(reply, request, "no artifact attached", observations, nil) {
		t.Fatal("expected required artifact failure reply with concrete natural facts to be accepted")
	}
}

func TestRequiredArtifactPromptsForbidTextSubstitute(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:                     "사업계획서 발표 자료 pptx 만들어줘",
		RequiredEvidenceTools:      []string{"file.deliver"},
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

func TestAgentTurnRunnerRegeneratesLimitReplyWhenItMentionsNonDeliverableLocator(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"loop","toolInput":{}}`,
		},
		textResponses: []string{
			"작업 결과는 sandbox:/mnt/data/Hermes_Agent_Slide_Part1.html에 있습니다.",
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
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if strings.Contains(result.UserNotice, "Hermes_Agent_Slide_Part1.html") {
		t.Fatalf("expected generated reply without non-deliverable locator, got %q", result.UserNotice)
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
			"replyStatus: source=suppressed; text_recovery_error=context deadline exceeded; /home/site/draft",
			"replyStatus: source=suppressed; text_recovery_error=context deadline exceeded; /home/site/draft",
			"replyStatus: source=suppressed; text_recovery_error=context deadline exceeded; /home/site/draft",
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
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected limit result, got error: %v", errorValue)
	}
	if result.ReplySuppressed || !strings.Contains(result.UserNotice, "Progress was saved") {
		t.Fatalf("expected raw limit reply, got reply=%q suppressed=%v", result.UserNotice, result.ReplySuppressed)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_reply", "raw_error") {
		t.Fatal("expected raw error limit reply event")
	}
}
