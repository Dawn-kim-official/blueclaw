package agent

import (
	"encoding/json"
	"strings"
)

type manualCapabilityRequirement struct {
	ToolNames  []string `json:"toolNames"`
	SkillNames []string `json:"skillNames"`
	Reason     string   `json:"reason,omitempty"`
}

type manualCapabilityResult struct {
	PinnedToolNames           []string            `json:"pinnedToolNames,omitempty"`
	PinnedSkillNames          []string            `json:"pinnedSkillNames,omitempty"`
	UnknownToolNames          []string            `json:"unknownToolNames,omitempty"`
	UnavailableToolNames      []string            `json:"unavailableToolNames,omitempty"`
	UnknownSkillNames         []string            `json:"unknownSkillNames,omitempty"`
	SkillsMissingAllowedTools map[string][]string `json:"skillsMissingAllowedTools,omitempty"`
	EmptyRequirement          bool                `json:"emptyRequirement,omitempty"`
}

func applyManualCapabilityRequirement(request AgentTurnRequest, requirement manualCapabilityRequirement) (AgentTurnRequest, manualCapabilityResult) {
	result := manualCapabilityResult{SkillsMissingAllowedTools: map[string][]string{}}
	if len(appendUniqueStrings(requirement.ToolNames)) == 0 && len(appendUniqueStrings(requirement.SkillNames)) == 0 {
		result.EmptyRequirement = true
	}
	request, result = pinRequestedTools(request, requirement.ToolNames, result)
	request, result = pinRequestedSkills(request, requirement.SkillNames, result)
	if len(result.SkillsMissingAllowedTools) == 0 {
		result.SkillsMissingAllowedTools = nil
	}
	return request, result
}

func pinRequestedTools(request AgentTurnRequest, toolNames []string, result manualCapabilityResult) (AgentTurnRequest, manualCapabilityResult) {
	for _, toolName := range appendUniqueStrings(toolNames) {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		if request.ToolSet == nil || !request.ToolSet.IsRegistered(trimmedToolName) {
			result.UnknownToolNames = appendUniqueStrings(result.UnknownToolNames, trimmedToolName)
			continue
		}
		if !request.ToolSet.CanExpose(trimmedToolName) {
			result.UnavailableToolNames = appendUniqueStrings(result.UnavailableToolNames, trimmedToolName)
			continue
		}
		result.PinnedToolNames = appendUniqueStrings(result.PinnedToolNames, trimmedToolName)
		request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, trimmedToolName)
	}
	request.ToolSet = request.ToolSet.WithAdditionalAllowedToolNames(request.PinnedToolNames)
	return request, result
}

func pinRequestedSkills(request AgentTurnRequest, skillNames []string, result manualCapabilityResult) (AgentTurnRequest, manualCapabilityResult) {
	for _, skillName := range appendUniqueStrings(skillNames) {
		trimmedSkillName := strings.TrimSpace(skillName)
		if trimmedSkillName == "" {
			continue
		}
		skillInstruction, isFound := findAvailableSkillInstruction(request.AvailableSkills, trimmedSkillName)
		if !isFound {
			result.UnknownSkillNames = appendUniqueStrings(result.UnknownSkillNames, trimmedSkillName)
			continue
		}
		missingToolNames := unavailableSkillToolNames(request.ToolSet, skillInstruction)
		if len(missingToolNames) > 0 {
			result.SkillsMissingAllowedTools[trimmedSkillName] = missingToolNames
			continue
		}
		result.PinnedSkillNames = appendUniqueStrings(result.PinnedSkillNames, trimmedSkillName)
		request.PinnedSkillNames = appendUniqueStrings(request.PinnedSkillNames, trimmedSkillName)
		request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, SkillToolNames(skillInstruction)...)
		request.ToolSet = request.ToolSet.WithAdditionalAllowedToolNames(SkillToolNames(skillInstruction))
		request.InstructionPrompt = appendPinnedSkillPrompt(request.InstructionPrompt, []SkillInstruction{skillInstruction})
		request.ActiveGoal.OutcomeContract.SelectedEvidenceHints = appendUniqueStrings(request.ActiveGoal.OutcomeContract.SelectedEvidenceHints, skillInstruction.Completion.RequiredEvidenceTools...)
	}
	return request, result
}

func findAvailableSkillInstruction(skillInstructions []SkillInstruction, skillName string) (SkillInstruction, bool) {
	trimmedSkillName := strings.TrimSpace(skillName)
	for _, skillInstruction := range skillInstructions {
		if strings.TrimSpace(skillInstruction.Name) == trimmedSkillName {
			return skillInstruction, true
		}
	}
	return SkillInstruction{}, false
}

func unavailableSkillToolNames(toolSet *ToolSet, skillInstruction SkillInstruction) []string {
	missingToolNames := []string{}
	for _, toolName := range SkillToolNames(skillInstruction) {
		if toolSet == nil || !toolSet.IsRegistered(toolName) || !toolSet.CanExpose(toolName) {
			missingToolNames = appendUniqueStrings(missingToolNames, toolName)
		}
	}
	return missingToolNames
}

func appendPinnedSkillPrompt(instructionPrompt string, skillInstructions []SkillInstruction) string {
	pinnedPrompt := buildSelectedSkillInstructionPrompt(skillInstructions)
	return strings.Join(nonEmptyStrings([]string{instructionPrompt, pinnedPrompt}), "\n\n")
}

func manualCapabilityObservation(index int, requirement manualCapabilityRequirement, result manualCapabilityResult) turnObservation {
	content := marshalManualCapabilityResult(requirement, result)
	observation := newContentObservation(nextObservationID(index), "require_capabilities", "", content)
	if manualCapabilityResultFailed(result) {
		observation.Failure = &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "require_capabilities",
			UserSafeSummary: manualCapabilityFailureSummary(result),
		}
	}
	return observation
}

func manualCapabilityResultFailed(result manualCapabilityResult) bool {
	return len(result.UnknownToolNames) > 0 ||
		len(result.UnavailableToolNames) > 0 ||
		len(result.UnknownSkillNames) > 0 ||
		len(result.SkillsMissingAllowedTools) > 0 ||
		result.EmptyRequirement
}

func manualCapabilityFailureSummary(result manualCapabilityResult) string {
	parts := []string{}
	if len(result.UnknownToolNames) > 0 {
		parts = append(parts, "unknown_tool="+strings.Join(result.UnknownToolNames, ","))
	}
	if len(result.UnavailableToolNames) > 0 {
		parts = append(parts, "tool_unavailable="+strings.Join(result.UnavailableToolNames, ","))
	}
	if len(result.UnknownSkillNames) > 0 {
		parts = append(parts, "unknown_skill="+strings.Join(result.UnknownSkillNames, ","))
	}
	if len(result.SkillsMissingAllowedTools) > 0 {
		parts = append(parts, "skill_missing_allowed_tools")
	}
	if result.EmptyRequirement {
		parts = append(parts, "empty_requirement")
	}
	return strings.Join(parts, "; ")
}

func marshalManualCapabilityResult(requirement manualCapabilityRequirement, result manualCapabilityResult) string {
	document, errorValue := json.Marshal(map[string]any{
		"requirement": requirement,
		"result":      result,
	})
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}
