package agent

import "testing"

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
