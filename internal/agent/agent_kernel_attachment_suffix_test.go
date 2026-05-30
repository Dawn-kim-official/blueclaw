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
					RequiredEvidenceTools: []string{"site.app.create", "terminal.run", "site.app.publish"},
				},
			},
			{
				Name: "calendar",
				Completion: SkillCompletion{
					RequiredEvidenceTools: []string{"calendar.event.add"},
				},
			},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}

	toolNames := selectedEvidenceHintTools(instructionBundle)

	if len(toolNames) != 3 || toolNames[0] != "site.app.create" || toolNames[1] != "terminal.run" || toolNames[2] != "site.app.publish" {
		t.Fatalf("expected selected skill evidence tools, got %+v", toolNames)
	}
}

func TestOutcomeContractCreatesExpectedResultsForSitePublish(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "개인 홈페이지 만들어서 배포해줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
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
				RequiredEvidenceTools: []string{"platform.dm.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(AgentRequest{Prompt: "https://dawn.kim 참고해서 사업계획서 작성해줘"}, intakeDecision, instructionBundle, ExecutionPlan{}, false, nil)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no DM hard gate for non-send goal, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.SelectedEvidenceHints) != 1 || contract.SelectedEvidenceHints[0] != "platform.dm.send" {
		t.Fatalf("expected selected evidence hint to be retained, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestOutcomeContractRequiresSendEvidenceForExternalSendPlan(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"platform.dm.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}

	contract := outcomeContractForRequest(AgentRequest{Prompt: "동하에게 테스트라고 DM 보내줘"}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "platform.dm.send" {
		t.Fatalf("expected send hard gate for external send goal, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeReferenceToolSetHidesSendAndSiteToolsForDocumentGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "file.write", "file.attach", "site.app.create", "site.app.publish", "platform.dm.send", "mail.message.send"})
	contract := OutcomeContract{
		SelectedEvidenceHints: []string{"site.app.create", "site.app.publish", "platform.dm.send", "mail.message.send"},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "https://dawn.kim 참고해서 사업계획서 작성해줘"}, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"web.fetch", "file.write", "file.attach"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"site.app.create", "site.app.publish", "platform.dm.send", "mail.message.send"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to be hidden for document goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectedSkillToolSetKeepsGenericWebTools(t *testing.T) {
	toolSet := testToolSet([]string{"web.search", "web.fetch", "terminal.run", "file.write"})
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "simple-slides", AllowedTools: []string{"terminal.run", "file.write"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "simple-slides", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, AgentRequest{Prompt: "https://dawn.kim 참고해서 ppt 만들어줘"}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"web.search", "web.fetch", "terminal.run", "file.write"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available with selected skills, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsSiteToolsForSiteGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "site.app.create", "site.app.publish"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "웹사이트 하나 만들어서 배포해줘"}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for site goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsActiveGoalEvidenceToolsForContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "site.app.create", "site.app.publish"})
	request := AgentRequest{
		Prompt: "다시 해봐 그럼 될 거야",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.app.create", "terminal.run", "site.app.publish"},
		}},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, request, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for active site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectedSkillToolSetKeepsActiveGoalEvidenceToolsForContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "site.app.create", "site.app.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"terminal.run", "site.app.create", "site.app.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해봐 그럼 될 거야",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.app.create", "terminal.run", "site.app.publish"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"site.app.create", "terminal.run", "site.app.publish"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"terminal.run", "site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for selected site continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectedSkillToolSetKeepsMatchingSelectedSkillToolsWhenActiveGoalWasAttachmentFallback(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "terminal.run", "file.attach", "site.app.create", "site.app.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"terminal.run", "site.app.create", "site.app.publish"},
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.app.create", "terminal.run", "site.app.publish"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해봐",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools:      []string{"file.attach"},
			RequiredAttachmentSuffixes: []string{".html"},
			SelectedEvidenceHints:      []string{"site.app.create", "terminal.run", "site.app.publish"},
			ArtifactRequirement:        ArtifactRequirementRequired,
		}},
	}
	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeImmediateReply}, instructionBundle, ExecutionPlan{}, false, nil)

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"terminal.run", "site.app.create", "site.app.publish"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to be exposed after selected site skill, got %+v", toolName, filteredToolSet.ListToolNames())
		}
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected selected site skill to require %s evidence, got %+v", toolName, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractRequiresActiveGoalEvidenceForContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "site-prototype",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"site.app.create", "terminal.run", "site.app.publish"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해봐 그럼 될 거야",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"site.app.create", "terminal.run", "site.app.publish"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"site.app.create", "site.app.publish"} {
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected active site continuation to require %s evidence, got %+v", toolName, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractRequiresSendEvidenceForActiveSendContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name: "direct-message",
			Completion: SkillCompletion{
				RequiredEvidenceTools: []string{"platform.dm.send"},
			},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해줘",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"platform.dm.send"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if !stringSliceContains(contract.RequiredEvidenceTools, "platform.dm.send") {
		t.Fatalf("expected active send continuation to require send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestSelectedSkillToolSetKeepsActiveSendToolForActiveSendContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"ask.confirm", "platform.dm.send", "file.write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "direct-message",
			AllowedTools: []string{"ask.confirm", "platform.dm.send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt: "다시 해줘",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"platform.dm.send"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"platform.dm.send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("platform.dm.send") {
		t.Fatalf("expected active send tool to remain available for continuation, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestOutcomeReferenceToolSetKeepsSendToolsForExplicitSendGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web.fetch", "platform.dm.send", "mail.message.send"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "동하에게 DM 보내줘"}, ExecutionPlan{}, false, OutcomeContract{})

	if !filteredToolSet.IsAllowed("platform.dm.send") {
		t.Fatalf("expected DM send to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestConfirmationHintsIgnoreUnrelatedSelectedSkillEvidence(t *testing.T) {
	hints := confirmationEvidenceHintsForRequest(
		AgentRequest{Prompt: "https://dawn.kim 참고해서 사업계획서 작성해줘"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask},
		[]string{"site.app.publish", "platform.dm.send"},
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
