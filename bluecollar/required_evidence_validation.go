package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

import "strings"

const (
	requiredEvidenceToolKindCapabilityOperation = "capability_operation"
	requiredEvidenceToolKindNativeTool          = "native_tool"
)

func workingSetEvidenceGroup(toolSet *toolcontract.ToolSet, candidateToolNames []string) []string {
	evidenceToolNames := []string{}
	for _, toolName := range appendUniqueStrings(candidateToolNames) {
		if !requiredEvidenceToolCanBeSatisfied(toolSet, toolName) {
			continue
		}
		evidenceToolNames = appendUniqueStrings(evidenceToolNames, toolName)
	}
	return evidenceToolNames
}

func requiredEvidenceToolCanBeSatisfied(toolSet *toolcontract.ToolSet, toolName string) bool {
	_, isValid := requiredEvidenceToolKind(toolSet, toolName)
	return isValid
}

func requiredEvidenceToolKind(toolSet *toolcontract.ToolSet, toolName string) (string, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return "", false
	}
	if toolSet == nil {
		return "", false
	}
	registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(toolSet, trimmedToolName)
	if !isRegistered {
		return "", false
	}
	if toolSet.IsAllowed(registeredToolName) {
		return requiredEvidenceToolKindNativeTool, true
	}
	if requiredEvidenceToolIsCapabilityOperation(toolSet, registeredToolName) {
		return requiredEvidenceToolKindCapabilityOperation, true
	}
	return "", false
}

func requiredEvidenceRegisteredToolName(toolSet *toolcontract.ToolSet, toolName string) (string, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	return trimmedToolName, toolSet.IsRegistered(trimmedToolName)
}

func requiredEvidenceToolIsCapabilityOperation(toolSet *toolcontract.ToolSet, toolName string) bool {
	return !toolcontract.IsKernelToolName(toolName) && toolSet.CanExpose(toolName)
}

func requiredEvidenceIncludesNamespace(toolSet *toolcontract.ToolSet, toolNames []string, namespace string) bool {
	for _, toolName := range toolNames {
		if toolIsInNamespace(toolSet, toolName, namespace) {
			return true
		}
	}
	return false
}

func requiredEvidenceIncludesSideEffect(toolSet *toolcontract.ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if toolcontract.IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName) {
			return true
		}
	}
	return false
}
