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

	contract := outcomeContractForRequest(AgentRequest{Prompt: "https://example.com 참고해서 사업계획서 작성해줘"}, intakeDecision, instructionBundle, ExecutionPlan{}, false, nil)

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

	contract := outcomeContractForRequest(AgentRequest{Prompt: "샘플에게 테스트라고 DM 보내줘"}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "platform.dm.send" {
		t.Fatalf("expected send hard gate for external send goal, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestAttachmentSuffixesComeFromStructuredOutputFormats(t *testing.T) {
	suffixes := attachmentSuffixesForRequestedOutputFormats([]string{"html", "pdf", "html"})

	if len(suffixes) != 2 || suffixes[0] != ".html" || suffixes[1] != ".pdf" {
		t.Fatalf("expected structured output format suffixes, got %+v", suffixes)
	}
}
