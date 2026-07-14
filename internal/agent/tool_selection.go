package agent

import (
	"encoding/json"
	"strings"
)

type requestToolsArguments struct {
	ToolNames  []string `json:"toolNames"`
	SkillNames []string `json:"skillNames"`
	Reason     string   `json:"reason,omitempty"`
}

type toolRequestResult struct {
	PinnedToolNames           []string            `json:"pinnedToolNames,omitempty"`
	PinnedSkillNames          []string            `json:"pinnedSkillNames,omitempty"`
	UnknownToolNames          []string            `json:"unknownToolNames,omitempty"`
	UnavailableToolNames      []string            `json:"unavailableToolNames,omitempty"`
	UnknownSkillNames         []string            `json:"unknownSkillNames,omitempty"`
	ReclassifiedSkillsAsTools []string            `json:"reclassifiedSkillsAsTools,omitempty"`
	SkillsMissingAllowedTools map[string][]string `json:"skillsMissingAllowedTools,omitempty"`
	EmptyRequirement          bool                `json:"emptyRequirement,omitempty"`
}

func applyToolRequest(request AgentTurnRequest, requestArguments requestToolsArguments) (AgentTurnRequest, toolRequestResult) {
	result := toolRequestResult{SkillsMissingAllowedTools: map[string][]string{}}
	requestArguments.ToolNames = normalizeRequestedToolNames(requestArguments.ToolNames, request.ToolSet)
	if len(appendUniqueStrings(requestArguments.ToolNames)) == 0 && len(appendUniqueStrings(requestArguments.SkillNames)) == 0 {
		result.EmptyRequirement = true
	}
	request, result = pinRequestedTools(request, requestArguments.ToolNames, result)
	request, result = pinRequestedSkills(request, requestArguments.SkillNames, result)
	request, result = reclassifySkillNamesThatAreTools(request, result)
	if len(result.SkillsMissingAllowedTools) == 0 {
		result.SkillsMissingAllowedTools = nil
	}
	return request, result
}

func normalizeRequestedToolNames(toolNames []string, toolSet *ToolSet) []string {
	normalizedToolNames := []string{}
	for _, toolName := range toolNames {
		normalizedToolNames = appendUniqueStrings(normalizedToolNames, normalizeRequestedToolName(toolName, toolSet))
	}
	return normalizedToolNames
}

func normalizeRequestedToolName(toolName string, toolSet *ToolSet) string {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return ""
	}
	if toolSet != nil && toolSet.IsRegistered(trimmedToolName) {
		return trimmedToolName
	}
	if normalizedToolName, isFound := normalizeContinueActionToolName(trimmedToolName, toolSet); isFound {
		return normalizedToolName
	}
	return trimmedToolName
}

func normalizeContinueActionToolName(toolName string, toolSet *ToolSet) (string, bool) {
	if toolSet == nil {
		return "", false
	}
	encodedToolName, hasPrefix := strings.CutPrefix(toolName, "continue__")
	if !hasPrefix || strings.TrimSpace(encodedToolName) == "" {
		return "", false
	}
	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		registeredToolName := strings.TrimSpace(toolDefinition.Name)
		if registeredToolName == "" {
			continue
		}
		if strings.EqualFold(encodeContinueActionToolName(registeredToolName), toolName) {
			return registeredToolName, true
		}
	}
	decodedToolName := strings.ReplaceAll(encodedToolName, "_", ".")
	if toolSet.IsRegistered(decodedToolName) {
		return decodedToolName, true
	}
	canonicalToolName := CanonicalEvidenceToolName(decodedToolName)
	if toolSet.IsRegistered(canonicalToolName) {
		return canonicalToolName, true
	}
	return "", false
}

func encodeContinueActionToolName(toolName string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_", "/", "_")
	return "continue__" + replacer.Replace(strings.TrimSpace(toolName))
}

func pinRequestedTools(request AgentTurnRequest, toolNames []string, result toolRequestResult) (AgentTurnRequest, toolRequestResult) {
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

func reclassifySkillNamesThatAreTools(request AgentTurnRequest, result toolRequestResult) (AgentTurnRequest, toolRequestResult) {
	if len(result.UnknownSkillNames) == 0 {
		return request, result
	}
	remainingUnknownSkillNames := []string{}
	for _, skillName := range result.UnknownSkillNames {
		trimmedName := strings.TrimSpace(skillName)
		if request.ToolSet == nil || !request.ToolSet.IsRegistered(trimmedName) {
			remainingUnknownSkillNames = appendUniqueStrings(remainingUnknownSkillNames, skillName)
			continue
		}
		if !request.ToolSet.CanExpose(trimmedName) {
			result.UnavailableToolNames = appendUniqueStrings(result.UnavailableToolNames, trimmedName)
			continue
		}
		result.ReclassifiedSkillsAsTools = appendUniqueStrings(result.ReclassifiedSkillsAsTools, trimmedName)
		result.PinnedToolNames = appendUniqueStrings(result.PinnedToolNames, trimmedName)
		request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, trimmedName)
	}
	result.UnknownSkillNames = remainingUnknownSkillNames
	request.ToolSet = request.ToolSet.WithAdditionalAllowedToolNames(request.PinnedToolNames)
	return request, result
}

func pinRequestedSkills(request AgentTurnRequest, skillNames []string, result toolRequestResult) (AgentTurnRequest, toolRequestResult) {
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
		result.PinnedSkillNames = appendUniqueStrings(result.PinnedSkillNames, trimmedSkillName)
		request.PinnedSkillNames = appendUniqueStrings(request.PinnedSkillNames, trimmedSkillName)
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

func appendPinnedSkillPrompt(instructionPrompt string, skillInstructions []SkillInstruction) string {
	pinnedPrompt := buildSelectedSkillInstructionPrompt(skillInstructions)
	return strings.Join(nonEmptyStrings([]string{instructionPrompt, pinnedPrompt}), "\n\n")
}

func toolRequestObservation(index int, requestArguments requestToolsArguments, result toolRequestResult) turnObservation {
	content := marshalToolSelectionResult(requestArguments, result)
	observation := newContentObservation(nextObservationID(index), "tool.request", "", content)
	if toolRequestResultFailed(result) {
		observation.Failure = &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "tool.request",
			UserSafeSummary: toolRequestFailureSummary(result),
		}
	}
	return observation
}

func toolRequestAddedNothing(before AgentTurnRequest, after AgentTurnRequest, result toolRequestResult) bool {
	if toolRequestResultFailed(result) {
		return false
	}
	return len(after.PinnedToolNames) == len(before.PinnedToolNames) &&
		len(after.PinnedSkillNames) == len(before.PinnedSkillNames)
}

func redundantToolSelectionObservation(index int, requestArguments requestToolsArguments, result toolRequestResult) turnObservation {
	requested := appendUniqueStrings(append(append([]string{}, requestArguments.ToolNames...), requestArguments.SkillNames...))
	directive := "These kernel tools, skills, or capability operations are already available in your palette: " + strings.Join(requested, ", ") + ". Do not request them again; use one now to make progress, or finish."
	content := marshalEventBody(map[string]any{
		"request":   requestArguments,
		"result":    result,
		"redundant": true,
		"directive": directive,
	})
	observation := newContentObservation(nextObservationID(index), "tool.request", "", content)
	observation.Summary = directive
	return observation
}

func ambientFixedPaletteObservation(index int, requestArguments requestToolsArguments) turnObservation {
	directive := "This ambient capture has a fixed palette: use only the already-available task and calendar capability operations, then finish. Do not request other capabilities."
	content := marshalEventBody(map[string]any{
		"request":      requestArguments,
		"fixedPalette": true,
		"directive":    directive,
	})
	observation := newContentObservation(nextObservationID(index), "tool.request", "", content)
	observation.Summary = directive
	return observation
}

func toolRequestResultFailed(result toolRequestResult) bool {
	return len(result.UnknownToolNames) > 0 ||
		len(result.UnavailableToolNames) > 0 ||
		len(result.UnknownSkillNames) > 0 ||
		len(result.SkillsMissingAllowedTools) > 0 ||
		result.EmptyRequirement
}

func toolRequestFailureSummary(result toolRequestResult) string {
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

func marshalToolSelectionResult(requestArguments requestToolsArguments, result toolRequestResult) string {
	document, errorValue := json.Marshal(map[string]any{
		"request": requestArguments,
		"result":  result,
	})
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}
