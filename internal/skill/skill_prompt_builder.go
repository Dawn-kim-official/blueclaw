package skill

import "strings"

type SkillPromptBuilder struct{}

func (skillPromptBuilder SkillPromptBuilder) BuildSkillPrompt(skillBundles []SkillBundle) string {
	parts := []string{}
	for _, skillBundle := range skillBundles {
		parts = append(parts, skillBundle.Instruction)
	}
	return strings.Join(parts, "\n\n")
}
