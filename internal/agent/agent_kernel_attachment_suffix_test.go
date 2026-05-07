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

func TestSelectedRequiredEvidenceToolsComeFromSelectedSkills(t *testing.T) {
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

	toolNames := selectedRequiredEvidenceTools(instructionBundle)

	if len(toolNames) != 3 || toolNames[0] != "site.app.create" || toolNames[1] != "terminal.run" || toolNames[2] != "site.app.publish" {
		t.Fatalf("expected selected skill evidence tools, got %+v", toolNames)
	}
}

func TestAttachmentSuffixesComeFromStructuredOutputFormats(t *testing.T) {
	suffixes := attachmentSuffixesForRequestedOutputFormats([]string{"html", "pdf", "html"})

	if len(suffixes) != 2 || suffixes[0] != ".html" || suffixes[1] != ".pdf" {
		t.Fatalf("expected structured output format suffixes, got %+v", suffixes)
	}
}
