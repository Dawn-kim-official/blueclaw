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
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeResearchTask,
		TaskComplexity:   TaskComplexitySimple,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "사용자가 명시적으로 업무 등록을 요청함",
		WorkKinds:        []string{WorkKindFlowTask},
		InitialToolNames: []string{"task.add", "task.list", "task.update"},
	}})

	toolCallCount := 0
	toolSet := newTestToolSet([]string{"task.add", "task.list", "task.update"})
	toolSet.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskID":"task-1","content":"메일 페이지 앱 비밀번호 개선"}`), nil
	})
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.add","toolInput":{"prompt":"메일 페이지 앱 비밀번호 개선"}}`,
		finishMessageDocument("업무를 등록했습니다."),
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

func TestAgentKernelPausesNeedsConfirmationIntake(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationNeedsConfirmation,
		TaskComplexity:   TaskComplexityNormal,
		EffortLevel:      EffortLevelStandard,
		ResponseLanguage: "ko",
		Reason:           "request appears too large for one bounded execution",
		UserFacingReply:  "범위를 조금 좁혀서 다시 요청해 주세요.",
	}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("회사 데이터 전부 정리해줘"))
	if errorValue != nil {
		t.Fatalf("expected intake pause to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %q", result.TaskRun.Status)
	}
	if result.UserNotice != "범위를 조금 좁혀서 다시 요청해 주세요." {
		t.Fatalf("expected router-provided reply, got %q", result.UserNotice)
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
		EffortLevel: EffortLevelStandard,
		WorkKinds:   []string{WorkKindSitePrototype},
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
