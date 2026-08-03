package agentcontract

import (
	"strings"

	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

func NormalizeIntakeOptions(options IntakeOptions) IntakeOptions {
	if NormalizeTaskLevel(string(options.DefaultTaskLevel)) == "" {
		options.DefaultTaskLevel = TaskLevelLow
	}
	options.SkillTaskLevelFloor = NormalizeTaskLevel(string(options.SkillTaskLevelFloor))
	return options
}

func NormalizeReactionEmojiName(emojiName string) string {
	normalizedEmojiName := strings.Trim(strings.TrimSpace(emojiName), ":")
	if normalizedEmojiName == "" {
		return DefaultReactionEmojiName
	}
	normalizedEmojiName = strings.ToLower(normalizedEmojiName)
	for _, allowedEmojiName := range ReactionEmojiNames {
		if normalizedEmojiName == allowedEmojiName {
			return normalizedEmojiName
		}
	}
	return DefaultReactionEmojiName
}

func NormalizeRequestedOutputFormats(formats []string) []string {
	normalizedFormats := []string{}
	seenFormat := map[string]bool{}
	for _, format := range formats {
		normalizedFormat := strings.ToLower(strings.TrimSpace(format))
		switch normalizedFormat {
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv", "json":
		default:
			continue
		}
		if seenFormat[normalizedFormat] {
			continue
		}
		seenFormat[normalizedFormat] = true
		normalizedFormats = append(normalizedFormats, normalizedFormat)
	}
	return normalizedFormats
}

func RegisteredToolNamesOnly(toolRegistry *toolcontract.ToolSet, toolNames []string) []string {
	if toolRegistry == nil || len(toolNames) == 0 {
		return nil
	}
	registeredToolNames := []string{}
	for _, toolName := range appendUniqueStrings([]string{}, toolNames...) {
		trimmedToolName := strings.TrimSpace(toolName)
		if toolRegistry.IsAllowed(trimmedToolName) || requiredEvidenceToolCanBeSatisfied(toolRegistry, trimmedToolName) {
			registeredToolNames = appendUniqueStrings(registeredToolNames, trimmedToolName)
		}
	}
	return registeredToolNames
}

func HasAllTools(toolRegistry *toolcontract.ToolSet, toolNames []string) bool {
	if toolRegistry == nil {
		return false
	}
	availableToolNames := map[string]bool{}
	for _, toolName := range toolRegistry.ListToolNames() {
		availableToolNames[toolName] = true
	}
	for _, toolName := range toolNames {
		if !availableToolNames[toolName] {
			return false
		}
	}
	return true
}

func HasTool(toolRegistry *toolcontract.ToolSet, toolName string) bool {
	if toolRegistry == nil {
		return false
	}
	for _, availableToolName := range toolRegistry.ListToolNames() {
		if availableToolName == toolName {
			return true
		}
	}
	return false
}

func appendUniqueStrings(values []string, candidates ...string) []string {
	nextValues := append([]string{}, values...)
	seenValue := map[string]bool{}
	for _, value := range nextValues {
		seenValue[value] = true
	}
	for _, candidate := range candidates {
		trimmedCandidate := strings.TrimSpace(candidate)
		if trimmedCandidate == "" || seenValue[trimmedCandidate] {
			continue
		}
		seenValue[trimmedCandidate] = true
		nextValues = append(nextValues, trimmedCandidate)
	}
	return nextValues
}

func requiredEvidenceToolCanBeSatisfied(toolSet *toolcontract.ToolSet, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" || toolSet == nil || !toolSet.IsRegistered(trimmedToolName) {
		return false
	}
	if toolSet.IsAllowed(trimmedToolName) {
		return true
	}
	return !toolcontract.IsKernelToolName(trimmedToolName) && toolSet.CanExpose(trimmedToolName)
}
