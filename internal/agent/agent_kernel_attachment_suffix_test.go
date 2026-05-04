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
