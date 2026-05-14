package agent

import "strings"

var defaultSkillNames = []string{
	"memory",
}

var builtInSkillInstructions = []SkillInstruction{
	{
		Name:        "memory",
		Description: "Use persistent memory across people and circles.",
		Prompt: strings.TrimSpace(`
Persistent memory is available by default.

Use memory.search when older context may be relevant but is not already present in the injected memory context.
If memory.search is unavailable, use web.search only when the missing information is required and can be answered from public, current, or external sources.
Do not use web.search to replace private person memory, circle memory, user preferences, names, or addressing instructions.
Use memory.remember only for durable facts, preferences, names, ongoing project context, or circle-shared knowledge that should help future conversations.
Call memory.search with a plain query string. Call memory.remember with a plain memory string.
Do not remember secrets, one-off requests, temporary details, or facts that are not useful beyond the current conversation.
The runtime decides whether memory.remember writes person memory or active circle memory from the current conversation scope.
`),
		AllowedTools: []string{
			"memory.search",
			"memory.remember",
		},
		DisableModelInvocation: true,
		Source: InstructionSource{
			Path:      "builtin:memory",
			SkillName: "memory",
		},
	},
}

func DefaultSkillNames() []string {
	return append([]string{}, defaultSkillNames...)
}

func BuiltInSkillInstructions() []SkillInstruction {
	return append([]SkillInstruction{}, builtInSkillInstructions...)
}

func DefaultSkillInstructions() []SkillInstruction {
	builtInSkillByName := skillInstructionByName(builtInSkillInstructions)
	defaultSkills := []SkillInstruction{}
	for _, skillName := range defaultSkillNames {
		if skillInstruction, isFound := builtInSkillByName[skillName]; isFound {
			defaultSkills = append(defaultSkills, skillInstruction)
		}
	}
	return defaultSkills
}

func DefaultSkillToolNames() []string {
	toolNames := []string{}
	for _, skillInstruction := range DefaultSkillInstructions() {
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	return toolNames
}

func DefaultAllowedToolNames(baseToolNames []string) []string {
	return appendUniqueStrings(baseToolNames, DefaultSkillToolNames()...)
}

func AppendSkillInstructions(left []SkillInstruction, right ...SkillInstruction) []SkillInstruction {
	return appendSkillInstructions(left, right...)
}
