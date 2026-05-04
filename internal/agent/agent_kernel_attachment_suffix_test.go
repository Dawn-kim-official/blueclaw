package agent

import "testing"

func TestSelectedRequiredAttachmentSuffixesHonorsHTMLOnlySlideRequest(t *testing.T) {
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

	if len(suffixes) != 1 || suffixes[0] != ".html" {
		t.Fatalf("expected html-only suffix contract, got %+v", suffixes)
	}
}

func TestSelectedRequiredAttachmentSuffixesKeepsDefaultSlideSet(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "simple-slides"}},
		SkillDecisions: []SkillSelectionDecision{{Name: "simple-slides", Status: "selected"}},
	}

	suffixes := selectedRequiredAttachmentSuffixes(instructionBundle, "너 뭐 할 수 있는지 8장 피피티 만들어줘")

	expectedSuffixes := []string{".pptx", ".pdf", ".html", "-notes.txt"}
	if len(suffixes) != len(expectedSuffixes) {
		t.Fatalf("expected default suffix contract, got %+v", suffixes)
	}
	for index, expectedSuffix := range expectedSuffixes {
		if suffixes[index] != expectedSuffix {
			t.Fatalf("expected suffix %d to be %q, got %+v", index, expectedSuffix, suffixes)
		}
	}
}
