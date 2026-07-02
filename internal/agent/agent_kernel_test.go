package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

type intakeDecisionLanguageModel struct {
	decision TurnDecision
}

func (model intakeDecisionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("intake model only serves structured routing")
}

func (model intakeDecisionLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	document, errorValue := json.Marshal(model.decision)
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	return llm.StructuredResponse{Content: string(document)}, nil
}

func newKernelTestServices() (*AgentKernel, *task.TaskRunService) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseIntakeOptions(IntakeOptions{IsEnabled: true})
	return agentKernel, taskRunService
}

func kernelTestRequest(prompt string) AgentRequest {
	return AgentRequest{
		RequesterPersonID: "person-kernel-test",
		ConversationID:    "conversation-kernel-test",
		Prompt:            prompt,
		ResponseLanguage:  "ko",
		ToolSet:           newTestToolSet([]string{"web.search"}),
	}
}

func TestAgentKernelConsumeRouteSuppressesReply(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskComplexity:   TaskComplexitySimple,
		EffortLevel:      EffortLevelQuick,
		ResponseLanguage: "ko",
		Reason:           "lightweight acknowledgement",
	}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("고마워!"))
	if errorValue != nil {
		t.Fatalf("expected consume route to complete: %v", errorValue)
	}
	if result.TurnRoute != TurnRouteConsume {
		t.Fatalf("expected consume route, got %q", result.TurnRoute)
	}
	if !result.ReplySuppressed {
		t.Fatalf("expected reply suppression for consume route")
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task run, got %q", result.TaskRun.Status)
	}
}

func TestAgentKernelDoesNotConsumeExecutableFlowTask(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteConsume,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeResearchTask,
		TaskComplexity:        TaskComplexitySimple,
		EffortLevel:           EffortLevelStandard,
		ResponseLanguage:      "ko",
		Reason:                "사용자가 명시적으로 업무 등록을 요청함",
		RequiredEvidenceTools: []string{"task.add"},
		InitialToolNames:      []string{CapabilityInvokeToolName},
	}})

	toolCallCount := 0
	toolSet := newTestCapabilityToolSet([]string{"task.add", "task.list", "task.update"})
	toolSet.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskID":"task-1","content":"메일 페이지 앱 비밀번호 개선"}`), nil
	})
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"task.add","input":{"prompt":"메일 페이지 앱 비밀번호 개선"}}}`,
		finishMessageWithEvidence("업무를 등록했습니다.", "obs-001", "task.add", 0),
	}}
	agentKernel.UseLanguageModelProvider(languageModel)

	request := kernelTestRequest("업무 등록해줘.\n\n- 메일 페이지 앱 비밀번호 개선")
	request.ToolSet = toolSet
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected executable consume to run through task loop: %v", errorValue)
	}
	if result.TurnRoute == TurnRouteConsume || result.ReplySuppressed {
		t.Fatalf("expected task loop instead of consume, got route=%q suppressed=%v", result.TurnRoute, result.ReplySuppressed)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected task.add to run once, got %d", toolCallCount)
	}
	if result.TaskRun.Result == "consumed" {
		t.Fatalf("expected task result, got consumed")
	}
}

func TestAgentKernelPausesNeedsConfirmationDisambiguation(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteStartTask,
		Classification:        IntakeClassificationNeedsConfirmation,
		TaskComplexity:        TaskComplexityNormal,
		EffortLevel:           EffortLevelStandard,
		ResponseLanguage:      "ko",
		Reason:                "multiple matching items",
		ClarificationQuestion: "어느 보고서를 말하는 건가요?",
		ClarificationOptions: []ClarificationOption{
			{Key: "A", Label: "주간보고서", Value: "주간보고서"},
			{Key: "B", Label: "월간보고서", Value: "월간보고서"},
		},
	}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("보고서 삭제해줘"))
	if errorValue != nil {
		t.Fatalf("expected disambiguation pause to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %q", result.TaskRun.Status)
	}
	if result.UserNotice != "어느 보고서를 말하는 건가요?" {
		t.Fatalf("expected clarification question, got %q", result.UserNotice)
	}
}

func TestAgentKernelBlocksUnsupportedIntake(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationUnsupported,
		TaskComplexity:   TaskComplexityNormal,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "request is outside the available execution boundary",
		UserFacingReply:  "이 요청은 현재 권한 범위 밖이라 진행할 수 없어요.",
	}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("서버 루트 비밀번호 바꿔줘"))
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %q", result.TaskRun.Status)
	}
	if result.UserNotice != "이 요청은 현재 권한 범위 밖이라 진행할 수 없어요." {
		t.Fatalf("expected router-provided reply, got %q", result.UserNotice)
	}
}

func TestAgentKernelBlocksInvalidRequiredEvidence(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteStartTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		TaskComplexity:        TaskComplexitySimple,
		EffortLevel:           EffortLevelStandard,
		RequiredEvidenceTools: []string{"calendar.create"},
		ResponseLanguage:      "ko",
		Reason:                "calendar event creation",
	}})
	agentKernel.UseLanguageModelProvider(staticReplyProvider{content: "일정 등록 도구 연결이 맞지 않아 작업을 진행할 수 없습니다."})
	request := kernelTestRequest("7월 6일 오후 1시 스타트업월드컵 일정 추가")
	request.ToolSet = newTestToolSet([]string{"calendar.add", CapabilityInvokeToolName})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected invalid evidence to block cleanly: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %q", result.TaskRun.Status)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), requiredEvidenceInvalidEventName, "calendar.create") {
		t.Fatal("expected invalid required evidence event")
	}
}

func TestAgentKernelBlocksSideEffectWithoutRequiredEvidence(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskComplexity:   TaskComplexitySimple,
		EffortLevel:      EffortLevelStandard,
		InitialToolNames: []string{TerminalRunToolName},
		ResponseLanguage: "ko",
		Reason:           "side effect tool planned without evidence",
	}})
	agentKernel.UseLanguageModelProvider(staticReplyProvider{content: "완료 근거가 없어 작업을 진행할 수 없습니다."})
	request := kernelTestRequest("7월 6일 오후 1시 스타트업월드컵 일정 추가")
	request.ToolSet = newTestToolSet([]string{TerminalRunToolName})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected missing evidence to block cleanly: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %q", result.TaskRun.Status)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), requiredEvidenceInvalidEventName, "side-effect task has no required evidence") {
		t.Fatal("expected missing required evidence event")
	}
}

type turnRouterDecisionLanguageModel struct {
	initialDecision TurnDecision
	reaskDecision   TurnDecision
	reaskCallCount  int
}

func (model *turnRouterDecisionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("intake model only serves structured routing")
}

func (model *turnRouterDecisionLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if structuredResponseRequest.StructuredOutputSchema.Name != "blueclaw_turn_router" {
		return llm.StructuredResponse{Content: "{}"}, nil
	}
	decision := model.initialDecision
	if turnRouterRequestIsRequiredEvidenceReask(structuredResponseRequest) {
		model.reaskCallCount++
		decision = model.reaskDecision
	}
	document, errorValue := json.Marshal(decision)
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	return llm.StructuredResponse{Content: string(document)}, nil
}

func turnRouterRequestIsRequiredEvidenceReask(structuredResponseRequest llm.StructuredResponseRequest) bool {
	for _, message := range structuredResponseRequest.Messages {
		if strings.Contains(message.Content, requiredEvidenceReaskInstruction) {
			return true
		}
	}
	return false
}

func sideEffectMissingEvidenceDecision() TurnDecision {
	return TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskComplexity:   TaskComplexitySimple,
		EffortLevel:      EffortLevelStandard,
		InitialToolNames: []string{TerminalRunToolName},
		ResponseLanguage: "ko",
		Reason:           "side effect tool planned without evidence",
	}
}

func TestAgentKernelReasksAndRecoversRequiredEvidence(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: sideEffectMissingEvidenceDecision(),
		reaskDecision:   TurnDecision{RequiredEvidenceTools: []string{TerminalRunToolName}},
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)

	toolCallCount := 0
	toolSet := newTestToolSet([]string{TerminalRunToolName})
	toolSet.RegisterTool(ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
	})
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"do the side effect"}}`,
		finishMessageWithEvidence("완료했습니다.", "obs-001", TerminalRunToolName, 0),
	}}
	agentKernel.UseLanguageModelProvider(languageModel)

	request := kernelTestRequest("서버에 배포 스크립트 실행해줘")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected re-ask recovery to run the task: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusBlocked {
		t.Fatalf("expected task to proceed after re-ask recovered evidence, got blocked")
	}
	if toolCallCount != 1 {
		t.Fatalf("expected terminal.run to run once, got %d", toolCallCount)
	}
	if intakeLanguageModel.reaskCallCount != 1 {
		t.Fatalf("expected exactly one re-ask call, got %d", intakeLanguageModel.reaskCallCount)
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, requiredEvidenceReaskEventName, `"didRecoverEvidence":true`) {
		t.Fatal("expected reask event reporting recovered evidence")
	}
	if !taskEventsContain(taskEvents, "agent.intake", TerminalRunToolName) {
		t.Fatal("expected rebuilt intake decision to carry the recovered evidence")
	}
}

func TestAgentKernelBlocksWhenReaskStillReturnsNoEvidence(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: sideEffectMissingEvidenceDecision(),
		reaskDecision:   sideEffectMissingEvidenceDecision(),
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)
	agentKernel.UseLanguageModelProvider(staticReplyProvider{content: "완료 근거가 없어 작업을 진행할 수 없습니다."})

	request := kernelTestRequest("서버에 배포 스크립트 실행해줘")
	request.ToolSet = newTestToolSet([]string{TerminalRunToolName})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected still-missing evidence to block cleanly: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %q", result.TaskRun.Status)
	}
	if intakeLanguageModel.reaskCallCount != 1 {
		t.Fatalf("expected exactly one re-ask attempt, got %d", intakeLanguageModel.reaskCallCount)
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, requiredEvidenceInvalidEventName, "side-effect task has no required evidence") {
		t.Fatal("expected missing required evidence event")
	}
	if !taskEventsContain(taskEvents, requiredEvidenceReaskEventName, `"wasAttempted":true`) {
		t.Fatal("expected reask event reporting the attempt")
	}
	if taskEventsContain(taskEvents, requiredEvidenceReaskEventName, `"didRecoverEvidence":true`) {
		t.Fatal("did not expect reask event to report recovered evidence")
	}
}

func TestAgentKernelGeneratesIntakeNoticeWhenRouterReplyMissing(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationUnsupported,
		TaskComplexity:   TaskComplexityNormal,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "request is outside the available execution boundary",
	}})
	agentKernel.UseLanguageModelProvider(staticReplyProvider{content: "지금 실행 범위에서는 안전하게 처리할 수 없어요. 요청을 좁혀주시면 도와드릴게요."})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("시스템 패키지 전부 지워줘"))
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.UserNotice != "지금 실행 범위에서는 안전하게 처리할 수 없어요. 요청을 좁혀주시면 도와드릴게요." {
		t.Fatalf("expected language-model intake notice, got %q", result.UserNotice)
	}
}

func TestAgentKernelFallsBackToReasonWhenIntakeNoticeModelsFail(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationUnsupported,
		TaskComplexity:   TaskComplexityNormal,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "request is outside the available execution boundary",
	}})
	agentKernel.UseLanguageModelProvider(failingLanguageModel{})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("시스템 패키지 전부 지워줘"))
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if !strings.Contains(result.UserNotice, "execution boundary") {
		t.Fatalf("expected compact reason fallback, got %q", result.UserNotice)
	}
}

func TestAgentKernelRunsBoundedTaskThroughTurnRunner(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskComplexity:   TaskComplexitySimple,
		EffortLevel:      EffortLevelQuick,
		ResponseLanguage: "ko",
		Reason:           "direct answer",
	}})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{finishMessageDocument("오늘은 수요일이에요.")}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("오늘 무슨 요일이야?"))
	if errorValue != nil {
		t.Fatalf("expected bounded run to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task run, got %q", result.TaskRun.Status)
	}
	if !strings.Contains(result.TaskRun.Result, "수요일") {
		t.Fatalf("expected finish message in result, got %q", result.TaskRun.Result)
	}
}

func TestSitePrototypeIntakePromotesToDeepLimits(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	intakeDecision := promoteSitePrototypeEffort(AgentRequest{}, IntakeDecision{
		EffortLevel:         EffortLevelStandard,
		OutputKind:          OutputKindSite,
		SiteRequestEvidence: "웹사이트",
	})

	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)
	deepProfile := EffortLimitProfileForLevel(EffortLevelDeep)

	if effortLevelRank(turnOptions.EffortLevel) < effortLevelRank(EffortLevelDeep) {
		t.Fatalf("expected at least deep effort, got %q", turnOptions.EffortLevel)
	}
	if turnOptions.MaxIterationCount < deepProfile.MaxIterationCount {
		t.Fatalf("expected deep iteration limit, got %d", turnOptions.MaxIterationCount)
	}
	if turnOptions.MaxToolCallCount < deepProfile.MaxToolCallCount {
		t.Fatalf("expected deep tool call limit, got %d", turnOptions.MaxToolCallCount)
	}
}

func TestAgentKernelCompleteLaunchFailureRedactsRawError(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseLanguageModelProvider(failingLanguageModel{})

	result := agentKernel.CompleteLaunchFailure(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-kernel-test",
		ConversationID:    "conversation-kernel-test",
		Prompt:            "발표자료 만들어줘",
		ResponseLanguage:  "ko",
	}, "launch", "build_tool_set", errors.New("tool registry mismatch token=launch-secret"))

	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task run, got %q", result.TaskRun.Status)
	}
	if result.FailureNotice.Source != "raw_error" {
		t.Fatalf("expected raw error notice, got %+v", result.FailureNotice)
	}
	if !strings.Contains(result.UserNotice, "tool registry mismatch") {
		t.Fatalf("expected failure detail in notice, got %q", result.UserNotice)
	}
	if strings.Contains(result.UserNotice, "launch-secret") {
		t.Fatalf("expected secret redaction, got %q", result.UserNotice)
	}
}
