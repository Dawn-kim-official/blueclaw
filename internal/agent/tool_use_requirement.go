package agent

import (
	"strings"
)

type toolUseRequirement struct {
	ToolPrefix         string
	ToolName           string
	Reason             string
	RequiresAttachment bool
	AttachmentSuffixes []string
}

func deriveToolUseRequirements(request AgentTurnRequest) []toolUseRequirement {
	requirements := evidenceToolRequirements(request)
	if directMessageEvidenceRequired(request) {
		return requirements
	}
	if requestRequiresBrowserEvidence(request) {
		requirements = append(requirements, toolUseRequirement{
			ToolPrefix: "browser.",
			Reason:     "the request asks for browser state or browser control",
		})
	}
	return requirements
}

func directMessageEvidenceRequired(request AgentTurnRequest) bool {
	return requiredEvidenceContains(request.RequiredEvidenceTools, "platform.message.send")
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
			ToolName:           trimmedToolName,
			Reason:             "selected skill requires completion evidence",
			RequiresAttachment: strings.HasSuffix(trimmedToolName, ".attach"),
			AttachmentSuffixes: attachmentSuffixesForEvidenceTool(trimmedToolName, request.RequiredAttachmentSuffixes),
		})
	}
	return requirements
}

func attachmentSuffixesForEvidenceTool(toolName string, suffixes []string) []string {
	if !strings.HasSuffix(toolName, ".attach") {
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
	if !hasToolPrefix(request.ToolSet, "browser.") {
		return false
	}
	return workKindsContain(request.WorkKinds, WorkKindBrowserSession)
}

func requestOnlyOpensBrowser(request AgentTurnRequest) bool {
	return request.TaskComplexity == TaskComplexitySimple && workKindsContain(request.WorkKinds, WorkKindBrowserSession)
}
