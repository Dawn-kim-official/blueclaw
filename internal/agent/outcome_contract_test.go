package agent

import (
	"strings"
	"testing"
)

func TestSelectedRequiredAttachmentSuffixesStayAdvisoryForSlides(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "presentation",
			Completion: SkillCompletion{
				RequiredAttachmentSuffixes: []string{".pptx", ".pdf", ".html", "-notes.txt"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	suffixes := selectedRequiredAttachmentSuffixes(instructionBundle, "Hermes Agent 장단점 분석 6장 ppt 만들어줘. html만 주면 돼")

	if len(suffixes) != 0 {
		t.Fatalf("expected no hard suffix contract, got %+v", suffixes)
	}
}

func TestSelectedEvidenceHintsComeFromSelectedSkills(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name: "site-prototype",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
				},
			},
			{
				Name: "calendar",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"calendar.add"},
				},
			},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
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
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask, SiteRequestEvidence: "개인 홈페이지 만들어서 배포해줘"},
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
			SiteRequestEvidence:   "테스트 웹사이트를 삭제",
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

func TestOutcomeContractTreatsWebsiteHTMLFormatAsPublicLink(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지를 만들어서 배포해줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{"html"}, SiteRequestEvidence: "개인 홈페이지를 만들어서 배포해줘"},
		InstructionBundle{},
		ExecutionPlan{PublicDeploy: true},
		true,
		[]string{".html"},
	)

	if len(contract.RequiredAttachmentSuffixes) != 0 {
		t.Fatalf("expected no html attachment requirement for site publish, got %+v", contract.RequiredAttachmentSuffixes)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "file.deliver") {
		t.Fatalf("expected no file.deliver requirement for site publish, got %+v", contract.RequiredEvidenceTools)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected site publish to require a public link, got %+v", contract.ExpectedResults)
	}
	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "파일") {
		t.Fatalf("expected site publish not to require an attached file, got %+v", contract.ExpectedResults)
	}
	if contract.ArtifactRequirement == ArtifactRequirementRequired {
		t.Fatalf("expected no required artifact contract for site publish, got %+v", contract)
	}
	if contract.ArtifactRequirement != ArtifactRequirementNone {
		t.Fatalf("expected no artifact workflow preference for site publish, got %+v", contract.ArtifactRequirement)
	}
}

func TestOutcomeContractKeepsRequestedFileWhenSiteSkillOnlySelected(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "site-prototype",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.status", "site.publish"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "기업 문서 가이드를 docx로 만들어줘"},
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

// FLAGGED for human review: pre-existing bug, unrelated to the fixed-kernel/capability.invoke
// collapse (predates it — this test exists as far back as 93ff158). attachmentSuffixesForOutcomeContract
// only inspects request.ActiveGoal for an explicit file requirement and never sees
// intakeDecision.RequestedOutputFormats (the "html" passed in here), so an explicit output-format
// request on a fresh site request silently drops its required attachment suffix. Left failing
// pending a decision on whether attachmentSuffixesForOutcomeContract should also accept the intake
// decision's requested output formats as a signal.
func TestOutcomeContractKeepsExplicitWebsiteHTMLFileRequest(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지를 만들어서 배포하고 HTML 파일도 첨부해줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{"html"}, SiteRequestEvidence: "개인 홈페이지를 만들어서 배포"},
		InstructionBundle{},
		ExecutionPlan{PublicDeploy: true},
		true,
		[]string{".html"},
	)

	if len(contract.RequiredAttachmentSuffixes) != 1 || contract.RequiredAttachmentSuffixes[0] != ".html" {
		t.Fatalf("expected explicit html file request to keep suffix, got %+v", contract.RequiredAttachmentSuffixes)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file.deliver") {
		t.Fatalf("expected explicit html file request to require file.deliver, got %+v", contract.RequiredEvidenceTools)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected explicit site file request to still require public link, got %+v", contract.ExpectedResults)
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "파일") {
		t.Fatalf("expected explicit site file request to require attached file, got %+v", contract.ExpectedResults)
	}
}

func TestOutcomeContractDoesNotTreatReplyInstructionAsExternalSend(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name: "direct-message",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"message.send"},
				},
			},
			{
				Name: "site-prototype",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"site.status", "site.publish"},
				},
			},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "direct-message", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
	}

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지를 만들어서 배포하고 URL만 알려줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, SiteRequestEvidence: "개인 홈페이지를 만들어서 배포"},
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
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"message.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
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
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"message.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
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

func TestOutcomeContractDoesNotPromoteSiteHintsForMessageDeleteContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{
				Name: "mattermost",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"message.delete", "message.send"},
				},
			},
			{
				Name: "site-prototype",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"site.status", "artifact.review", "site.publish"},
				},
			},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "mattermost", Status: "selected"},
			{Name: "site-prototype", Status: "selected"},
		},
	}
	request := AgentRequest{
		Prompt: "해",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			ArtifactRequirement:   ArtifactRequirementNone,
			SelectedEvidenceHints: []string{"message.delete", "message.send", "site.status", "artifact.review", "site.publish"},
			Source:                "execution_plan",
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"site.status", "artifact.review", "site.publish"} {
		if stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected message delete continuation not to require %s, got %+v", toolName, contract)
		}
		if stringSliceContains(contract.SelectedEvidenceHints, toolName) {
			t.Fatalf("expected message delete continuation not to select %s, got %+v", toolName, contract)
		}
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "message.delete") {
		t.Fatalf("expected message delete continuation to require delete outcome, got %+v", contract)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "message.send") || stringSliceContains(contract.SelectedEvidenceHints, "message.send") {
		t.Fatalf("expected message delete continuation not to require separate send outcome, got %+v", contract)
	}
	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected no site link result for message delete continuation, got %+v", contract.ExpectedResults)
	}
}

func TestOutcomeContractIgnoresMailKeywordForArtifactAttachmentGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"message.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
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
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"message.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}

	contract := outcomeContractForRequest(AgentRequest{Prompt: "샘플에게 테스트라고 DM 보내줘"}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

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
		Skills: []SkillInstruction{{
			Name: "internkim-flow",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"task.add", "task.list", "task.update"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
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
	toolSet := testToolSet([]string{"web.fetch", "file.write", "file.deliver", "site.create", "site.publish", "message.send", "mail.message.send"})
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

func TestAgentTurnToolSetOnlyExposesKernelToolsDespitePinning(t *testing.T) {
	toolSet := testToolSet([]string{"web.search", "web.fetch", "terminal.run", "file.write"})
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "presentation", AllowedTools: []string{"terminal.run", "file.write"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, AgentRequest{
		Prompt:          "https://example.com 참고해서 ppt 만들어줘",
		PinnedToolNames: []string{"web.search", "web.fetch", "terminal.run", "file.write"},
	}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"terminal.run", "file.write"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected kernel tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	// web.search and web.fetch are non-kernel; pinning cannot expose them directly, only capability.invoke reaches them.
	for _, toolName := range []string{"web.search", "web.fetch"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected non-kernel pinned tool %s to stay out of the action schema, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsSiteToolsForSiteGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "site.create", "site.publish"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "웹사이트 하나 만들어서 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{
		RequiredEvidenceTools: []string{"site.create", "site.publish"},
		SiteEvidenceQuote:     "웹사이트 하나 만들어서 배포해줘",
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
			SiteEvidenceQuote:     "웹사이트 하나 만들어서 배포해줘",
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
			Name:         "site-prototype",
			AllowedTools: []string{"terminal.run", "site.create", "site.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "다시 해봐 그럼 될 거야",
		PinnedToolNames: []string{"terminal.run", "site.create", "site.publish"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "웹사이트 하나 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
			SiteEvidenceQuote:     "웹사이트 하나 만들어서 배포해줘",
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"}, SiteEvidenceQuote: "웹사이트 하나 만들어서 배포해줘"}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("terminal.run") {
		t.Fatalf("expected kernel tool terminal.run to remain available, got %+v", filteredToolSet.ListToolNames())
	}
	// Continuation of an active site goal no longer keeps site.* exposed directly; capability.invoke is the only path.
	for _, toolName := range []string{"site.create", "site.publish"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to stay out of the action schema even for an active site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetHidesSelectedSiteSkillToolsWhenActiveGoalWasAttachmentFallback(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "file.deliver", "site.create", "site.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"terminal.run", "site.create", "site.publish"},
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
			},
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
			SiteEvidenceQuote:          "개인 홈페이지를 만들어서 배포해줘",
			ArtifactRequirement:        ArtifactRequirementRequired,
		}},
	}
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeImmediateReply}, instructionBundle, ExecutionPlan{}, false, nil)

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"terminal.run", "file.deliver"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected kernel tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	// A selected site skill no longer exposes site.* directly; the model reaches it only through capability.invoke.
	for _, toolName := range []string{"site.create", "site.publish"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to stay out of the action schema after selected site skill, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeContractRequiresActiveGoalRequiredEvidenceForContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "site-prototype",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해봐 그럼 될 거야",
		ActiveGoal: ActiveGoal{OriginalInstruction: "웹사이트 하나 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.create", "site.publish"},
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
			SiteEvidenceQuote:     "웹사이트 하나 만들어서 배포해줘",
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"site.create", "site.publish"} {
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected active site continuation to require %s evidence, got %+v", toolName, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractStripsStaleUnverifiableSiteRequirement(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "site-prototype",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "버튼 색 바꿔줘",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "19일 오후 6시 어반브랜딩 미팅 추가 위치는 코엑스",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"site.create", "site.publish"},
				SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
				ExpectedResults: []ExpectedResult{{
					ID:          "site-public-link",
					Type:        ExpectedResultTypeLink,
					Description: "public URL",
					Required:    true,
				}},
				SiteEvidenceQuote: "웹사이트 만들어줘",
			},
		},
	}

	_, report := normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt, false)
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if !report.HasDrops() {
		t.Fatalf("expected stale site normalization report, got %+v", report)
	}
	for _, toolName := range []string{"site.create", "site.publish"} {
		if stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected stale site tool %s to be stripped, got %+v", toolName, contract)
		}
	}
	if expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected stale public link result to be stripped, got %+v", contract.ExpectedResults)
	}
	if strings.TrimSpace(contract.SiteEvidenceQuote) != "" {
		t.Fatalf("expected stale site evidence quote to be cleared, got %+v", contract)
	}
}

func TestOutcomeContractPreservesGenuineSiteGoalContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "site-prototype",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.create", "terminal.run", "site.publish"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "버튼 색 바꿔줘",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "포트폴리오 웹사이트 만들어서 배포해줘",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"site.create", "site.publish"},
				SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
				ExpectedResults: []ExpectedResult{{
					ID:          "site-public-link",
					Type:        ExpectedResultTypeLink,
					Description: "public URL",
					Required:    true,
				}},
				SiteEvidenceQuote: "웹사이트 만들어서 배포",
			},
		},
	}

	_, report := normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt, false)
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if report.HasDrops() {
		t.Fatalf("expected genuine site continuation to keep requirements, got %+v", report)
	}
	for _, toolName := range []string{"site.create", "site.publish"} {
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected genuine site tool %s to remain, got %+v", toolName, contract)
		}
	}
	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeLink, "public URL") {
		t.Fatalf("expected genuine public link result to remain, got %+v", contract.ExpectedResults)
	}
	if contract.SiteEvidenceQuote != "웹사이트 만들어서 배포" {
		t.Fatalf("expected site evidence quote to remain, got %+v", contract)
	}
}

func TestOutcomeContractPreservesSiteGoalDuringApprovalContinuation(t *testing.T) {
	request := AgentRequest{
		Prompt:                 "확인",
		IsApprovalContinuation: true,
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "방금 배포한 Local Fleet Studio 테스트 웹사이트를 삭제해줘.",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"site.delete"},
				SelectedEvidenceHints: []string{"site.delete"},
				SiteEvidenceQuote:     "Local Fleet Studio 웹사이트 생성 배포 수정 삭제",
			},
		},
	}

	_, report := normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt, true)
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, InstructionBundle{}, ExecutionPlan{}, false, nil)

	if report.HasDrops() {
		t.Fatalf("expected approval continuation to keep active site goal, got %+v", report)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "site.delete") {
		t.Fatalf("expected site.delete evidence to remain, got %+v", contract)
	}
}

func TestOutcomeContractRequiresSendEvidenceForActiveSendContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"message.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해줘",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"message.send"},
			SelectedEvidenceHints: []string{"message.send"},
		}},
	}
	request.ActiveGoal.OriginalInstruction = "샘플에게 테스트라고 DM 보내줘"

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if !stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected active send continuation to require send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestAgentTurnToolSetHidesSendToolForActiveSendContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"ask.confirm", "message.send", "file.write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "direct-message",
			AllowedTools: []string{"ask.confirm", "message.send"},
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

	if !filteredToolSet.IsAllowed("ask.confirm") {
		t.Fatalf("expected kernel tool ask.confirm to remain available, got %+v", filteredToolSet.ListToolNames())
	}
	// Even an active send continuation reaches message.send only through capability.invoke now.
	if filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected message.send to stay out of the action schema for continuation, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAgentTurnToolSetHidesUnrequestedSendToolForAttachmentFollowUp(t *testing.T) {
	toolSet := testToolSet([]string{"ask.confirm", "message.send", "file.preview", "file.read"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "direct-message",
			AllowedTools: []string{"ask.confirm", "message.send"},
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

	if filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected send tool to stay hidden for attachment follow-up, got %+v", filteredToolSet.ListToolNames())
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
