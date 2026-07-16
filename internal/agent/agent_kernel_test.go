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

func TestAgentKernelConsumeRouteSuppressesReply(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
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
	toolSet.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskID":"task-1","content":"메일 페이지 앱 비밀번호 개선"}`), nil
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
	toolSet.RegisterTool(ToolDefinition{Name: "calendar.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"created":true}`), nil
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

func TestAgentKernelPrunesInvalidEvidenceOnApprovalContinuation(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
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
	toolSet := newTestToolSet([]string{"site.delete"})
	toolSet.RegisterTool(ToolDefinition{Name: "site.delete"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"deleted":true}`), nil
	})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.delete","toolInput":{"siteID":"site-1"}}`,
		finishMessageWithEvidence("웹사이트를 삭제했습니다.", "obs-001", "site.delete", 0),
	}})

	request := kernelTestRequest("응 확인했어, 진행해줘")
	request.ToolSet = toolSet
	request.IsApprovalContinuation = true
	request.ActiveGoal = ActiveGoal{
		GoalID:              "goal-approval-continuation",
		TaskRunID:           "task-approval-continuation",
		OriginalInstruction: "테스트 웹사이트를 삭제해줘",
		CurrentObjective:    "site.delete 승인 후 실행",
		Status:              ActiveGoalStatusActive,
		OutcomeContract:     OutcomeContract{RequiredEvidenceTools: []string{"site.delete"}},
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
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, requiredEvidenceInvalidEventName, "delete_website_artifact") {
		t.Fatal("expected pruned invalid evidence event")
	}
	if !taskEventsContain(taskEvents, requiredEvidenceInvalidEventName, "pruned") {
		t.Fatal("expected prune reason on the invalid evidence event")
	}
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
	toolSet.RegisterTool(ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
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
	toolSet.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskID":"task-1","content":"신규 입사자 온보딩 문서 검토"}`), nil
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

func TestAgentKernelReplacesReadOnlyEvidenceForScheduledTask(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	intakeLanguageModel := &turnRouterDecisionLanguageModel{
		initialDecision: TurnDecision{
			Route:                 TurnRouteStartTask,
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeScheduledTask,
			TaskLevel:             TaskLevelLow,
			EstimatedMinutes:      1,
			RequiredEvidenceTools: []string{"task.list"},
			InitialToolNames:      []string{"task.update"},
			ResponseLanguage:      "ko",
			Reason:                "update requested work",
		},
		reaskDecision: TurnDecision{RequiredEvidenceTools: []string{"task.update"}},
	}
	agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)

	toolCallCount := 0
	toolSet := newTestToolSet([]string{"task.history", "task.update"})
	toolSet.RegisterTool(ToolDefinition{Name: "task.update"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskID":"task-1","content":"고객지원 분기 결산 검토 완료","endDate":"2026-07-17"}`), nil
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
	toolSet.RegisterTool(ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
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
	toolSet.RegisterTool(ToolDefinition{Name: TerminalRunToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"exitCode":0,"stdout":"done","stderr":"","timedOut":false}`), nil
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
	agentKernel.UseLanguageModelProvider(fixedReplyLanguageModel{reply: "요청을 분류하지 못해 이번 작업을 시작하지 못했습니다. 다시 요청해 주세요."})

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
	intakeDecision := promoteArtifactTaskLevel(AgentRequest{}, IntakeDecision{
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
