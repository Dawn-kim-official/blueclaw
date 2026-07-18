package agent

import (
	"strings"
	"testing"
)

func TestSelectedRequiredAttachmentSuffixesStayAdvisoryForSlides(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "presentation"}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	suffixes := selectedRequiredAttachmentSuffixes(instructionBundle, "Hermes Agent 장단점 분석 6장 ppt 만들어줘. html만 주면 돼")

	if len(suffixes) != 0 {
		t.Fatalf("expected no hard suffix contract, got %+v", suffixes)
	}
}

func TestSelectedEvidenceHintsComeFromSelectedSkills(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "site-prototype"}, {Name: "calendar"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
	}

	toolNames := selectedEvidenceHintTools(instructionBundle)

	if len(toolNames) != 3 || toolNames[0] != "site.create" || toolNames[1] != "terminal.run" || toolNames[2] != "site.publish" {
		t.Fatalf("expected selected skill evidence tools, got %+v", toolNames)
	}
}

func TestOutcomeContractUsesIntakeRequiredEvidenceWithoutToolFallback(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "내일 오후 3시에 이 메시지 보내줘",
			ToolSet: newTestToolSet([]string{"task.add", "schedule.create"}),
		},
		IntakeDecision{
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeScheduledTask,
			RequiredEvidenceTools: []string{"schedule.create"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "schedule.create") {
		t.Fatalf("expected schedule.create required evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "task.add") {
		t.Fatalf("expected explicit required evidence not to add fallback evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractPreservesActiveGoalEvidence(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.delete"},
		}}},
		IntakeDecision{
			Classification:        IntakeClassificationBoundedTask,
			RequiredEvidenceTools: []string{"file.delete"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "site.delete") || stringSliceContains(contract.RequiredEvidenceTools, "file.delete") {
		t.Fatalf("expected active goal evidence to remain authoritative, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractDoesNotFallbackToScheduleCreateForScheduledTaskShape(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "내일 오후 3시에 이 메시지 보내줘",
			ToolSet: newTestToolSet([]string{"schedule.create"}),
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeScheduledTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "schedule.create") {
		t.Fatalf("expected scheduled task shape not to create fallback evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractCreatesExpectedResultsForSitePublish(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지 만들어서 배포해줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask, RequiredEvidenceTools: []string{"site.publish"}},
		InstructionBundle{},
		ExecutionPlan{PublicDeploy: true},
		true,
		nil,
	)

	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected site publish contract to require public link result, got %+v", contract.ExpectedResults)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeMessage, "최종 답변") {
		t.Fatalf("expected site publish contract to include final message result, got %+v", contract.ExpectedResults)
	}
}

func TestOutcomeContractDoesNotRequirePublicLinkForSiteDelete(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "방금 배포한 테스트 웹사이트를 삭제해줘",
			ToolSet: newTestToolSet([]string{"site.delete"}),
		},
		IntakeDecision{
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeMaintenanceTask,
			RequiredEvidenceTools: []string{"site.delete"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected site delete not to require public link result, got %+v", contract.ExpectedResults)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "site.delete") {
		t.Fatalf("expected site.delete evidence to remain, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractRequiresCurrentEffectsForSiteModification(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "예쁜 귤 웹사이트 퀄리티가 너무 낮아. 더 예쁘게 해줘.",
			ToolSet: newTestToolSet([]string{"site.status", "file.edit", "site.publish"}),
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEffects) != 0 {
		t.Fatalf("expected no text-derived site effects without explicit contract, got %+v", contract.RequiredEffects)
	}
}

func TestOutcomeContractCreatesExpectedResultsForRequestedFile(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "pptx 파일 만들어줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{".pptx"}},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "파일") {
		t.Fatalf("expected file output contract, got %+v", contract.ExpectedResults)
	}
	if len(contract.ExpectedResults[0].AcceptanceHints) == 0 || contract.ExpectedResults[0].AcceptanceHints[0] != ".pptx" {
		t.Fatalf("expected suffix hint to be preserved, got %+v", contract.ExpectedResults)
	}
}

func TestOutcomeContractKeepsRequestedFileWhenSiteSkillOnlySelected(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "site-prototype"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		RequiredEvidenceTools: []string{"site.status", "site.publish"},
	}
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt: "기업 문서 가이드를 docx로 만들어줘",
			ToolSet: newTestToolSetWithDefinitions([]ToolDefinition{
				{Name: "site.status", Namespace: "site", SideEffectClass: ToolSideEffectRead},
				{Name: "site.publish", Namespace: "site", SideEffectClass: ToolSideEffectExternalPublish},
			}),
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{".docx"}},
		instructionBundle,
		ExecutionPlan{},
		false,
		[]string{".docx"},
	)

	if len(contract.RequiredAttachmentSuffixes) != 1 || contract.RequiredAttachmentSuffixes[0] != ".docx" {
		t.Fatalf("expected selected site skill not to clear requested file suffix, got %+v", contract.RequiredAttachmentSuffixes)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file.deliver") {
		t.Fatalf("expected file.deliver requirement for requested file, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "site.publish") {
		t.Fatalf("expected selected site skill not to require site publish, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.SelectedEvidenceHints, "site.publish") {
		t.Fatalf("expected selected site skill not to keep stale site hint, got %+v", contract.SelectedEvidenceHints)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "파일") {
		t.Fatalf("expected file result for requested attachment, got %+v", contract.ExpectedResults)
	}
	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected selected site skill not to require public link, got %+v", contract.ExpectedResults)
	}
}

func TestResolvedInputDischargesAskInputContract(t *testing.T) {
	request := AgentRequest{
		ExistingTaskRunID: "task-1",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1",
			Status:    ActiveGoalStatusWaitingUserInput,
		},
	}
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{AskInputToolName, "task.update"},
		RequiredEvidenceAnyOf: [][]string{{AskInputToolName, "task.update"}},
		SelectedEvidenceHints: []string{AskInputToolName},
		ExpectedResults: []ExpectedResult{{
			ID:              "choice",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice",
			Required:        true,
			AcceptanceHints: []string{AskInputToolName},
		}, {
			ID:              "choice-update",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice applied",
			Required:        true,
			AcceptanceHints: []string{AskInputToolName, "task.update"},
		}},
		OperationContract: &OperationContract{
			Version: operationContractVersion,
			Requirements: []OperationRequirement{
				{RequirementID: "ask", ToolName: AskInputToolName},
				{RequirementID: "update", ToolName: "task.update"},
			},
		},
	}

	resolvedContract := dischargeResolvedInputContract(request, TurnDecision{Route: TurnRouteContinueTask}, contract)

	if stringSliceContains(resolvedContract.RequiredEvidenceTools, AskInputToolName) ||
		stringSliceContains(resolvedContract.SelectedEvidenceHints, AskInputToolName) {
		t.Fatalf("expected resolved ask.input requirements to be discharged, got %+v", resolvedContract)
	}
	if len(resolvedContract.ExpectedResults) != 1 ||
		len(resolvedContract.ExpectedResults[0].AcceptanceHints) != 1 ||
		resolvedContract.ExpectedResults[0].AcceptanceHints[0] != "task.update" {
		t.Fatalf("expected mixed expected result to retain its remaining hint, got %+v", resolvedContract.ExpectedResults)
	}
	if len(resolvedContract.RequiredEvidenceAnyOf) != 1 || !stringSliceContains(resolvedContract.RequiredEvidenceAnyOf[0], "task.update") {
		t.Fatalf("expected unrelated evidence alternative to remain, got %+v", resolvedContract.RequiredEvidenceAnyOf)
	}
	if resolvedContract.OperationContract == nil || len(resolvedContract.OperationContract.Requirements) != 1 || resolvedContract.OperationContract.Requirements[0].ToolName != "task.update" {
		t.Fatalf("expected unrelated operation requirement to remain, got %+v", resolvedContract.OperationContract)
	}
}

func TestUnresolvedInputKeepsAskInputContract(t *testing.T) {
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{AskInputToolName},
		ExpectedResults: []ExpectedResult{{
			ID:              "choice",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice",
			Required:        true,
			AcceptanceHints: []string{AskInputToolName},
		}},
	}
	request := AgentRequest{
		ExistingTaskRunID: "task-1",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1",
			Status:    ActiveGoalStatusWaitingUserInput,
		},
	}

	for _, turnDecision := range []TurnDecision{{Route: TurnRouteStartTask}, {Route: TurnRouteAnswerQuestion}} {
		unresolvedContract := dischargeResolvedInputContract(request, turnDecision, contract)
		if !stringSliceContains(unresolvedContract.RequiredEvidenceTools, AskInputToolName) || len(unresolvedContract.ExpectedResults) != 1 {
			t.Fatalf("expected route %s to preserve ask.input contract, got %+v", turnDecision.Route, unresolvedContract)
		}
	}
	request.ExistingTaskRunID = "task-2"
	mismatchedContract := dischargeResolvedInputContract(request, TurnDecision{Route: TurnRouteContinueTask}, contract)
	if !stringSliceContains(mismatchedContract.RequiredEvidenceTools, AskInputToolName) {
		t.Fatal("expected another task's continuation not to discharge ask.input")
	}
}

func TestOutcomeContractDoesNotTreatReplyInstructionAsExternalSend(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{Name: "direct-message"}, {Name: "site-prototype"}},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "direct-message", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
		RequiredEvidenceTools: []string{"message.send", "site.status", "site.publish"},
	}

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지를 만들어서 배포하고 URL만 알려줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequiredEvidenceTools: []string{"site.publish"}},
		instructionBundle,
		ExecutionPlan{PublicDeploy: true},
		true,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected reply instruction not to require external send evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "site.publish") {
		t.Fatalf("expected site publish evidence to remain required, got %+v", contract.RequiredEvidenceTools)
	}
}

func expectedResultsContain(results []ExpectedResult, resultType string, descriptionFragment string) bool {
	for _, result := range results {
		if result.Type == resultType && strings.Contains(result.Description, descriptionFragment) {
			return true
		}
	}
	return false
}

func TestOutcomeContractIgnoresSelectedDirectMessageForNonSendGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message.send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(AgentRequest{Prompt: "https://example.com 참고해서 사업계획서 작성해줘"}, intakeDecision, instructionBundle, ExecutionPlan{}, false, nil)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no DM hard gate for non-send goal, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.SelectedEvidenceHints) != 1 || contract.SelectedEvidenceHints[0] != "message.send" {
		t.Fatalf("expected selected evidence hint to be retained, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestOutcomeContractDoesNotPromoteDirectMessageHintForAttachmentFollowUp(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message.send"},
	}
	request := AgentRequest{
		Prompt: "다시 시도해보자",
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Path:        "home/inbox/mattermost/direct/post/kim-intern-automation.html",
				ContentType: "text/html",
			}},
		},
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"message.send"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected attachment follow-up not to require DM send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractIgnoresMailKeywordForArtifactAttachmentGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message.send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "메일, 일정, 브라우저 제어 능력을 소개하는 5장짜리 발표자료를 PPTX로 첨부해줘"},
		intakeDecision,
		instructionBundle,
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected artifact attachment request not to require DM send evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file.deliver") {
		t.Fatalf("expected artifact attachment request to require file.deliver, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractRequiresSendEvidenceForExternalSendPlan(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message.send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}

	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		Name:            "message.send",
		Namespace:       "message",
		SideEffectClass: ToolSideEffectExternalSend,
	}})
	contract := outcomeContractForRequest(AgentRequest{Prompt: "샘플에게 테스트라고 DM 보내줘", ToolSet: toolSet}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "message.send" {
		t.Fatalf("expected send hard gate for external send goal, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractUsesIntakeSendEvidenceForExternalSend(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "샘플에게 테스트라고 DM 보내줘",
			ToolSet: testToolSet([]string{"message.send"}),
		},
		IntakeDecision{
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeMaintenanceTask,
			RequiredEvidenceTools: []string{"message.send"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "message.send" {
		t.Fatalf("expected intake required evidence to require message.send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractIgnoresIntakeSendEvidenceForCurrentConversationReply(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{{
		Name:            "message.send",
		Namespace:       "message",
		SideEffectClass: ToolSideEffectExternalSend,
	}})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "안녕. 짧게 인사로 답해줘.",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeMaintenanceTask,
			RequiredEvidenceTools: []string{"message.send"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		true,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected current-conversation reply not to require message.send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractKeepsSendEvidenceForExternalSendContinuation(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt: "해",
			ActiveGoal: ActiveGoal{
				OriginalInstruction: "샘플에게 테스트라고 DM 보내줘",
				OutcomeContract: OutcomeContract{
					RequiredEvidenceTools: []string{"message.send"},
				},
			},
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected external send continuation to keep message.send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractUsesIntakeFlowTaskAddEvidence(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "업무 등록해줘\n- 메일 페이지 앱 비밀번호 개선하기",
			ToolSet: testToolSet([]string{"task.add", "task.list", "task.update"}),
		},
		IntakeDecision{
			Classification:        IntakeClassificationBoundedTask,
			TaskShape:             TaskShapeMaintenanceTask,
			RequiredEvidenceTools: []string{"task.add"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "task.add" {
		t.Fatalf("expected flow task add hard gate, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractDoesNotDeriveEvidenceFromPromptAndAvailableTools(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		toolNames []string
	}{
		{
			name:      "flow task",
			prompt:    "업무 등록해줘",
			toolNames: []string{"task.add"},
		},
		{
			name:      "external send",
			prompt:    "샘플에게 테스트라고 DM 보내줘",
			toolNames: []string{"message.send"},
		},
	}

	for _, test := range tests {
		contract := outcomeContractForRequest(
			AgentRequest{
				Prompt:  test.prompt,
				ToolSet: testToolSet(test.toolNames),
			},
			IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
			InstructionBundle{},
			ExecutionPlan{},
			false,
			nil,
		)
		if len(contract.RequiredEvidenceTools) != 0 {
			t.Fatalf("expected %s prompt and available tools not to derive evidence, got %+v", test.name, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractSelectsOneFlowTaskEvidenceHintForCurrentOperation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "internkim-flow"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredEvidenceTools: []string{"task.add", "task.list", "task.update"},
	}
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "이번 주 업무 목록 보여줘",
			ToolSet: testToolSet([]string{"task.add", "task.list", "task.update"}),
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask, RequiredEvidenceTools: []string{"task.list"}},
		instructionBundle,
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "task.list" {
		t.Fatalf("expected only task.list evidence for list operation, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeReferenceToolSetHidesSendAndSiteToolsForDocumentGoal(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{Name: "web.fetch", Namespace: "web", SideEffectClass: ToolSideEffectRead},
		{Name: "file.write", Namespace: "file", SideEffectClass: ToolSideEffectWorkspaceWrite},
		{Name: "file.deliver", Namespace: "file", SideEffectClass: ToolSideEffectExternalWrite},
		{Name: "site.create", Namespace: "site", SideEffectClass: ToolSideEffectExternalWrite},
		{Name: "site.publish", Namespace: "site", SideEffectClass: ToolSideEffectExternalPublish},
		{Name: "message.send", Namespace: "message", SideEffectClass: ToolSideEffectExternalSend},
		{Name: "mail.message.send", Namespace: "mail", SideEffectClass: ToolSideEffectExternalSend},
	})
	contract := OutcomeContract{
		SelectedEvidenceHints: []string{"site.create", "site.publish", "message.send", "mail.message.send"},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "https://example.com 참고해서 사업계획서 작성해줘"}, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"web.fetch", "file.write", "file.deliver"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"site.create", "site.publish", "message.send", "mail.message.send"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to be hidden for document goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetExposesPinnedNonKernelTools(t *testing.T) {
	toolSet := testToolSet([]string{"web.search", "web.fetch", "terminal.run", "file.write"})
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "presentation", ToolReferences: []string{"terminal.run", "file.write"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, AgentRequest{
		Prompt:          "https://example.com 참고해서 ppt 만들어줘",
		PinnedToolNames: []string{"web.search", "web.fetch", "terminal.run", "file.write"},
	}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"terminal.run", "file.write", "web.search", "web.fetch"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsSiteToolsForSiteGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "site.create", "site.publish"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "웹사이트 하나 만들어서 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{
		RequiredEvidenceTools: []string{"site.create", "site.publish"},
	})

	for _, toolName := range []string{"site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for site goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsActiveGoalEvidenceToolsForContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "site.create", "site.publish"})
	request := AgentRequest{
		Prompt: "다시 해봐 그럼 될 거야",
		ActiveGoal: ActiveGoal{OriginalInstruction: "웹사이트 하나 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
		}},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, request, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for active site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetHidesSiteToolsForActiveGoalContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "site.create", "site.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "site-prototype",
			ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "다시 해봐 그럼 될 거야",
		PinnedToolNames: []string{"terminal.run", "site.create", "site.publish"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "웹사이트 하나 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	// Continuation of an active site goal keeps site.* exposed because it is still pinned.
	for _, toolName := range []string{"terminal.run", "site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available for an active site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetHidesSelectedSiteSkillToolsWhenActiveGoalWasAttachmentFallback(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "file.deliver", "site.create", "site.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "site-prototype",
			ToolReferences: []string{"terminal.run", "site.create", "site.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "다시 해봐",
		PinnedToolNames: []string{"terminal.run", "site.create", "site.publish"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "개인 홈페이지를 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{"file.deliver"},
			RequiredAttachmentSuffixes: []string{".html"},
			SelectedEvidenceHints:      []string{"site.create", "terminal.run", "site.publish"},
			ArtifactRequirement:        ArtifactRequirementRequired,
		}},
	}
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeImmediateReply}, instructionBundle, ExecutionPlan{}, false, nil)

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	// A selected site skill keeps site.* exposed because it is still pinned, alongside the kernel tools.
	for _, toolName := range []string{"terminal.run", "file.deliver", "site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available after selected site skill, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeContractRequiresActiveGoalRequiredEvidenceForContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "site-prototype"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
		RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
	}
	request := AgentRequest{
		Prompt: "다시 해봐 그럼 될 거야",
		ActiveGoal: ActiveGoal{OriginalInstruction: "웹사이트 하나 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.create", "site.publish"},
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"site.create", "site.publish"} {
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected active site continuation to require %s evidence, got %+v", toolName, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractPreservesSiteGoalDuringApprovalContinuation(t *testing.T) {
	request := AgentRequest{
		Prompt:                 "확인",
		IsApprovalContinuation: true,
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.delete"},
			SelectedEvidenceHints: []string{"site.delete"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, InstructionBundle{}, ExecutionPlan{}, false, nil)

	if !stringSliceContains(contract.RequiredEvidenceTools, "site.delete") {
		t.Fatalf("expected site.delete evidence to remain, got %+v", contract)
	}
}

func TestAgentTurnToolSetExposesSendToolForActiveSendContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"message.send", "file.write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message.send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "다시 해줘",
		PinnedToolNames: []string{"message.send"},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "샘플에게 테스트라고 DM 보내줘",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"message.send"},
				SelectedEvidenceHints: []string{"message.send"},
			},
		},
	}
	contract := OutcomeContract{RequiredEvidenceTools: []string{"message.send"}, SelectedEvidenceHints: []string{"message.send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected pinned send tool to remain available for continuation, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAgentTurnToolSetHidesUnrequestedSendToolForAttachmentFollowUp(t *testing.T) {
	toolSet := testToolSet([]string{"message.send", "file.preview", "file.read"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message.send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "다시 시도해보자",
		PinnedToolNames: []string{"file.preview"},
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Path:        "home/inbox/mattermost/direct/post/kim-intern-automation.html",
				ContentType: "text/html",
			}},
		},
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"message.send"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"message.send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected selected direct send tool to remain available, got %+v", filteredToolSet.ListToolNames())
	}
	if !filteredToolSet.IsAllowed("file.preview") {
		t.Fatalf("expected attachment preview to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestOutcomeReferenceToolSetKeepsSendToolsForExplicitSendGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "message.send", "mail.message.send"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "샘플에게 DM 보내줘"}, ExecutionPlan{}, false, OutcomeContract{RequiredEvidenceTools: []string{"message.send"}})

	if !filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected DM send to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestConfirmationHintsIgnoreUnrelatedSelectedSkillEvidence(t *testing.T) {
	hints := confirmationEvidenceHintsForRequest(
		AgentRequest{Prompt: "https://example.com 참고해서 사업계획서 작성해줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask},
		[]string{"site.publish", "message.send"},
	)

	if len(hints) != 0 {
		t.Fatalf("expected unrelated skill evidence not to force confirmation planning, got %+v", hints)
	}
}

func TestAttachmentSuffixesComeFromStructuredOutputFormats(t *testing.T) {
	suffixes := attachmentSuffixesForRequestedOutputFormats([]string{"html", "pdf", "html"})

	if len(suffixes) != 2 || suffixes[0] != ".html" || suffixes[1] != ".pdf" {
		t.Fatalf("expected structured output format suffixes, got %+v", suffixes)
	}
}
