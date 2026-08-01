package bluecollar

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

Use injected memory context when older context may be relevant.
Call memory_search before answering when the request may depend on earlier preferences, people, projects, or decisions that are not visible in the current conversation.
Use the selected public web tool only when the missing information is required and public, current, or external.
Do not use public web lookup to replace private person memory, circle memory, user preferences, names, or addressing instructions.
Nothing is stored automatically; memory_remember is the only path to durable storage.
When the user explicitly asks you to remember something, or states a durable preference, fact, or context update, call memory_remember with one compact standalone fact per call before finishing, then acknowledge what was stored.
Remember only what stays useful across future conversations, such as names, preferences, working style, project context, recurring constraints, and corrections to earlier memory; treat these as non-exhaustive examples, not special cases.
Do not remember secrets, one-off requests, temporary details, small talk, or facts that are not useful beyond the current conversation.
The runtime decides whether durable memory belongs to person memory or active circle memory from the current conversation scope.
`),
		ToolReferences: nil,
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
