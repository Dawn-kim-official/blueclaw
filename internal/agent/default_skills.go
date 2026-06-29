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

Use injected memory context when older context may be relevant.
Use the capability CLI for public web information only when the missing information is required and public, current, or external.
Do not use public web lookup to replace private person memory, circle memory, user preferences, names, or addressing instructions.
If the user explicitly asks you to remember something, or states a durable preference, fact, or context update, finish with a clear acknowledgement; the runtime memory pipeline handles durable storage outside the compact kernel.
Treat examples such as names, preferences, working style, project context, and recurring constraints as non-exhaustive examples, not special cases.
Do not remember secrets, one-off requests, temporary details, or facts that are not useful beyond the current conversation.
The runtime decides whether durable memory belongs to person memory or active circle memory from the current conversation scope.
`),
		AllowedTools:           nil,
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
