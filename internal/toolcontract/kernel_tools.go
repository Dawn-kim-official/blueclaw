package toolcontract

import "strings"

const (
	TerminalRunToolName         = "terminal.run"
	AskInputToolName            = "ask.input"
	AskConfirmToolName          = "ask.confirm"
	FileDeliverToolName         = "file.deliver"
	AskChoiceToolName           = "ask.choice"
	SkillSearchToolName         = "skill.search"
	FileReadToolName            = "file.read"
	FileWriteToolName           = "file.write"
	FileDeleteToolName          = "file.delete"
	FileEditToolName            = "file.edit"
	FilePreviewToolName         = "file.preview"
	ImageReadToolName           = "image.read"
	ConversationHistoryToolName = "conversation.history"
	PlanUpdateToolName          = "plan.update"
	RequestToolsToolName        = "request_tools"
)

func KernelToolNames() []string {
	return []string{
		TerminalRunToolName,
		FileDeliverToolName,
		SkillSearchToolName,
		FileReadToolName,
		FileWriteToolName,
		FileDeleteToolName,
		FileEditToolName,
		FilePreviewToolName,
		ImageReadToolName,
		ConversationHistoryToolName,
		PlanUpdateToolName,
		RequestToolsToolName,
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

func ToolNamesMatch(leftToolName string, rightToolName string) bool {
	return strings.TrimSpace(leftToolName) == strings.TrimSpace(rightToolName)
}

func IsArtifactDeliveryTool(toolName string) bool {
	return strings.TrimSpace(toolName) == FileDeliverToolName
}
