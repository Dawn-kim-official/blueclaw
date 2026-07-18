package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

type intakeDecisionLanguageModel struct {
	decision TurnDecision
}

type rejectingOperationContractLanguageModel struct {
	decision TurnDecision
}

func (model intakeDecisionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("intake model only serves structured routing")
}

func (model rejectingOperationContractLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "요청한 값을 안전하게 확정하지 못했습니다.", nil
}

func (model rejectingOperationContractLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == operationContractSchemaName {
		return llm.StructuredResponse{Content: `{"operations":[{"toolName":"task.add","requiredValuesJSON":"{\"query\":\"분기 결산\"}"}]}`}, nil
	}
	document, errorValue := json.Marshal(model.decision)
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	return llm.StructuredResponse{Content: string(document)}, nil
}

func (model intakeDecisionLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == operationContractSchemaName {
		document := operationContractTestDocument(request.StructuredOutputSchema.Document)
		return llm.StructuredResponse{Content: document}, nil
	}
	if request.StructuredOutputSchema.Name == operationContractReviewSchemaName {
		return llm.StructuredResponse{Content: `{"isComplete":true,"reason":""}`}, nil
	}
	document, errorValue := json.Marshal(model.decision)
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	return llm.StructuredResponse{Content: string(document)}, nil
}

func operationContractTestDocument(schemaDocument string) string {
	var schema map[string]any
	_ = json.Unmarshal([]byte(schemaDocument), &schema)
	properties, _ := schema["properties"].(map[string]any)
	operations, _ := properties["operations"].(map[string]any)
	items, _ := operations["items"].(map[string]any)
	itemProperties, _ := items["properties"].(map[string]any)
	toolName, _ := itemProperties["toolName"].(map[string]any)
	toolNames, _ := toolName["enum"].([]any)
	operationDocuments := []map[string]string{}
	for _, value := range toolNames {
		name, _ := value.(string)
		operationDocuments = append(operationDocuments, map[string]string{"toolName": name, "requiredValuesJSON": "{}"})
	}
	document, _ := json.Marshal(map[string]any{"operations": operationDocuments})
	return string(document)
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

func TestApprovalContinuationRestoresSelectedToolDecision(t *testing.T) {
	request := AgentRequest{
		PinnedToolNames:  []string{"file.read"},
		PinnedSkillNames: []string{"calendar"},
		ActiveGoal: ActiveGoal{
			SelectedToolNames:  []string{"message.send"},
			SelectedSkillNames: []string{"direct-message"},
		},
	}

	restoredRequest := restorePersistedToolSelection(request)

	if !sameStringSet(restoredRequest.PinnedToolNames, []string{"file.read", "message.send"}) {
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
		[]string{"file.read"},
		[]string{"task.add"},
		true,
	)

	if !sameStringSet(pinnedToolNames, []string{"manual.tool", "file.read", "task.add"}) {
		t.Fatalf("expected manual, router, and required evidence tools, got %+v", pinnedToolNames)
	}
	activeGoal := activeGoalForTurn(AgentRequest{PinnedToolNames: pinnedToolNames}, OutcomeContract{}, ExecutionPlan{}, false)
	if !sameStringSet(activeGoal.SelectedToolNames, []string{"manual.tool", "file.read", "task.add"}) {
		t.Fatalf("expected the typed working set to persist in active goal, got %+v", activeGoal.SelectedToolNames)
	}
}

func TestFreshTaskKeepsRouterInitialToolsWithoutRequiredEvidence(t *testing.T) {
	pinnedToolNames := pinnedToolNamesForResolvedRequest(
		[]string{"manual.tool"},
		[]string{"manual.tool", "previous.tool"},
		[]string{"file.read"},
		nil,
		true,
	)
	if !sameStringSet(pinnedToolNames, []string{"manual.tool", "file.read"}) {
		t.Fatalf("expected router fallback without required evidence, got %+v", pinnedToolNames)
	}
}

func TestContinuationKeepsPersistedToolsAuthoritative(t *testing.T) {
	pinnedToolNames := pinnedToolNamesForResolvedRequest(
		[]string{"manual.tool"},
		[]string{"manual.tool", "message.send"},
		[]string{"file.read"},
		[]string{"task.add"},
		false,
	)

	if !sameStringSet(pinnedToolNames, []string{"manual.tool", "message.send", "file.read"}) {
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
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task run, got %q", result.TaskRun.Status)
	}
	if skillRetriever.searchCount != 0 {
		t.Fatalf("expected consume route to skip skill retrieval, got %d calls", skillRetriever.searchCount)
	}
}

func TestAgentKernelRejectsExecutableConsumeContradiction(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteConsume,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeResearchTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		ResponseLanguage:      "ko",
		Reason:                "사용자가 명시적으로 업무 등록을 요청함",
		RequiredEvidenceTools: []string{"task.add"},
		InitialToolNames:      []string{"task.add"},
	}})

	toolCallCount := 0
	toolSet := newTestCapabilityToolSet([]string{"task.add", "task.list", "task.update"})
	registerTestTool(toolSet, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"task-1","content":"메일 페이지 앱 비밀번호 개선"}`), nil
	})
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.add","toolInput":{"prompt":"메일 페이지 앱 비밀번호 개선"}}`,
		finishMessageWithEvidence("업무를 등록했습니다.", "obs-001", "task.add", 0),
	}}
	agentKernel.UseLanguageModelProvider(languageModel)

	request := kernelTestRequest("업무 등록해줘.\n\n- 메일 페이지 앱 비밀번호 개선")
	request.ToolSet = toolSet
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected router failure result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task for contradictory decision, got %q", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no tool call after contradictory decision, got %d", toolCallCount)
	}
}

func TestAgentKernelDoesNotInvokeToolWhenOperationContractCompilationFails(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	decision := TurnDecision{
		Route:                 TurnRouteStartTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		ResponseLanguage:      "ko",
		RequiredEvidenceTools: []string{"task.add"},
		InitialToolNames:      []string{"task.add"},
	}
	languageModel := rejectingOperationContractLanguageModel{decision: decision}
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseLanguageModelProvider(languageModel)
	toolCallCount := 0
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		ID:              "capabilityd:task.add",
		Name:            "task.add",
		InputSchema:     operationContractTaskInputSchema(),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass: ToolSideEffectStateChange,
	}})
	registerTestTool(toolSet, ToolDefinition{
		ID:              "capabilityd:task.add",
		Name:            "task.add",
		InputSchema:     operationContractTaskInputSchema(),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass: ToolSideEffectStateChange,
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess("ok"), nil
	})
	request := kernelTestRequest("분기 결산 누락 확인 업무를 추가해줘")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected fail-closed task result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if strings.TrimSpace(result.UserNotice) == "" {
		t.Fatal("expected compiler failure to produce a user notice")
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no handler call, got %d", toolCallCount)
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
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %q", result.TaskRun.Status)
	}
	if result.UserNotice != "이 요청은 현재 권한 범위 밖이라 진행할 수 없어요." {
		t.Fatalf("expected router-provided reply, got %q", result.UserNotice)
	}
}

// Invalid required evidence must never hard-block the task at intake. The
// invalid names are pruned and the task keeps executing; real permission is
// enforced at execution.
func TestAgentKernelPrunesInvalidRequiredEvidenceAndProceeds(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteStartTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		RequiredEvidenceTools: []string{"calendar.create"},
		InitialToolNames:      []string{"calendar.add"},
		ResponseLanguage:      "ko",
		Reason:                "calendar event creation",
	}})
	toolSet := newTestToolSet([]string{"calendar.add"})
	registerTestTool(toolSet, ToolDefinition{Name: "calendar.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"created":true}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calendar.add","toolInput":{}}`,
		finishMessageWithEvidence("일정을 추가했습니다.", "obs-001", "calendar.add", 0),
	}})
	request := kernelTestRequest("7월 6일 오후 1시 스타트업월드컵 일정 추가")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected invalid evidence to prune and proceed: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusBlocked {
		t.Fatalf("invalid required evidence must not hard-block; task should proceed, got %q", result.TaskRun.Status)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), requiredEvidenceInvalidEventName, "pruned") {
		t.Fatal("expected the invalid evidence to be recorded as pruned, not blocking")
	}
}

func TestAgentKernelPreservesActiveContractOnApprovalContinuation(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteContinueTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		RequiredEvidenceTools: []string{"delete_website_artifact"},
		InitialToolNames:      []string{"site.delete"},
		ResponseLanguage:      "ko",
		Reason:                "approval reply classified with hallucinated evidence",
	}})

	toolCallCount := 0
	siteDeleteDefinition := testToolDescriptor("site.delete")
	siteDeleteDefinition.InputSchema = json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`)
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{siteDeleteDefinition})
	registerTestTool(toolSet, siteDeleteDefinition, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"deleted":true}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.delete","toolInput":{"siteID":"site-1"}}`,
		finishMessageWithEvidence("웹사이트를 삭제했습니다.", "obs-001", "site.delete", 0),
	}})

	request := kernelTestRequest("응 확인했어, 진행해줘")
	request.ToolSet = toolSet
	request.IsApprovalContinuation = true
	siteDeleteDescriptor, _ := toolSet.ToolDefinition("site.delete")
	request.ActiveGoal = ActiveGoal{
		GoalID:              "goal-approval-continuation",
		TaskRunID:           "task-approval-continuation",
		OriginalInstruction: "테스트 웹사이트를 삭제해줘",
		CurrentObjective:    "site.delete 승인 후 실행",
		Status:              ActiveGoalStatusActive,
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.delete"},
			OperationContract: &OperationContract{
				Version: operationContractVersion,
				Requirements: []OperationRequirement{{
					RequirementID: "operation-1",
					ToolID:        siteDeleteDescriptor.ID,
					ToolName:      "site.delete",
					InputMode:     OperationInputContainsExplicit,
					RequiredInput: json.RawMessage(`{"siteID":"site-1"}`),
				}},
			},
		},
	}

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected approval continuation to run: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusBlocked {
		t.Fatal("expected approval continuation to survive invalid intake evidence, got blocked")
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the approved capability call to run once, got %d", toolCallCount)
	}
}

func TestAgentKernelCompilesOperationContractBeforeApprovalPause(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: destructiveSiteDeleteDecision()})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		destructiveSiteDeleteExecutionPlan(),
		`{"reply":"site-1 웹사이트를 삭제할까요?"}`,
	}})
	siteDeleteDefinition := testToolDescriptor("site.delete")
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{siteDeleteDefinition})
	toolCallCount := 0
	registerTestTool(toolSet, siteDeleteDefinition, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"deleted":true}`), nil
	})
	request := kernelTestRequest("site-1 웹사이트를 삭제해줘")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected approval pause: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no side effect before approval, got %d calls", toolCallCount)
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.goal.waiting_approval", `"operationContract":{"version":1`) {
		t.Fatal("expected reviewed operation contract persisted before approval")
	}
}

func TestExistingTaskRunIDDoesNotAuthorizeConfirmationBypass(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: destructiveSiteDeleteDecision()})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		destructiveSiteDeleteExecutionPlan(),
		`{"reply":"site-1 웹사이트를 삭제할까요?"}`,
	}})
	siteDeleteDefinition := testToolDescriptor("site.delete")
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{siteDeleteDefinition})
	toolCallCount := 0
	registerTestTool(toolSet, siteDeleteDefinition, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"deleted":true}`), nil
	})
	existingTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "site-1 웹사이트를 삭제해줘")
	request := kernelTestRequest("site-1 웹사이트를 삭제해줘")
	request.ToolSet = toolSet
	request.ExistingTaskRunID = existingTaskRun.TaskRunID

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected confirmation gate: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected existing task identity not to authorize execution, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no side effect without explicit approval, got %d calls", toolCallCount)
	}
}

func TestSemanticRevisionStartsNewTaskRunAndOperationContract(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteReviseTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		RequiredEvidenceTools: []string{"task.add"},
		InitialToolNames:      []string{"task.add"},
		ResponseLanguage:      "ko",
	}})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.add","toolInput":{"title":"새 업무"}}`,
		finishMessageWithEvidence("새 업무를 추가했습니다.", "obs-001", "task.add", 0),
	}})
	taskAddDefinition := testToolDescriptor("task.add")
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{taskAddDefinition})
	registerTestTool(toolSet, taskAddDefinition, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"taskID":"new-task"}`), nil
	})
	existingTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "기존 업무")
	request := kernelTestRequest("기존 요청 대신 새 업무를 추가해줘")
	request.ToolSet = toolSet
	request.ExistingTaskRunID = existingTaskRun.TaskRunID
	request.ActiveGoal = ActiveGoal{
		TaskRunID: existingTaskRun.TaskRunID,
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task.add"},
			OperationContract: &OperationContract{
				Version: operationContractVersion,
				Requirements: []OperationRequirement{{
					RequirementID: "operation-1",
					ToolID:        taskAddDefinition.ID,
					ToolName:      "task.add",
					InputMode:     OperationInputContainsExplicit,
					RequiredInput: json.RawMessage(`{"title":"기존 업무"}`),
				}},
			},
		},
	}

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected semantic revision to run: %v", errorValue)
	}
	if result.TaskRun.TaskRunID == existingTaskRun.TaskRunID {
		t.Fatal("expected semantic revision to start a fresh task run")
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected revised task to complete, got %s", result.TaskRun.Status)
	}
}

func TestInvalidPersistedActiveGoalBlocksBeforeToolHandler(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		recoveryDecisionDocument("active goal restore failed", "persisted state is invalid", "report the failure", "explain that the task could not safely resume"),
	}})
	toolCallCount := 0
	toolSet := newTestCapabilityToolSet([]string{"task.add"})
	registerTestTool(toolSet, testToolDescriptor("task.add"), func(context.Context, ToolInvocation) (ToolResult, error) {
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
	if result.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected no handler call, got %d", toolCallCount)
	}
}

func destructiveSiteDeleteDecision() TurnDecision {
	return TurnDecision{
		Route:                 TurnRouteStartTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeApprovalGatedTask,
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		RequiredEvidenceTools: []string{"site.delete"},
		InitialToolNames:      []string{"site.delete"},
		ResponseLanguage:      "ko",
	}
}

func destructiveSiteDeleteExecutionPlan() string {
	return `{"originalInstruction":"site-1 웹사이트를 삭제해줘","summary":"site-1 삭제","targets":["site-1"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"승인 후 삭제"}`
}

// A side-effect task that arrives without required evidence must not hard-block;
// it proceeds and the tool actually runs.
func TestAgentKernelSideEffectWithoutRequiredEvidenceProceeds(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
		InitialToolNames: []string{TerminalRunToolName},
		ResponseLanguage: "ko",
		Reason:           "side effect tool planned without evidence",
	}})
	toolCallCount := 0
	toolSet := newTestToolSet([]string{TerminalRunToolName})
	registerTestTool(toolSet, ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"do the side effect"}}`,
		finishMessageWithEvidence("완료했습니다.", "obs-001", TerminalRunToolName, 0),
	}})
	request := kernelTestRequest("서버에 배포 스크립트 실행해줘")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected side-effect task to proceed: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusBlocked {
		t.Fatalf("missing required evidence must not hard-block; task should proceed, got %q", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the side-effect tool to run once, got %d", toolCallCount)
	}
}

type routerLedgerLanguageModel struct {
	decision   TurnDecision
	response   llm.StructuredResponse
	errorValue error
}

func (model *routerLedgerLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("router model only serves structured routing")
}

func (model *routerLedgerLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != turnRouterSchemaName {
		return llm.StructuredResponse{Content: "{}"}, nil
	}
	if model.errorValue != nil {
		return model.response, model.errorValue
	}
	document, errorValue := json.Marshal(model.decision)
	if errorValue != nil {
		return llm.StructuredResponse{}, errorValue
	}
	response := model.response
	response.Content = string(document)
	return response, nil
}

func persistedTurnRouterCallRecords(taskEvents []task.TaskEvent) []llmCallRecord {
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

type turnRouterDecisionLanguageModel struct {
	initialDecision TurnDecision
	reaskDecision   TurnDecision
	reaskCallCount  int
}

func (model *turnRouterDecisionLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("intake model only serves structured routing")
}

func (model *turnRouterDecisionLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if structuredResponseRequest.StructuredOutputSchema.Name == operationContractSchemaName {
		return llm.StructuredResponse{Content: operationContractTestDocument(structuredResponseRequest.StructuredOutputSchema.Document)}, nil
	}
	if structuredResponseRequest.StructuredOutputSchema.Name == operationContractReviewSchemaName {
		return llm.StructuredResponse{Content: `{"isComplete":true,"reason":""}`}, nil
	}
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
		TaskLevel:        TaskLevelLow,
		EstimatedMinutes: 1,
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
	registerTestTool(toolSet, ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
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

func TestAgentKernelDoesNotReaskMaintenanceEvidenceWithoutInitialTool(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: TurnDecision{
			Route:            TurnRouteStartTask,
			Classification:   IntakeClassificationBoundedTask,
			TaskShape:        TaskShapeMaintenanceTask,
			TaskLevel:        TaskLevelLow,
			EstimatedMinutes: 1,
			ResponseLanguage: "ko",
			Reason:           "register requested work",
		},
		reaskDecision: TurnDecision{RequiredEvidenceTools: []string{"task.add"}},
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)

	toolCallCount := 0
	toolSet := newTestCapabilityToolSet([]string{"task.add"})
	registerTestTool(toolSet, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"task-1","content":"신규 입사자 온보딩 문서 검토"}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.add","toolInput":{"prompt":"신규 입사자 온보딩 문서 검토"}}`,
		finishMessageWithEvidence("업무를 등록했습니다.", "obs-001", "task.add", 0),
	}})

	request := kernelTestRequest("신규 입사자 온보딩 문서 검토하는 업무 하나 등록해줘")
	request.ToolSet = toolSet
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected maintenance task to return a blocked result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected maintenance task without an initial direct tool to block, got %q", result.TaskRun.Status)
	}
	if intakeLanguageModel.reaskCallCount != 0 {
		t.Fatalf("expected no evidence re-ask without a typed side-effect signal, got %d", intakeLanguageModel.reaskCallCount)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected task.add to remain unavailable without an initial direct tool, got %d", toolCallCount)
	}
	if countTaskEvents(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), requiredEvidenceReaskEventName) != 0 {
		t.Fatal("did not expect a required-evidence reask event")
	}
}

func TestAgentKernelRepairsReadOnlyEvidenceWithoutDroppingRouterTools(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: TurnDecision{
			Route:                 TurnRouteStartTask,
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeScheduledTask,
			TaskLevel:             TaskLevelLow,
			EstimatedMinutes:      1,
			RequiredEvidenceTools: []string{"task.list"},
			InitialToolNames:      []string{"file.read"},
			ResponseLanguage:      "ko",
			Reason:                "update requested work",
		},
		reaskDecision: TurnDecision{RequiredEvidenceTools: []string{"task.update"}},
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)

	toolCallCount := 0
	toolSet := newTestToolSet([]string{"file.read", "task.history", "task.update"})
	registerTestTool(toolSet, ToolDefinition{Name: "task.update"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"task-1","content":"고객지원 분기 결산 검토 완료","endDate":"2026-07-17"}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task.update","toolInput":{"taskID":"task-1","content":"고객지원 분기 결산 검토 완료","endDate":"2026-07-17"}}`,
		finishMessageWithEvidence("업무를 수정했습니다.", "obs-001", "task.update", 0),
	}})

	request := kernelTestRequest("고객지원 분기 결산 업무를 검토 완료로 바꾸고 마감일은 7월 17일로 유지해줘")
	request.ToolSet = toolSet
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected corrected task.update evidence to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected corrected task to complete, got %q", result.TaskRun.Status)
	}
	if intakeLanguageModel.reaskCallCount != 1 {
		t.Fatalf("expected exactly one evidence re-ask, got %d", intakeLanguageModel.reaskCallCount)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected task.update to run once, got %d", toolCallCount)
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, requiredEvidenceReaskEventName, `"recoveredEvidence":["task.update"]`) {
		t.Fatal("expected reask event to record corrected task.update evidence")
	}
	if taskEventsContain(taskEvents, "agent.intake", "task.history") {
		t.Fatal("expected corrected evidence to replace task.history")
	}
	if !taskEventsContain(taskEvents, "agent.intake", "task.update") {
		t.Fatal("expected rebuilt intake decision to require task.update")
	}
	if !taskEventsContain(taskEvents, "agent.instructions_loaded", `"selectedToolNames":["file.read","task.update"]`) {
		t.Fatal("expected the router tool and recovered evidence to remain in the active goal")
	}
}

func TestAgentKernelDoesNotLetInvalidEvidenceChooseExecution(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: TurnDecision{
			Route:                 TurnRouteStartTask,
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeScheduledTask,
			TaskLevel:             TaskLevelLow,
			EstimatedMinutes:      1,
			RequiredEvidenceTools: []string{"task.history"},
			ResponseLanguage:      "ko",
			Reason:                "deployment requested",
		},
		reaskDecision: TurnDecision{RequiredEvidenceTools: []string{"task.list"}},
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)

	toolCallCount := 0
	toolSet := newTestToolSet([]string{TerminalRunToolName, "task.list"})
	registerTestTool(toolSet, ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"do the side effect"}}`,
		finishMessageWithEvidence("완료했습니다.", "obs-001", TerminalRunToolName, 0),
	}})

	request := kernelTestRequest("서버에 배포 스크립트 실행해줘")
	request.ToolSet = toolSet
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected task to stop safely after rejecting read-only recovery: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected direct execution to complete with its own evidence, got %q", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected direct terminal tool to run once, got %d calls", toolCallCount)
	}
	if intakeLanguageModel.reaskCallCount != 1 {
		t.Fatalf("expected exactly one re-ask attempt, got %d", intakeLanguageModel.reaskCallCount)
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, requiredEvidenceReaskEventName, `"didRecoverEvidence":true`) {
		t.Fatal("expected read-only task.list not to recover side-effect evidence")
	}
	if !taskEventsContain(taskEvents, requiredEvidenceReaskEventName, "no valid required evidence") {
		t.Fatal("expected rejected read-only evidence reason")
	}
}

func TestAgentKernelContinuesWhenEmptyReaskHasNoWrongContract(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: sideEffectMissingEvidenceDecision(),
		reaskDecision:   sideEffectMissingEvidenceDecision(),
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)
	toolCallCount := 0
	toolSet := newTestToolSet([]string{TerminalRunToolName})
	registerTestTool(toolSet, ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"do the side effect"}}`,
		finishMessageWithEvidence("완료했습니다.", "obs-001", TerminalRunToolName, 0),
	}})

	request := kernelTestRequest("서버에 배포 스크립트 실행해줘")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected task to proceed without an incorrect contract: %v", errorValue)
	}
	if result.TaskRun.Status == task.TaskStatusBlocked {
		t.Fatalf("expected empty evidence recovery to remain a soft fallback, got %q", result.TaskRun.Status)
	}
	if intakeLanguageModel.reaskCallCount != 1 {
		t.Fatalf("expected exactly one re-ask attempt, got %d", intakeLanguageModel.reaskCallCount)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the side-effect tool to run once, got %d calls", toolCallCount)
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, requiredEvidenceReaskEventName, `"wasAttempted":true`) {
		t.Fatal("expected reask event reporting the attempt")
	}
}

func TestRequiredEvidenceMatchesSelectedCandidates(t *testing.T) {
	if !requiredEvidenceMatchesCandidates([]string{"task.update"}, []string{"task.update"}) {
		t.Fatal("expected exact selected evidence candidate to match")
	}
	if requiredEvidenceMatchesCandidates([]string{"task.list"}, []string{"task.update"}) {
		t.Fatal("expected evidence outside selected candidates to be rejected")
	}
	if requiredEvidenceMatchesCandidates(nil, nil) {
		t.Fatal("expected empty evidence to be rejected")
	}
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
	if result.TaskRun.Status != task.TaskStatusCompleted {
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
		response: llm.StructuredResponse{
			ProviderName: "sdkd",
			ModelName:    "router-model",
			ModelTier:    "xlow",
			Usage: llm.Usage{
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
	if records[0].Provider != "sdkd" || records[0].Model != "router-model" || records[0].ModelTier != "xlow" || records[0].UsedFallback {
		t.Fatalf("expected SDKD router metadata without fallback, got %+v", records[0])
	}
	if records[0].PromptTokens != 11 || records[0].CompletionTokens != 7 || records[0].TotalTokens != 18 {
		t.Fatalf("expected router token metadata, got %+v", records[0])
	}
}

func TestAgentKernelPersistsTurnRouterFailureWithoutFallbackRoute(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(&routerLedgerLanguageModel{
		response: llm.StructuredResponse{
			ProviderName: "sdkd",
			ModelName:    "router-model",
		},
		errorValue: errors.New("router unavailable"),
	})
	agentKernel.UseLanguageModelProvider(&recoveryChatNoticeProvider{chatReply: "요청을 분류하지 못해 이번 작업을 시작하지 못했습니다. 다시 요청해 주세요."})

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), kernelTestRequest("오늘 무슨 요일이야?"))
	if errorValue != nil {
		t.Fatalf("expected persisted router failure result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusFailed || result.FailureNotice.Source != "generated" {
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
	siteToolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		Name:            "site.publish",
		Namespace:       "site",
		SideEffectClass: ToolSideEffectExternalPublish,
	}})
	intakeDecision := promoteArtifactTaskLevel(AgentRequest{ToolSet: siteToolSet}, IntakeDecision{
		TaskLevel:             TaskLevelLow,
		EstimatedMinutes:      1,
		RequiredEvidenceTools: []string{"site.publish"},
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

func TestAgentKernelCountsIntakeTimeTowardTaskWorkDuration(t *testing.T) {
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
		t.Fatalf("expected elapsed task result: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected intake time to exhaust the task budget, got %+v", result.TaskRun)
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected no task action after elapsed intake, got %d requests", len(languageModel.requests))
	}
	if !taskEventsContain(taskRunService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
		t.Fatal("expected persisted max_elapsed event")
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
		Reason:             "SDKD topology diagnostic",
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
