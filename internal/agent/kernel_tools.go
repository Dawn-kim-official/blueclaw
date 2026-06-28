package agent

import "strings"

const (
	TerminalRunToolName     = "terminal.run"
	AskInputToolName        = "ask.input"
	AskChoiceToolName       = "ask.choice"
	AskConfirmToolName      = "ask.confirm"
	ArtifactDeliverToolName = "artifact.deliver"
	SkillSearchToolName     = "skill.search"
)

func KernelToolNames() []string {
	return []string{
		TerminalRunToolName,
		AskInputToolName,
		AskChoiceToolName,
		AskConfirmToolName,
		ArtifactDeliverToolName,
		SkillSearchToolName,
	}
}

func IsKernelToolName(toolName string) bool {
	for _, kernelToolName := range KernelToolNames() {
		if strings.TrimSpace(toolName) == kernelToolName {
			return true
		}
	}
	return false
}

func CanonicalEvidenceToolName(toolName string) string {
	trimmedToolName := strings.TrimSpace(toolName)
	switch trimmedToolName {
	case "file.attach":
		return ArtifactDeliverToolName
	case "terminal.session":
		return TerminalRunToolName
	default:
		return trimmedToolName
	}
}

func ToolNamesMatch(leftToolName string, rightToolName string) bool {
	return CanonicalEvidenceToolName(leftToolName) == CanonicalEvidenceToolName(rightToolName)
}

func IsArtifactDeliveryTool(toolName string) bool {
	return CanonicalEvidenceToolName(toolName) == ArtifactDeliverToolName
}
