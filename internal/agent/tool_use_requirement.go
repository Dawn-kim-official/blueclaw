package agent

import (
	"strings"
)

type toolUseRequirement struct {
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
	if toolSet == nil {
		return false
	}
	toolDefinition, isFound := toolSet.ToolDefinition(toolName)
	return isFound && ToolDefinitionRequiresSideEffectEvidence(toolDefinition)
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
	return requiredEvidenceIncludesNamespace(request.ToolSet, request.RequiredEvidenceTools, "browser")
}

func requestOnlyOpensBrowser(request AgentTurnRequest) bool {
	return taskLevelWantsSingleFinalReply(request.TaskLevel) && requestRequiresBrowserEvidence(request)
}
