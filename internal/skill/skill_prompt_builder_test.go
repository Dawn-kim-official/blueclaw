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
			References:  []string{"references/large.md"},
			Scripts:     []string{"scripts/build.sh"},
			Assets:      []string{"assets/template.html"},
		},
	})

	if prompt != "Use the concise skill body." {
		t.Fatalf("expected prompt to contain only instruction body, got %q", prompt)
	}
	for _, forbiddenText := range []string{"references/large.md", "scripts/build.sh", "assets/template.html"} {
		if strings.Contains(prompt, forbiddenText) {
			t.Fatalf("expected prompt to exclude bundled resource metadata %q", forbiddenText)
		}
	}
}
