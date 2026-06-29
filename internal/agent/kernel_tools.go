package agent

import "strings"

const (
	TerminalRunToolName      = "terminal.run"
	AskInputToolName         = "ask.input"
	AskConfirmToolName       = "ask.confirm"
	FileDeliverToolName      = "file.deliver"
	AskChoiceToolName        = "ask.choice"
	ArtifactDeliverToolName  = "artifact.deliver"
	FileAttachToolName       = "file.attach"
	SkillSearchToolName      = "skill.search"
	CapabilityInvokeToolName = "capability.invoke"
	FileReadToolName         = "file.read"
	FileWriteToolName        = "file.write"
	FileDeleteToolName       = "file.delete"
	FileEditToolName         = "file.edit"
	FilePatchToolName        = "file.patch"
	FilePreviewToolName      = "file.preview"
	ImageReadToolName        = "image.read"
)

func KernelToolNames() []string {
	return []string{
		TerminalRunToolName,
		AskInputToolName,
		AskConfirmToolName,
		FileDeliverToolName,
		SkillSearchToolName,
		CapabilityInvokeToolName,
		FileReadToolName,
		FileWriteToolName,
		FileDeleteToolName,
		FileEditToolName,
		FilePatchToolName,
		FilePreviewToolName,
		ImageReadToolName,
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
	case AskChoiceToolName:
		return AskInputToolName
	case ArtifactDeliverToolName, FileAttachToolName:
		return FileDeliverToolName
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
	return CanonicalEvidenceToolName(toolName) == FileDeliverToolName
}
