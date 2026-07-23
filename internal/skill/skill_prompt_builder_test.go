package skill

import (
	"strings"
	"testing"
)

func TestSkillPromptBuilderUsesOnlyInstructionBodies(t *testing.T) {
	prompt := (SkillPromptBuilder{}).BuildSkillPrompt([]SkillBundle{
		{
			Name:        "example",
			Instruction: "Use the concise skill body.",
		},
	})

	if prompt != "Use the concise skill body." {
		t.Fatalf("expected prompt to contain only instruction body, got %q", prompt)
	}
	if strings.Contains(prompt, "references/") {
		t.Fatalf("expected prompt to contain only the instruction body, got %q", prompt)
	}
}
