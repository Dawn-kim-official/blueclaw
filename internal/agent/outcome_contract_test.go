package agent

import (
	"strings"
	"testing"
)

func TestSelectedRequiredAttachmentSuffixesStayAdvisoryForSlides(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "simple-slides",
			Completion: SkillCompletion{
				RequiredAttachmentSuffixes: []string{".pptx", ".pdf", ".html", "-notes.txt"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "simple-slides", Status: "selected"}},
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

func TestOutcomeContractRequiresCurrentEffectsForSiteModification(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:    "예쁜 귤 웹사이트 퀄리티가 너무 낮아. 더 예쁘게 해줘.",
			WorkKinds: []string{WorkKindSitePrototype},
			ToolSet:   newTestToolSet([]string{"site.status", "file.edit", "site.build", "site.publish"}),
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	for _, expectedEffect := range []OutcomeEffect{
		{ObjectType: "workspace", Effect: "modified"},
		{ObjectType: "website", Effect: "built"},
		{ObjectType: "website", Effect: "published"},
	} {
		if !outcomeEffectsContain(contract.RequiredEffects, expectedEffect.ObjectType, expectedEffect.Effect) {
			t.Fatalf("expected required effect %+v, got %+v", expectedEffect, contract.RequiredEffects)
		}
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
	if stringSliceContains(contract.RequiredEvidenceTools, "file.attach") {
		t.Fatalf("expected no file.attach requirement for site publish, got %+v", contract.RequiredEvidenceTools)
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
				RequiredEvidenceTools: []string{"site.status", "site.build", "site.publish"},
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
	if !stringSliceContains(contract.RequiredEvidenceTools, "file.attach") {
		t.Fatalf("expected file.attach requirement for requested file, got %+v", contract.RequiredEvidenceTools)
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

func TestOutcomeContractKeepsExplicitWebsiteHTMLFileRequest(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지를 만들어서 배포하고 HTML 파일도 첨부해줘", WorkKinds: []string{WorkKindSitePrototype, WorkKindFileDelivery}},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{"html"}, SiteRequestEvidence: "개인 홈페이지를 만들어서 배포"},
		InstructionBundle{},
		ExecutionPlan{PublicDeploy: true},
		true,
		[]string{".html"},
	)

	if len(contract.RequiredAttachmentSuffixes) != 1 || contract.RequiredAttachmentSuffixes[0] != ".html" {
		t.Fatalf("expected explicit html file request to keep suffix, got %+v", contract.RequiredAttachmentSuffixes)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file.attach") {
		t.Fatalf("expected explicit html file request to require file.attach, got %+v", contract.RequiredEvidenceTools)
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
					RequiredEvidenceTools: []string{"site.status", "site.build", "site.publish"},
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

	contract := outcomeContractForRequest(AgentRequest{Prompt: "https://dawn.kim 참고해서 사업계획서 작성해줘"}, intakeDecision, instructionBundle, ExecutionPlan{}, false, nil)

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
					RequiredEvidenceTools: []string{"site.status", "site.build", "artifact.review", "site.publish"},
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
			SelectedEvidenceHints: []string{"message.delete", "message.send", "site.status", "site.build", "artifact.review", "site.publish"},
			Source:                "execution_plan",
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"site.status", "site.build", "artifact.review", "site.publish"} {
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
	if !stringSliceContains(contract.RequiredEvidenceTools, "file.attach") {
		t.Fatalf("expected artifact attachment request to require file.attach, got %+v", contract.RequiredEvidenceTools)
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

	contract := outcomeContractForRequest(AgentRequest{Prompt: "동하에게 테스트라고 DM 보내줘"}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "message.send" {
		t.Fatalf("expected send hard gate for external send goal, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractRequiresSendEvidenceForExternalSendWorkKind(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:    "동하에게 테스트라고 DM 보내줘",
			ToolSet:   testToolSet([]string{"message.send"}),
			WorkKinds: []string{WorkKindExternalSend},
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "message.send" {
		t.Fatalf("expected external send work kind to require message.send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractRequiresFlowTaskAddEvidenceForFlowTaskWorkKind(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:    "업무 등록해줘\n- 메일 페이지 앱 비밀번호 개선하기",
			ToolSet:   testToolSet([]string{"task.add", "task.list", "task.update"}),
			WorkKinds: []string{WorkKindFlowTask},
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "task.add" {
		t.Fatalf("expected flow task add hard gate, got %+v", contract.RequiredEvidenceTools)
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
			Prompt:    "이번 주 업무 목록 보여줘",
			ToolSet:   testToolSet([]string{"task.add", "task.list", "task.update"}),
			WorkKinds: []string{WorkKindFlowTask},
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
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
	toolSet := testToolSet([]string{"web.fetch", "file.write", "file.attach", "site.create", "site.publish", "message.send", "mail.message.send"})
	contract := OutcomeContract{
		SelectedEvidenceHints: []string{"site.create", "site.publish", "message.send", "mail.message.send"},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "https://dawn.kim 참고해서 사업계획서 작성해줘"}, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"web.fetch", "file.write", "file.attach"} {
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

func TestAgentTurnToolSetKeepsPinnedWebAndSkillTools(t *testing.T) {
	toolSet := testToolSet([]string{"web.search", "web.fetch", "terminal.run", "file.write"})
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "simple-slides", AllowedTools: []string{"terminal.run", "file.write"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "simple-slides", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, AgentRequest{
		Prompt:          "https://dawn.kim 참고해서 ppt 만들어줘",
		PinnedToolNames: []string{"web.search", "web.fetch", "terminal.run", "file.write"},
	}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"web.search", "web.fetch", "terminal.run", "file.write"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available with selected skills, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsSiteToolsForSiteGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "site.create", "site.publish"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "웹사이트 하나 만들어서 배포해줘", WorkKinds: []string{WorkKindSitePrototype}}, ExecutionPlan{}, false, OutcomeContract{SiteEvidenceQuote: "웹사이트 하나 만들어서 배포해줘"})

	for _, toolName := range []string{"site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for site goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsActiveGoalEvidenceToolsForContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "site.create", "site.publish"})
	request := AgentRequest{
		Prompt:    "다시 해봐 그럼 될 거야",
		WorkKinds: []string{WorkKindSitePrototype},
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

func TestAgentTurnToolSetKeepsPinnedActiveGoalEvidenceToolsForContinuation(t *testing.T) {
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
		WorkKinds:       []string{WorkKindSitePrototype},
		PinnedToolNames: []string{"terminal.run", "site.create", "site.publish"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "웹사이트 하나 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"},
			SiteEvidenceQuote:     "웹사이트 하나 만들어서 배포해줘",
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"site.create", "terminal.run", "site.publish"}, SiteEvidenceQuote: "웹사이트 하나 만들어서 배포해줘"}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"terminal.run", "site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for selected site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetKeepsPinnedSelectedSkillToolsWhenActiveGoalWasAttachmentFallback(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "file.attach", "site.create", "site.publish"})
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
		WorkKinds:       []string{WorkKindSitePrototype},
		PinnedToolNames: []string{"terminal.run", "site.create", "site.publish"},
		ActiveGoal: ActiveGoal{OriginalInstruction: "개인 홈페이지를 만들어서 배포해줘", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{"file.attach"},
			RequiredAttachmentSuffixes: []string{".html"},
			SelectedEvidenceHints:      []string{"site.create", "terminal.run", "site.publish"},
			SiteEvidenceQuote:          "개인 홈페이지를 만들어서 배포해줘",
			ArtifactRequirement:        ArtifactRequirementRequired,
		}},
	}
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeImmediateReply}, instructionBundle, ExecutionPlan{}, false, nil)

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"terminal.run", "site.create", "site.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to be exposed after selected site skill, got %+v", toolName, filteredToolSet.ListToolNames())
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
		Prompt:    "버튼 색 바꿔줘",
		WorkKinds: []string{WorkKindSitePrototype},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "19일 오후 6시 어반브랜딩 미팅 추가 위치는 코엑스",
			WorkKinds:           []string{WorkKindSitePrototype},
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

	normalizedActiveGoal, report := normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt)
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if !report.HasDrops() {
		t.Fatalf("expected stale site normalization report, got %+v", report)
	}
	if workKindsContain(normalizedActiveGoal.WorkKinds, WorkKindSitePrototype) {
		t.Fatalf("expected stale active goal work kind to be stripped, got %+v", normalizedActiveGoal.WorkKinds)
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
		Prompt:    "버튼 색 바꿔줘",
		WorkKinds: []string{WorkKindSitePrototype},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "포트폴리오 웹사이트 만들어서 배포해줘",
			WorkKinds:           []string{WorkKindSitePrototype},
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

	normalizedActiveGoal, report := normalizeActiveGoalSiteRequirement(request.ActiveGoal, request.Prompt)
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if report.HasDrops() {
		t.Fatalf("expected genuine site continuation to keep requirements, got %+v", report)
	}
	if !workKindsContain(normalizedActiveGoal.WorkKinds, WorkKindSitePrototype) {
		t.Fatalf("expected genuine active goal work kind to remain, got %+v", normalizedActiveGoal.WorkKinds)
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
	request.ActiveGoal.OriginalInstruction = "동하에게 테스트라고 DM 보내줘"

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if !stringSliceContains(contract.RequiredEvidenceTools, "message.send") {
		t.Fatalf("expected active send continuation to require send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestAgentTurnToolSetKeepsPinnedActiveSendToolForActiveSendContinuation(t *testing.T) {
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
			OriginalInstruction: "동하에게 테스트라고 DM 보내줘",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"message.send"},
				SelectedEvidenceHints: []string{"message.send"},
			},
		},
	}
	contract := OutcomeContract{RequiredEvidenceTools: []string{"message.send"}, SelectedEvidenceHints: []string{"message.send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected active send tool to remain available for continuation, got %+v", filteredToolSet.ListToolNames())
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

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "동하에게 DM 보내줘"}, ExecutionPlan{}, false, OutcomeContract{RequiredEvidenceTools: []string{"message.send"}})

	if !filteredToolSet.IsAllowed("message.send") {
		t.Fatalf("expected DM send to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestConfirmationHintsIgnoreUnrelatedSelectedSkillEvidence(t *testing.T) {
	hints := confirmationEvidenceHintsForRequest(
		AgentRequest{Prompt: "https://dawn.kim 참고해서 사업계획서 작성해줘"},
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
