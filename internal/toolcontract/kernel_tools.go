package toolcontract

import "strings"

const (
	TerminalRunToolName         = "terminal_run"
	AskInputToolName            = "ask_input"
	AskConfirmToolName          = "ask_confirm"
	FileDeliverToolName         = "file_deliver"
	AskChoiceToolName           = "ask_choice"
	SkillSearchToolName         = "skill_search"
	FileReadToolName            = "file_read"
	FileWriteToolName           = "file_write"
	FileDeleteToolName          = "file_delete"
	FileEditToolName            = "file_edit"
	FilePreviewToolName         = "file_preview"
	ImageReadToolName           = "image_read"
	ConversationHistoryToolName = "conversation_history"
	PlanUpdateToolName          = "plan_update"
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
