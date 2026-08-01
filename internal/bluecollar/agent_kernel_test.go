package bluecollar

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
	"github.com/Dawn-kim-official/blueclaw/internal/taskstate"
)

type intakeDecisionLanguageModel struct {
	decision TurnDecision
}

func (languageModel intakeDecisionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("intake model only serves structured routing")
}

func (languageModel intakeDecisionLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	document, errorValue := json.Marshal(languageModel.decision)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	return model.StructuredResponse{Content: string(document)}, nil
}

func newKernelTestServices() (*AgentKernel, *taskstate.TaskRunService) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
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
		ToolSet:           newTestToolSet([]string{"web_search"}),
	}
}

func TestApprovalContinuationRestoresSelectedToolDecision(t *testing.T) {
	request := AgentRequest{
		PinnedToolNames:  []string{"file_read"},
		PinnedSkillNames: []string{"calendar"},
		ActiveGoal: ActiveGoal{
			SelectedToolNames:  []string{"message_send"},
			SelectedSkillNames: []string{"direct-message"},
		},
	}

	restoredRequest := restorePersistedToolSelection(request)

	if !sameStringSet(restoredRequest.PinnedToolNames, []string{"file_read", "message_send"}) {
		t.Fatalf("expected selected tool decision to be restored, got %+v", restoredRequest.PinnedToolNames)
	}
	if !sameStringSet(restoredRequest.PinnedSkillNames, []string{"calendar", "direct-message"}) {
		t.Fatalf("expected selected skill decision to be restored, got %+v", restoredRequest.PinnedSkillNames)
	}
}

func TestFreshTaskPinsRouterInitialAndRequiredEvidenceTools(t *testing.T) {
	pinnedToolNames := pinnedToolNamesForResolvedRequest(
		[]string{"manual.tool"},
		[]string{"manual.tool", "previous.tool"},
		[]string{"file_read"},
		[]string{"task_add"},
		true,
	)

	if !sameStringSet(pinnedToolNames, []string{"manual.tool", "file_read", "task_add"}) {
		t.Fatalf("expected manual, router, and required evidence tools, got %+v", pinnedToolNames)
	}
	activeGoal := activeGoalForTurn(AgentRequest{PinnedToolNames: pinnedToolNames}, OutcomeContract{}, ExecutionPlan{}, false)
	if !sameStringSet(activeGoal.SelectedToolNames, []string{"manual.tool", "file_read", "task_add"}) {
		t.Fatalf("expected the typed working set to persist in active goal, got %+v", activeGoal.SelectedToolNames)
	}
}

func TestFreshTaskKeepsRouterInitialToolsWithoutRequiredEvidence(t *testing.T) {
	pinnedToolNames := pinnedToolNamesForResolvedRequest(
		[]string{"manual.tool"},
		[]string{"manual.tool", "previous.tool"},
		[]string{"file_read"},
		nil,
		true,
	)
	if !sameStringSet(pinnedToolNames, []string{"manual.tool", "file_read"}) {
		t.Fatalf("expected router fallback without required evidence, got %+v", pinnedToolNames)
	}
}

func TestRequiredNextToolsPreferPersistedThenArbitratedThenRouterOrder(t *testing.T) {
	testCases := []struct {
		name              string
		activeGoal        ActiveGoal
		arbitratedTools   []string
		routerTools       []string
		expectedToolNames []string
	}{
		{
			name:              "persisted continuation",
			activeGoal:        ActiveGoal{RequiredNextTools: []string{"task_update", "file_deliver"}},
			arbitratedTools:   []string{"calendar_update"},
			routerTools:       []string{"file_write"},
			expectedToolNames: []string{"task_update", "file_deliver"},
		},
		{
			name:              "arbitrated workflow",
			arbitratedTools:   []string{"file_write", toolcontract.TerminalRunToolName, toolcontract.FileDeliverToolName},
			routerTools:       []string{"file_write", toolcontract.FileDeliverToolName},
			expectedToolNames: []string{"file_write", toolcontract.TerminalRunToolName, toolcontract.FileDeliverToolName},
		},
		{
			name:              "router fallback",
			routerTools:       []string{"file_write", toolcontract.FileDeliverToolName},
			expectedToolNames: []string{"file_write", toolcontract.FileDeliverToolName},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			toolNames := requiredNextToolNamesForResolvedRequest(testCase.activeGoal, testCase.arbitratedTools, testCase.routerTools)
			if !slices.Equal(toolNames, testCase.expectedToolNames) {
				t.Fatalf("expected %v, got %v", testCase.expectedToolNames, toolNames)
			}
		})
	}
}

func TestContinuationKeepsPersistedToolsAuthoritative(t *testing.T) {
	pinnedToolNames := pinnedToolNamesForResolvedRequest(
		[]string{"manual.tool"},
		[]string{"manual.tool", "message_send"},
		[]string{"file_read"},
		[]string{"task_add"},
		false,
	)

	if !sameStringSet(pinnedToolNames, []string{"manual.tool", "message_send", "file_read"}) {
		t.Fatalf("expected persisted and router continuation tools without arbitration replacement, got %+v", pinnedToolNames)
	}
}

func TestExistingTaskRequestIsNotFresh(t *testing.T) {
	turnDecision := TurnDecision{Route: TurnRouteStartTask}
	for _, request := range []AgentRequest{
		{ExistingTaskRunID: "task-run-1"},
		{IsApprovalContinuation: true},
		{IsRuntimeRestartResume: true},
	} {
		if requestStartsFreshTask(turnDecision, request) {
			t.Fatalf("expected continuation request not to be fresh, got %+v", request)
		}
	}
	if !requestStartsFreshTask(turnDecision, AgentRequest{}) {
		t.Fatal("expected a start route without continuation state to be fresh")
	}
}

func TestAgentKernelConsumeRouteSuppressesReply(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	skillRetriever := &countingSkillRetriever{}
	agentKernel.UseSkillRetriever(skillRetriever)
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelXLow,
		EstimatedMinutes: 1,
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
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task run, got %q", result.TaskRun.Status)
	}
	if skillRetriever.searchCount != 0 {
		t.Fatalf("expected consume route to skip skill retrieval, got %d calls", skillRetriever.searchCount)
	}
}

func TestAgentKernelRejectsExecutableConsumeContradiction(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeResearchTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "사용자가 명시적으로 업무 등록을 요청함",
		InitialToolNames: []string{"task_add"},
	}})

	toolCallCount := 0
	toolSet := newTestCapabilityToolSet([]string{"task_add", "task_list", "task_update"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: "task_add"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"task-1","content":"메일 페이지 앱 비밀번호 개선"}`), nil
	})
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task_add","toolInput":{"prompt":"메일 페이지 앱 비밀번호 개선"}}`,
		finishMessageWithEvidence("업무를 등록했습니다.", "obs-001", "task_add", 0),
	}}
	agentKernel.UseLanguageModelProvider(languageModel)

	request := kernelTestRequest("업무 등록해줘.\n\n- 메일 페이지 앱 비밀번호 개선")
	request.ToolSet = toolSet
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected router failure result: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task for contradictory decision, got %q", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no tool call after contradictory decision, got %d", toolCallCount)
	}
}

func TestAgentKernelPausesNeedsConfirmationDisambiguation(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteClarify,
		Classification:        IntakeClassificationNeedsConfirmation,
		TaskShape:             TaskShapeApprovalGatedTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
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
	if result.TaskRun.Status != taskstate.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %q", result.TaskRun.Status)
	}
	if result.UserNotice != "어느 보고서를 말하는 건가요?" {
		t.Fatalf("expected clarification question, got %q", result.UserNotice)
	}
}

func TestAgentKernelBlocksUnsupportedIntake(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteGiveUp,
		Classification:   IntakeClassificationUnsupported,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "request is outside the available execution boundary",
		UserFacingReply:  "이 요청은 현재 권한 범위 밖이라 진행할 수 없어요.",
	}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("서버 루트 비밀번호 바꿔줘"))
	if errorValue != nil {
		t.Fatalf("expected unsupported intake to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %q", result.TaskRun.Status)
	}
	if result.UserNotice != "이 요청은 현재 권한 범위 밖이라 진행할 수 없어요." {
		t.Fatalf("expected router-provided reply, got %q", result.UserNotice)
	}
}

func TestAgentKernelPreservesActiveContractOnApprovalContinuation(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteContinueTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		InitialToolNames: []string{"site_unserve"},
		ResponseLanguage: "ko",
		Reason:           "approval reply classified with hallucinated evidence",
	}})

	toolCallCount := 0
	siteDeleteDefinition := testToolDescriptor("site_unserve")
	siteDeleteDefinition.InputSchema = json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`)
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{siteDeleteDefinition})
	registerTestTool(toolSet, siteDeleteDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"deleted":true}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site_unserve","toolInput":{"siteID":"site-1"}}`,
		finishMessageWithEvidence("웹사이트를 삭제했습니다.", "obs-001", "site_unserve", 0),
	}})

	request := kernelTestRequest("응 확인했어, 진행해줘")
	request.ToolSet = toolSet
	request.IsApprovalContinuation = true
	request.ActiveGoal = ActiveGoal{
		GoalID:              "goal-approval-continuation",
		TaskRunID:           "task-approval-continuation",
		OriginalInstruction: "테스트 웹사이트를 삭제해줘",
		CurrentObjective:    "site_unserve 승인 후 실행",
		Status:              ActiveGoalStatusActive,
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_unserve"},
		},
	}

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected approval continuation to run: %v", errorValue)
	}
	if result.TaskRun.Status == taskstate.TaskStatusBlocked {
		t.Fatal("expected approval continuation to survive invalid intake evidence, got blocked")
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the approved capability call to run once, got %d", toolCallCount)
	}
}

func TestExistingTaskRunIDDoesNotAuthorizeConfirmationBypass(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: destructiveSiteDeleteDecision()})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		destructiveSiteDeleteExecutionPlan(),
		`{"action":"continue","toolName":"site_unserve","toolInput":{}}`,
		`{"question":"site-1 웹사이트를 삭제할까요?"}`,
	}})
	siteDeleteDefinition := testToolDescriptor("site_unserve")
	siteDeleteDefinition.RequiresApproval = true
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{siteDeleteDefinition})
	toolCallCount := 0
	registerTestTool(toolSet, siteDeleteDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"deleted":true}`), nil
	})
	existingTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "site-1 웹사이트를 삭제해줘")
	request := kernelTestRequest("site-1 웹사이트를 삭제해줘")
	request.ToolSet = toolSet
	request.ExistingTaskRunID = existingTaskRun.TaskRunID

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected runtime approval gate: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusWaitingApproval {
		t.Fatalf("expected existing task identity not to authorize execution, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no side effect without explicit approval, got %d calls", toolCallCount)
	}
}

func TestSemanticRevisionStartsNewTaskRun(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteReviseTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		InitialToolNames: []string{"task_add"},
		ResponseLanguage: "ko",
	}})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task_add","toolInput":{"title":"새 업무"}}`,
		finishMessageWithEvidence("새 업무를 추가했습니다.", "obs-001", "task_add", 0),
	}})
	taskAddDefinition := testToolDescriptor("task_add")
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{taskAddDefinition})
	registerTestTool(toolSet, taskAddDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"taskID":"new-task"}`), nil
	})
	existingTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "기존 업무")
	request := kernelTestRequest("기존 요청 대신 새 업무를 추가해줘")
	request.ToolSet = toolSet
	request.ExistingTaskRunID = existingTaskRun.TaskRunID
	request.ActiveGoal = ActiveGoal{
		TaskRunID: existingTaskRun.TaskRunID,
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task_add"},
		},
	}

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected semantic revision to run: %v", errorValue)
	}
	if result.TaskRun.TaskRunID == existingTaskRun.TaskRunID {
		t.Fatal("expected semantic revision to start a fresh task run")
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected revised task to complete, got %s", result.TaskRun.Status)
	}
}

func TestInvalidPersistedActiveGoalBlocksBeforeToolHandler(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		recoveryDecisionDocument("report the failure", "explain that the task could not safely resume"),
	}})
	toolCallCount := 0
	toolSet := newTestCapabilityToolSet([]string{"task_add"})
	registerTestTool(toolSet, testToolDescriptor("task_add"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"unexpected"}`), nil
	})
	request := kernelTestRequest("계속해")
	request.ToolSet = toolSet
	request.ActiveGoal = ActiveGoal{RestoreError: "latest active goal event is invalid"}

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected fail-closed result: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no handler call, got %d", toolCallCount)
	}
}

func destructiveSiteDeleteDecision() TurnDecision {
	return TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeApprovalGatedTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		InitialToolNames: []string{"site_unserve"},
		ResponseLanguage: "ko",
	}
}

func destructiveSiteDeleteExecutionPlan() string {
	return `{"originalInstruction":"site-1 웹사이트를 삭제해줘","summary":"site-1 삭제","targets":["site-1"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"승인 후 삭제"}`
}

func TestAgentKernelSideEffectTaskProceedsWithoutRouterPredictedEvidence(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		InitialToolNames: []string{toolcontract.TerminalRunToolName},
		ResponseLanguage: "ko",
		Reason:           "side effect tool planned without a predicted evidence name",
	}})
	toolCallCount := 0
	toolSet := newTestToolSet([]string{toolcontract.TerminalRunToolName, "task_update"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolcontract.TerminalRunToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal_run","toolInput":{"command":"do the side effect"}}`,
		finishMessageWithEvidence("완료했습니다.", "obs-001", toolcontract.TerminalRunToolName, 0),
	}})
	request := kernelTestRequest("서버에 배포 스크립트 실행해줘")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected side-effect task without predicted evidence to proceed: %v", errorValue)
	}
	if result.TaskRun.Status == taskstate.TaskStatusBlocked {
		t.Fatalf("expected the intake to never block for missing evidence, got %q", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the planned side effect to run once, got %d", toolCallCount)
	}
}

type routerLedgerLanguageModel struct {
	decision   TurnDecision
	response   model.StructuredResponse
	errorValue error
}

func (languageModel *routerLedgerLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("router model only serves structured routing")
}

func (languageModel *routerLedgerLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != turnRouterSchemaName {
		return model.StructuredResponse{Content: "{}"}, nil
	}
	if languageModel.errorValue != nil {
		return languageModel.response, languageModel.errorValue
	}
	document, errorValue := json.Marshal(languageModel.decision)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	response := languageModel.response
	response.Content = string(document)
	return response, nil
}

func persistedTurnRouterCallRecords(taskEvents []taskstate.TaskEvent) []llmCallRecord {
	records := []llmCallRecord{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "llm.call" {
			continue
		}
		var record llmCallRecord
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue == nil && record.SchemaName == turnRouterSchemaName {
			records = append(records, record)
		}
	}
	return records
}

func TestAgentKernelGeneratesIntakeNoticeWhenRouterReplyMissing(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteGiveUp,
		Classification:   IntakeClassificationUnsupported,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "request is outside the available execution boundary",
	}})
	agentKernel.UseLanguageModelProvider(&recoveryChatNoticeProvider{chatReply: "지금 실행 범위에서는 안전하게 처리할 수 없어요. 요청을 좁혀주시면 도와드릴게요."})

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
		Route:            TurnRouteGiveUp,
		Classification:   IntakeClassificationUnsupported,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
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
		TaskLevel:        TaskLevelXLow,
		EstimatedMinutes: 1,
		ResponseLanguage: "ko",
		Reason:           "direct answer",
	}})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{finishMessageDocument("오늘은 수요일이에요.")}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("오늘 무슨 요일이야?"))
	if errorValue != nil {
		t.Fatalf("expected bounded run to complete: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected completed task run, got %q", result.TaskRun.Status)
	}
	if !strings.Contains(result.TaskRun.Result, "수요일") {
		t.Fatalf("expected finish message in result, got %q", result.TaskRun.Result)
	}
}

func TestAgentKernelPersistsTurnRouterLLMCall(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(&routerLedgerLanguageModel{
		decision: TurnDecision{
			Route:            TurnRouteStartTask,
			Classification:   IntakeClassificationQuickReply,
			TaskShape:        TaskShapeImmediateReply,
			TaskLevel:        TaskLevelXLow,
			EstimatedMinutes: 1,
			ResponseLanguage: "ko",
			Reason:           "direct answer",
		},
		response: model.StructuredResponse{
			ProviderName: "llmd",
			ModelName:    "router-model",
			ModelTier:    "xlow",
			Usage: model.Usage{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		},
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{finishMessageDocument("완료했습니다.")}})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("오늘 무슨 요일이야?"))
	if errorValue != nil {
		t.Fatalf("expected bounded run to complete: %v", errorValue)
	}
	records := persistedTurnRouterCallRecords(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID))
	if len(records) != 1 {
		t.Fatalf("expected one persisted router call, got %+v", records)
	}
	if records[0].Provider != "llmd" || records[0].Model != "router-model" || records[0].ModelTier != "xlow" || records[0].UsedFallback {
		t.Fatalf("expected LLMD router metadata without fallback, got %+v", records[0])
	}
	if records[0].PromptTokens != 11 || records[0].CompletionTokens != 7 || records[0].TotalTokens != 18 {
		t.Fatalf("expected router token metadata, got %+v", records[0])
	}
}

func TestAgentKernelPersistsTurnRouterFailureWithoutFallbackRoute(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(&routerLedgerLanguageModel{
		response: model.StructuredResponse{
			ProviderName: "llmd",
			ModelName:    "router-model",
		},
		errorValue: errors.New("router unavailable"),
	})
	agentKernel.UseLanguageModelProvider(&recoveryChatNoticeProvider{chatReply: "요청을 분류하지 못해 이번 작업을 시작하지 못했습니다. 다시 요청해 주세요."})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("오늘 무슨 요일이야?"))
	if errorValue != nil {
		t.Fatalf("expected persisted router failure result: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusFailed || result.FailureNotice.Source != "generated" {
		t.Fatalf("expected LLM-authored failed task, got %+v", result)
	}
	if !strings.Contains(result.FailureNotice.SendableMessage(), "분류하지 못해") {
		t.Fatalf("expected authored failure notice, got %+v", result.FailureNotice)
	}
	records := persistedTurnRouterCallRecords(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID))
	if len(records) != 1 || !records[0].IsError || !strings.Contains(records[0].Error, "router unavailable") {
		t.Fatalf("expected persisted router error call, got %+v", records)
	}
	if taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "") {
		t.Fatal("router failure must not invent an intake decision")
	}
}

func TestSitePrototypeIntakePromotesToXHighLimits(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	siteToolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            "site_serve",
		Namespace:       "site",
		SideEffectClass: toolcontract.ToolSideEffectExternalPublish,
	}})
	request := AgentRequest{
		ToolSet: siteToolSet,
		ActiveGoal: ActiveGoal{
			OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"site_serve"}},
		},
	}
	intakeDecision := promoteArtifactTaskLevel(request, IntakeDecision{
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
	})

	turnOptions := agentKernel.turnOptionsForIntakeDecision(intakeDecision)
	xHighProfile := TaskLevelProfileForLevel(TaskLevelXHigh)

	if taskLevelRank(turnOptions.TaskLevel) < taskLevelRank(TaskLevelXHigh) {
		t.Fatalf("expected at least xhigh task level, got %q", turnOptions.TaskLevel)
	}
	if turnOptions.MaxIterationCount < xHighProfile.MaxIterationCount {
		t.Fatalf("expected xhigh iteration limit, got %d", turnOptions.MaxIterationCount)
	}
	if turnOptions.MaxToolCallCount < xHighProfile.MaxToolCallCount {
		t.Fatalf("expected xhigh tool call limit, got %d", turnOptions.MaxToolCallCount)
	}
	if turnOptions.MaxElapsedSecond != int(xHighProfile.Duration.Seconds()) {
		t.Fatalf("expected xhigh work duration, got %d seconds", turnOptions.MaxElapsedSecond)
	}
}

func TestHumanEstimateDoesNotShrinkTaskWorkDuration(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	shortEstimate := agentKernel.turnOptionsForIntakeDecision(IntakeDecision{TaskLevel: TaskLevelLow, EstimatedMinutes: 1})
	longEstimate := agentKernel.turnOptionsForIntakeDecision(IntakeDecision{TaskLevel: TaskLevelLow, EstimatedMinutes: 30})
	expectedSeconds := int(TaskLevelProfileForLevel(TaskLevelLow).Duration.Seconds())

	if shortEstimate.MaxElapsedSecond != expectedSeconds || longEstimate.MaxElapsedSecond != expectedSeconds {
		t.Fatalf("expected low work duration %d regardless of human estimate, got short=%d long=%d", expectedSeconds, shortEstimate.MaxElapsedSecond, longEstimate.MaxElapsedSecond)
	}
}

func TestAgentKernelRouterDeadlinePersistsOneBlockedTask(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseTurnOptions(TurnOptions{MaxElapsedSecond: 1})
	agentKernel.UseIntakeLanguageModelProvider(deadlineBlockingRouterLanguageModel{})
	request := kernelTestRequest("고객지원 업무를 정리해줘")
	request.TurnStartedAt = time.Now().Add(-2 * time.Second)

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected persisted max elapsed result: %v", errorValue)
	}
	taskRuns := taskRunService.ListTaskRunByPersonID(request.RequesterPersonID)
	if len(taskRuns) != 1 || result.TaskRun.TaskRunID != taskRuns[0].TaskRunID {
		t.Fatalf("expected exactly one persisted task, got %+v", taskRuns)
	}
	if result.TaskRun.Status != taskstate.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected blocked max elapsed task, got %+v", result.TaskRun)
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.limit_stop", `"phase":"intake"`) {
		t.Fatal("expected observable intake max elapsed event")
	}
	if taskEventNameCount(taskEvents, "agent.limit_stop") != 1 || taskEventNameCount(taskEvents, "agent.goal.blocked") != 1 {
		t.Fatalf("expected one limit and goal event, got %+v", taskEvents)
	}
	if taskEventsContain(taskEvents, "agent.intake", "") {
		t.Fatal("router deadline must not invent an intake decision")
	}
}

func TestAgentKernelSkillDeadlinePersistsOneBlockedTask(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	languageModel := &routerThenBlockingLanguageModel{decision: TurnDecision{
		Route:              TurnRouteStartTask,
		Classification:     IntakeClassificationBoundedTask,
		TaskShape:          TaskShapeMaintenanceTask,
		TaskLevel:          TaskLevelXLow,
		EstimatedMinutes:   1,
		PriorTaskReference: PriorTaskReferenceNone,
		Reason:             "업무 정리",
	}}
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseLanguageModelProvider(languageModel)
	request := kernelTestRequest("고객지원 업무를 정리해줘")
	workDuration := workDurationWithinTotal(TaskLevelProfileForLevel(TaskLevelXLow).Duration)
	request.TurnStartedAt = time.Now().Add(-workDuration + 100*time.Millisecond)

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected persisted max elapsed result: %v", errorValue)
	}
	taskRuns := taskRunService.ListTaskRunByPersonID(request.RequesterPersonID)
	if len(taskRuns) != 1 || result.TaskRun.TaskRunID != taskRuns[0].TaskRunID {
		t.Fatalf("expected exactly one persisted task, got %+v", taskRuns)
	}
	if result.TaskRun.Status != taskstate.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected blocked max elapsed task, got %+v", result.TaskRun)
	}
	if languageModel.postRouterCallCount == 0 {
		t.Fatal("expected post-router skill selection to receive the task budget")
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventNameCount(taskEvents, "agent.intake") != 1 || taskEventNameCount(taskEvents, "agent.limit_stop") != 1 || taskEventNameCount(taskEvents, "agent.goal.blocked") != 1 {
		t.Fatalf("expected one intake, limit, and goal event, got %+v", taskEvents)
	}
}

func TestAgentKernelCallerCancellationIsNotMaxElapsed(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(deadlineBlockingRouterLanguageModel{})
	responseContext, cancel := context.WithCancel(context.Background())
	cancel()

	result, errorValue := agentKernel.RunAgentRequest(responseContext, kernelTestRequest("고객지원 업무를 정리해줘"))

	if errorValue != nil {
		t.Fatalf("expected persisted cancellation result: %v", errorValue)
	}
	if result.TaskRun.FailureReason == "max_elapsed" {
		t.Fatalf("expected caller cancellation to remain distinct, got %+v", result.TaskRun)
	}
	if taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
		t.Fatal("caller cancellation must not emit a max elapsed event")
	}
}

func TestTurnBudgetCallerContextExcludesInternalTotalDeadline(t *testing.T) {
	turnBudget := newTurnBudgetContext(context.Background(), time.Now().Add(-2*time.Second), false, time.Now(), TurnOptions{MaxElapsedSecond: 1})
	defer turnBudget.cancel()

	if !errors.Is(turnBudget.totalContext.Err(), context.DeadlineExceeded) {
		t.Fatal("expected the internal total deadline to expire")
	}
	if turnBudget.callerContext().Err() != nil {
		t.Fatalf("expected the caller context to remain active, got %v", turnBudget.callerContext().Err())
	}
}

func TestTurnBudgetCallerContextPreservesCallerCancellationAndDeadline(t *testing.T) {
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledBudget := newTurnBudgetContext(cancelledContext, time.Now(), false, time.Now(), TurnOptions{MaxElapsedSecond: 30})
	defer cancelledBudget.cancel()

	if !errors.Is(cancelledBudget.callerContext().Err(), context.Canceled) {
		t.Fatalf("expected caller cancellation, got %v", cancelledBudget.callerContext().Err())
	}

	deadlineContext, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	deadlineBudget := newTurnBudgetContext(deadlineContext, time.Now(), false, time.Now(), TurnOptions{MaxElapsedSecond: 30})
	defer deadlineBudget.cancel()

	if !errors.Is(deadlineBudget.callerContext().Err(), context.DeadlineExceeded) {
		t.Fatalf("expected caller deadline, got %v", deadlineBudget.callerContext().Err())
	}
}

func TestClampedTurnStartedAtReanchorsStaleNonResumeAnchor(t *testing.T) {
	referenceNow := time.Now()
	staleAnchor := referenceNow.Add(-nonResumeAnchorStaleAllowance - time.Second)

	resolvedTurnStartedAt, didClampAnchor, originalTurnStartedAt := clampedTurnStartedAt(staleAnchor, false, referenceNow)

	if !didClampAnchor {
		t.Fatal("expected a stale non-resume anchor to be clamped")
	}
	if !resolvedTurnStartedAt.Equal(referenceNow) {
		t.Fatalf("expected the clamped anchor to equal the reference now, got %v", resolvedTurnStartedAt)
	}
	if !originalTurnStartedAt.Equal(staleAnchor) {
		t.Fatalf("expected the original anchor to be preserved for diagnostics, got %v", originalTurnStartedAt)
	}
}

func TestClampedTurnStartedAtPreservesResumeAnchorRegardlessOfAge(t *testing.T) {
	referenceNow := time.Now()
	staleAnchor := referenceNow.Add(-nonResumeAnchorStaleAllowance - time.Hour)

	resolvedTurnStartedAt, didClampAnchor, originalTurnStartedAt := clampedTurnStartedAt(staleAnchor, true, referenceNow)

	if didClampAnchor {
		t.Fatal("expected a runtime-restart resume to keep its restored anchor untouched")
	}
	if !resolvedTurnStartedAt.Equal(staleAnchor) {
		t.Fatalf("expected the resume anchor to remain the restored value, got %v", resolvedTurnStartedAt)
	}
	if !originalTurnStartedAt.Equal(staleAnchor) {
		t.Fatalf("expected the original anchor to match the resume anchor, got %v", originalTurnStartedAt)
	}
}

func TestClampedTurnStartedAtLeavesFreshAnchorUntouched(t *testing.T) {
	referenceNow := time.Now()
	freshAnchor := referenceNow.Add(-time.Second)

	resolvedTurnStartedAt, didClampAnchor, originalTurnStartedAt := clampedTurnStartedAt(freshAnchor, false, referenceNow)

	if didClampAnchor {
		t.Fatal("expected a fresh non-resume anchor to be left untouched")
	}
	if !resolvedTurnStartedAt.Equal(freshAnchor) {
		t.Fatalf("expected the anchor to remain fresh, got %v", resolvedTurnStartedAt)
	}
	if !originalTurnStartedAt.Equal(freshAnchor) {
		t.Fatalf("expected the original anchor to match the fresh anchor, got %v", originalTurnStartedAt)
	}
}

func TestAgentKernelXHighTaskKeepsHourBudgetWithLowExecutionModel(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	languageModel := &deadlineCapturingFinishLanguageModel{}
	agentKernel.UseLanguageModelProvider(languageModel)
	precomputedDecision := TurnDecision{
		Route:          TurnRouteStartTask,
		Classification: IntakeClassificationQuickReply,
		TaskShape:      TaskShapeImmediateReply,
		TaskLevel:      TaskLevelXHigh,
	}
	request := kernelTestRequest("웹사이트를 만들어줘")
	request.PrecomputedTurnDecision = &precomputedDecision
	request.IsPrecomputedDecisionExact = true
	request.SkipSkillSelection = true
	request.ToolSet = newTestToolSet(nil)
	request.TurnStartedAt = time.Now().Add(-15 * time.Minute)

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil || result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected xhigh task to complete with low execution model: result=%+v error=%v", result, errorValue)
	}
	if languageModel.deadline.IsZero() {
		t.Fatal("expected execution deadline")
	}
	remainingDuration := time.Until(languageModel.deadline)
	if remainingDuration < 43*time.Minute {
		t.Fatalf("expected xhigh hour budget independent of execution model, got %s", remainingDuration)
	}
}

func TestAgentKernelClampsStaleNonResumeAnchorInsteadOfInstantElapsing(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	languageModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("diagnostic done")}}
	agentKernel.UseLanguageModelProvider(languageModel)
	precomputedDecision := TurnDecision{
		Route:              TurnRouteStartTask,
		Classification:     IntakeClassificationQuickReply,
		TaskShape:          TaskShapeImmediateReply,
		TaskLevel:          TaskLevelLow,
		PriorTaskReference: PriorTaskReferenceNone,
	}
	request := kernelTestRequest("진단해줘")
	request.PrecomputedTurnDecision = &precomputedDecision
	request.IsPrecomputedDecisionExact = true
	request.SkipSkillSelection = true
	request.ToolSet = newTestToolSet(nil)
	request.TurnStartedAt = time.Now().Add(-TaskLevelProfileForLevel(TaskLevelLow).Duration - time.Second)

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected a completed task result: %v", errorValue)
	}
	if result.TaskRun.Status != taskstate.TaskStatusCompleted {
		t.Fatalf("expected the clamped anchor to let the task proceed to completion, got %+v", result.TaskRun)
	}
	if len(languageModel.requests) == 0 {
		t.Fatal("expected the task action model to run once the anchor was clamped")
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, "agent.limit_stop", "max_elapsed") {
		t.Fatal("a clamped anchor must not still report an instant max_elapsed")
	}
	if !taskEventsContain(taskEvents, "agent.turn_anchor_clamped", "originalTurnStartedAtUnixMs") {
		t.Fatal("expected a turn_anchor_clamped diagnostic event naming the stale original anchor")
	}
}

func TestExactPrecomputedDecisionSkipsArtifactTaskLevelPromotion(t *testing.T) {
	intakeDecision := promoteArtifactTaskLevelForRequest(AgentRequest{
		Prompt:                     "Create and publish a PDF website",
		IsPrecomputedDecisionExact: true,
	}, IntakeDecision{TaskLevel: TaskLevelLow})

	if intakeDecision.TaskLevel != TaskLevelLow {
		t.Fatalf("expected exact precomputed task level, got %q", intakeDecision.TaskLevel)
	}
}

func TestAgentKernelPreservesExactPrecomputedTaskLevel(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{finishMessageDocument("diagnostic done")}})
	agentKernel.UseTaskTierLanguageModels(nil, failingLanguageModel{}, nil, nil, nil, nil)
	precomputedDecision := TurnDecision{
		Route:              TurnRouteStartTask,
		Classification:     IntakeClassificationQuickReply,
		TaskShape:          TaskShapeImmediateReply,
		TaskLevel:          TaskLevelLow,
		EstimatedMinutes:   1,
		PriorTaskReference: PriorTaskReferenceNone,
		Reason:             "LLMD topology diagnostic",
	}
	request := kernelTestRequest("Create and publish a PDF website")
	request.PrecomputedTurnDecision = &precomputedDecision
	request.IsPrecomputedDecisionExact = true
	request.SkipSkillSelection = true
	request.ToolSet = newTestToolSet(nil)

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected exact low-tier diagnostic run: %v", errorValue)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", `"level":"low"`) {
		t.Fatal("expected persisted exact low task level")
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

	if result.TaskRun.Status != taskstate.TaskStatusFailed {
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

type deadlineBlockingRouterLanguageModel struct{}

func (deadlineBlockingRouterLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("router model only serves structured routing")
}

func (deadlineBlockingRouterLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	<-responseContext.Done()
	return model.StructuredResponse{}, responseContext.Err()
}

type routerThenBlockingLanguageModel struct {
	decision            TurnDecision
	postRouterCallCount int
}

func (languageModel *routerThenBlockingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("language model only serves structured generation")
}

func (languageModel *routerThenBlockingLanguageModel) GenerateStructuredResponse(responseContext context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == turnRouterSchemaName {
		document, errorValue := json.Marshal(languageModel.decision)
		return model.StructuredResponse{Content: string(document)}, errorValue
	}
	languageModel.postRouterCallCount++
	<-responseContext.Done()
	return model.StructuredResponse{}, responseContext.Err()
}

type deadlineCapturingFinishLanguageModel struct {
	deadline time.Time
}

func (languageModel *deadlineCapturingFinishLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("language model only serves structured generation")
}

func (languageModel *deadlineCapturingFinishLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.deadline, _ = responseContext.Deadline()
	return model.StructuredResponse{Content: finishMessageDocument("완료했습니다.")}, nil
}

func taskEventNameCount(taskEvents []taskstate.TaskEvent, eventName string) int {
	count := 0
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == eventName {
			count++
		}
	}
	return count
}
