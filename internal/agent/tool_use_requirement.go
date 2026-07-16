package agent

import (
	"strings"
)

type toolUseRequirement struct {
	ToolPrefix                 string
	ToolName                   string
	Reason                     string
	RequiresAttachment         bool
	RequiresSideEffectEvidence bool
	AttachmentSuffixes         []string
}

func deriveToolUseRequirements(request AgentTurnRequest) []toolUseRequirement {
	return evidenceToolRequirements(request)
}

func evidenceToolRequirements(request AgentTurnRequest) []toolUseRequirement {
	requirements := []toolUseRequirement{}
	seenToolName := map[string]bool{}
	for _, toolName := range request.RequiredEvidenceTools {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" || seenToolName[trimmedToolName] {
			continue
		}
		seenToolName[trimmedToolName] = true
		requirements = append(requirements, toolUseRequirement{
			ToolName:                   trimmedToolName,
			Reason:                     "selected workflow requires completion evidence",
			RequiresAttachment:         IsArtifactDeliveryTool(trimmedToolName),
			RequiresSideEffectEvidence: requiredEvidenceToolNeedsSuccessfulSideEffect(request.ToolSet, trimmedToolName),
			AttachmentSuffixes:         attachmentSuffixesForEvidenceTool(trimmedToolName, request.RequiredAttachmentSuffixes),
		})
	}
	return requirements
}

func requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet *ToolSet, toolName string) bool {
	if toolSet != nil {
		if toolDefinition, isFound := toolSet.ToolDefinition(toolName); isFound {
			return ToolDefinitionRequiresSideEffectEvidence(toolDefinition)
		}
	}
	return ToolDefinitionRequiresSideEffectEvidence(ToolDefinition{Name: toolName})
}

func attachmentSuffixesForEvidenceTool(toolName string, suffixes []string) []string {
	if !IsArtifactDeliveryTool(toolName) {
		return nil
	}
	trimmedSuffixes := []string{}
	seenSuffix := map[string]bool{}
	for _, suffix := range suffixes {
		trimmedSuffix := strings.TrimSpace(suffix)
		if trimmedSuffix == "" || seenSuffix[trimmedSuffix] {
			continue
		}
		seenSuffix[trimmedSuffix] = true
		trimmedSuffixes = append(trimmedSuffixes, trimmedSuffix)
	}
	return trimmedSuffixes
}

func requestRequiresBrowserEvidence(request AgentTurnRequest) bool {
	return requiredEvidenceHasPrefix(request.RequiredEvidenceTools, "browser.")
}

func requestOnlyOpensBrowser(request AgentTurnRequest) bool {
	return taskLevelWantsSingleFinalReply(request.TaskLevel) && requestRequiresBrowserEvidence(request)
}
