package agent

import "strings"

type SkillSelector struct{}

func (skillSelector SkillSelector) IsAvailable(skillInstruction SkillInstruction, request AgentRequest) bool {
	return !allToolReferencesMissing(skillInstruction, request)
}

func (skillSelector SkillSelector) ShouldInclude(skillInstruction SkillInstruction, request AgentRequest) bool {
	decision := skillSelector.Evaluate(skillInstruction, request, "default")
	return decision.Status == "selected"
}

func (skillSelector SkillSelector) Evaluate(skillInstruction SkillInstruction, request AgentRequest, profileName string) SkillSelectionDecision {
	return skillAvailabilityDecision(skillInstruction, request, profileName)
}

func skillAvailabilityDecision(skillInstruction SkillInstruction, request AgentRequest, profileName string) SkillSelectionDecision {
	normalizedProfileName := firstNonEmptySkillSelectionString(profileName, "default")
	if allToolReferencesMissing(skillInstruction, request) {
		return skippedSkillDecision(skillInstruction, normalizedProfileName, "missing_tool_references", missingToolReferences(skillInstruction, request))
	}
	return skippedSkillDecision(skillInstruction, normalizedProfileName, "no_trigger_matched", nil)
}

func allToolReferencesMissing(skillInstruction SkillInstruction, request AgentRequest) bool {
	referenceNames := SkillToolNames(skillInstruction)
	if len(referenceNames) == 0 {
		return false
	}
	consideredNames := []string{}
	for _, referenceName := range referenceNames {
		if !IsKernelToolName(referenceName) {
			consideredNames = append(consideredNames, referenceName)
		}
	}
	if len(consideredNames) == 0 {
		consideredNames = referenceNames
	}
	for _, referenceName := range consideredNames {
		if requestHasToolName(request, referenceName) {
			return false
		}
	}
	return true
}

func missingToolReferences(skillInstruction SkillInstruction, request AgentRequest) []string {
	missingToolReferences := []string{}
	for _, toolName := range SkillToolNames(skillInstruction) {
		if !requestHasToolName(request, toolName) {
			missingToolReferences = append(missingToolReferences, strings.TrimSpace(toolName))
		}
	}
	return missingToolReferences
}

func SkillToolNames(skillInstruction SkillInstruction) []string {
	return appendUniqueStrings(skillInstruction.ToolReferences)
}

func requestHasToolName(request AgentRequest, toolName string) bool {
	if request.ToolSet == nil {
		return false
	}
	return requestToolSetCanReachTool(request.ToolSet, toolName)
}

func requestToolSetCanReachTool(toolSet *ToolSet, toolName string) bool {
	if toolSet == nil {
		return false
	}
	return toolSet.IsAllowed(toolName) || toolSet.CanExpose(toolName)
}

func normalizeSkillSelectionText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func selectedSkillDecision(skillInstruction SkillInstruction, profileName string, reason string) SkillSelectionDecision {
	return SkillSelectionDecision{
		Name:        skillInstruction.Name,
		Status:      "selected",
		Reason:      reason,
		ProfileName: profileName,
		Source:      skillInstruction.Source,
	}
}

func skippedSkillDecision(skillInstruction SkillInstruction, profileName string, reason string, missingToolReferences []string) SkillSelectionDecision {
	return SkillSelectionDecision{
		Name:                  skillInstruction.Name,
		Status:                "skipped",
		Reason:                reason,
		ProfileName:           profileName,
		MissingToolReferences: missingToolReferences,
		Source:                skillInstruction.Source,
	}
}

func firstNonEmptySkillSelectionString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
