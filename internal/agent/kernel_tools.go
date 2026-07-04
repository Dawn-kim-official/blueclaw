package agent

import (
	"encoding/json"
	"strings"
)

const (
	TerminalRunToolName         = "terminal.run"
	AskInputToolName            = "ask.input"
	AskConfirmToolName          = "ask.confirm"
	FileDeliverToolName         = "file.deliver"
	AskChoiceToolName           = "ask.choice"
	ArtifactDeliverToolName     = "artifact.deliver"
	FileAttachToolName          = "file.attach"
	SkillSearchToolName         = "skill.search"
	CapabilityInvokeToolName    = "capability.invoke"
	FileReadToolName            = "file.read"
	FileWriteToolName           = "file.write"
	FileDeleteToolName          = "file.delete"
	FileEditToolName            = "file.edit"
	FilePatchToolName           = "file.patch"
	FilePreviewToolName         = "file.preview"
	ImageReadToolName           = "image.read"
	ConversationHistoryToolName = "conversation.history"
	TaskHistoryToolName         = "task.history"
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
		ConversationHistoryToolName,
		TaskHistoryToolName,
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

// effectiveActionToolNameAndInput unwraps a capability.invoke call to the
// underlying operation name and its nested input, so operation-specific
// validation and dedup logic never has to special-case the neutral kernel
// verb. Non-capability calls pass through unchanged.
func effectiveActionToolNameAndInput(toolName string, toolInput json.RawMessage) (string, json.RawMessage) {
	if strings.TrimSpace(toolName) != CapabilityInvokeToolName {
		return toolName, toolInput
	}
	var document struct {
		Operation string          `json:"operation"`
		Input     json.RawMessage `json:"input"`
	}
	if json.Unmarshal(toolInput, &document) != nil {
		return toolName, toolInput
	}
	operation := strings.TrimSpace(document.Operation)
	if operation == "" {
		return toolName, toolInput
	}
	return operation, unwrapStringifiedOperationInput(document.Input)
}

// unwrapStringifiedOperationInput accepts the capability.invoke input contract
// where the operation parameters arrive as one JSON object encoded in a string,
// mirroring the runtime handler's normalization so validation and dedup logic
// see the same object either way.
func unwrapStringifiedOperationInput(input json.RawMessage) json.RawMessage {
	var stringifiedInput string
	if json.Unmarshal(input, &stringifiedInput) != nil {
		return input
	}
	innerInput := json.RawMessage(strings.TrimSpace(stringifiedInput))
	var probe map[string]json.RawMessage
	if json.Unmarshal(innerInput, &probe) != nil {
		return input
	}
	return innerInput
}
