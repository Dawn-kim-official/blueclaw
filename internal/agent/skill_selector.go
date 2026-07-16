package agent

import (
	"path/filepath"
	"strings"
)

type SkillSelector struct{}

func (skillSelector SkillSelector) ShouldInclude(skillInstruction SkillInstruction, request AgentRequest) bool {
	decision := skillSelector.Evaluate(skillInstruction, request, "default")
	return decision.Status == "selected"
}

func (skillSelector SkillSelector) Evaluate(skillInstruction SkillInstruction, request AgentRequest, profileName string) SkillSelectionDecision {
	return skillAvailabilityDecision(skillInstruction, request, profileName)
}

func skillAvailabilityDecision(skillInstruction SkillInstruction, request AgentRequest, profileName string) SkillSelectionDecision {
	normalizedProfileName := firstNonEmptySkillSelectionString(profileName, "default")
	if !skillProfileAllows(skillInstruction, normalizedProfileName) {
		return skippedSkillDecision(skillInstruction, normalizedProfileName, "profile_not_allowed", nil)
	}
	missingTools := missingAllowedTools(skillInstruction, request)
	if len(missingTools) > 0 {
		return skippedSkillDecision(skillInstruction, normalizedProfileName, "missing_allowed_tools", missingTools)
	}
	return skippedSkillDecision(skillInstruction, normalizedProfileName, "no_trigger_matched", nil)
}

func skillProfileAllows(skillInstruction SkillInstruction, profileName string) bool {
	if len(skillInstruction.AllowedProfiles) == 0 {
		return true
	}
	for _, allowedProfile := range skillInstruction.AllowedProfiles {
		if normalizeSkillSelectionText(allowedProfile) == normalizeSkillSelectionText(profileName) {
			return true
		}
	}
	return false
}

func missingAllowedTools(skillInstruction SkillInstruction, request AgentRequest) []string {
	missingTools := []string{}
	for _, toolName := range SkillToolNames(skillInstruction) {
		if !requestHasToolName(request, toolName) {
			missingTools = append(missingTools, strings.TrimSpace(toolName))
		}
	}
	return missingTools
}

func SkillToolNames(skillInstruction SkillInstruction) []string {
	return appendUniqueStrings(skillInstruction.AllowedTools)
}

func skillPathsAllow(skillInstruction SkillInstruction, request AgentRequest) bool {
	if len(skillInstruction.Paths) == 0 {
		return true
	}
	for _, activePath := range request.ActivePaths {
		if skillPathMatchesAny(activePath, skillInstruction.Paths) {
			return true
		}
	}
	return false
}

func skillPathMatchesAny(path string, patterns []string) bool {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return false
	}
	for _, pattern := range patterns {
		isMatched, errorValue := filepath.Match(strings.TrimSpace(pattern), trimmedPath)
		if errorValue == nil && isMatched {
			return true
		}
	}
	return false
}

func requestHasToolName(request AgentRequest, toolName string) bool {
	if request.ToolSet == nil {
		return false
	}
	return requestToolSetCanReachTool(request.ToolSet, toolName)
}

func requestHasToolPrefix(request AgentRequest, toolPrefix string) bool {
	if request.ToolSet == nil {
		return false
	}
	trimmedToolPrefix := strings.TrimSpace(toolPrefix)
	if trimmedToolPrefix == "" {
		return false
	}
	for _, toolName := range request.ToolSet.ListRegisteredToolNames() {
		if strings.HasPrefix(toolName, trimmedToolPrefix) && requestToolSetCanReachTool(request.ToolSet, toolName) {
			return true
		}
	}
	return false
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

func skippedSkillDecision(skillInstruction SkillInstruction, profileName string, reason string, missingTools []string) SkillSelectionDecision {
	return SkillSelectionDecision{
		Name:         skillInstruction.Name,
		Status:       "skipped",
		Reason:       reason,
		ProfileName:  profileName,
		MissingTools: missingTools,
		Source:       skillInstruction.Source,
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
