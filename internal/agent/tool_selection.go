package agent

import (
	"encoding/json"
	"strings"
)

type selectToolsRequest struct {
	ToolNames  []string `json:"toolNames"`
	SkillNames []string `json:"skillNames"`
	Reason     string   `json:"reason,omitempty"`
}

type toolSelectionResult struct {
	PinnedToolNames           []string                   `json:"pinnedToolNames,omitempty"`
	PinnedSkillNames          []string                   `json:"pinnedSkillNames,omitempty"`
	UnknownToolNames          []string                   `json:"unknownToolNames,omitempty"`
	UnavailableToolNames      []string                   `json:"unavailableToolNames,omitempty"`
	ToolCandidates            map[string][]toolCandidate `json:"toolCandidates,omitempty"`
	UnknownSkillNames         []string                   `json:"unknownSkillNames,omitempty"`
	SkillsMissingAllowedTools map[string][]string        `json:"skillsMissingAllowedTools,omitempty"`
	EmptyRequirement          bool                       `json:"emptyRequirement,omitempty"`
}

type toolCandidate struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Availability ToolAvailability `json:"availability"`
	MatchReason  string           `json:"matchReason,omitempty"`
}

func applyToolSelectionRequest(request AgentTurnRequest, selectionRequest selectToolsRequest) (AgentTurnRequest, toolSelectionResult) {
	result := toolSelectionResult{SkillsMissingAllowedTools: map[string][]string{}}
	if len(appendUniqueStrings(selectionRequest.ToolNames)) == 0 && len(appendUniqueStrings(selectionRequest.SkillNames)) == 0 {
		result.EmptyRequirement = true
	}
	request, result = pinSelectedTools(request, selectionRequest.ToolNames, result)
	request, result = pinSelectedSkills(request, selectionRequest.SkillNames, result)
	if len(result.SkillsMissingAllowedTools) == 0 {
		result.SkillsMissingAllowedTools = nil
	}
	if len(result.ToolCandidates) == 0 {
		result.ToolCandidates = nil
	}
	return request, result
}

func pinSelectedTools(request AgentTurnRequest, toolNames []string, result toolSelectionResult) (AgentTurnRequest, toolSelectionResult) {
	for _, toolName := range appendUniqueStrings(toolNames) {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		if request.ToolSet == nil || !request.ToolSet.IsRegistered(trimmedToolName) {
			result.UnknownToolNames = appendUniqueStrings(result.UnknownToolNames, trimmedToolName)
			result.ToolCandidates = addToolCandidates(result.ToolCandidates, trimmedToolName, request.ToolSet)
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

func addToolCandidates(candidates map[string][]toolCandidate, requestedToolName string, toolSet *ToolSet) map[string][]toolCandidate {
	matches := matchingToolCandidates(requestedToolName, toolSet, 3)
	if len(matches) == 0 {
		return candidates
	}
	if candidates == nil {
		candidates = map[string][]toolCandidate{}
	}
	candidates[strings.TrimSpace(requestedToolName)] = matches
	return candidates
}

func matchingToolCandidates(requestedToolName string, toolSet *ToolSet, limit int) []toolCandidate {
	if toolSet == nil || strings.TrimSpace(requestedToolName) == "" {
		return nil
	}
	candidates := []toolCandidate{}
	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		candidate, isMatch := toolCandidateForRequest(requestedToolName, toolDefinition, toolSet)
		if !isMatch {
			continue
		}
		candidates = append(candidates, candidate)
		if len(candidates) >= limit {
			return candidates
		}
	}
	return candidates
}

func toolCandidateForRequest(requestedToolName string, toolDefinition ToolDefinition, toolSet *ToolSet) (toolCandidate, bool) {
	toolName := strings.TrimSpace(toolDefinition.Name)
	if toolName == "" {
		return toolCandidate{}, false
	}
	matchReason := toolCandidateMatchReason(requestedToolName, toolDefinition)
	if matchReason == "" {
		return toolCandidate{}, false
	}
	availability, _ := toolSet.ToolAvailability(toolName)
	if strings.TrimSpace(availability.Status) == ToolAvailabilityDenied {
		return toolCandidate{}, false
	}
	return toolCandidate{
		Name:         toolName,
		Description:  firstNonEmptyString(strings.TrimSpace(toolDefinition.Description), specificToolDescription(toolName)),
		Availability: availability,
		MatchReason:  matchReason,
	}, true
}

func toolCandidateMatchReason(requestedToolName string, toolDefinition ToolDefinition) string {
	requestedText := strings.ToLower(strings.TrimSpace(requestedToolName))
	toolName := strings.ToLower(strings.TrimSpace(toolDefinition.Name))
	if requestedText == "" || toolName == "" {
		return ""
	}
	if strings.EqualFold(requestedText, toolName) {
		return "exact"
	}
	if toolNameContainsSharedSegment(toolName, requestedText) {
		return "name_segment"
	}
	searchText := strings.ToLower(strings.Join([]string{
		toolDefinition.Name,
		toolDefinition.Description,
		toolDefinition.RecoveryCard.Does,
		toolDefinition.RecoveryCard.Produces,
		toolDefinition.RecoveryCard.UseWhen,
	}, " "))
	for _, token := range searchTokens(requestedText) {
		if strings.Contains(searchText, token) {
			return "query_token:" + token
		}
	}
	return ""
}

func toolNameContainsSharedSegment(toolName string, requestedText string) bool {
	for _, token := range searchTokens(requestedText) {
		for _, segment := range strings.FieldsFunc(toolName, func(value rune) bool { return value == '.' || value == '_' || value == '-' }) {
			if strings.TrimSpace(segment) == token {
				return true
			}
		}
	}
	return false
}

func searchTokens(value string) []string {
	tokens := []string{}
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(value rune) bool {
		return value == '.' || value == '_' || value == '-' || value == ' ' || value == '/'
	}) {
		trimmedToken := strings.TrimSpace(token)
		if len([]rune(trimmedToken)) >= 3 {
			tokens = appendUniqueStrings(tokens, trimmedToken)
		}
	}
	return tokens
}

func pinSelectedSkills(request AgentTurnRequest, skillNames []string, result toolSelectionResult) (AgentTurnRequest, toolSelectionResult) {
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

func toolSelectionObservation(index int, selectionRequest selectToolsRequest, result toolSelectionResult) turnObservation {
	content := marshalToolSelectionResult(selectionRequest, result)
	observation := newContentObservation(nextObservationID(index), "select_tools", "", content)
	if toolSelectionResultFailed(result) {
		observation.Failure = &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "select_tools",
			UserSafeSummary: toolSelectionFailureSummary(result),
		}
	}
	return observation
}

func toolSelectionAddedNothing(before AgentTurnRequest, after AgentTurnRequest, result toolSelectionResult) bool {
	if toolSelectionResultFailed(result) {
		return false
	}
	return len(after.PinnedToolNames) == len(before.PinnedToolNames) &&
		len(after.PinnedSkillNames) == len(before.PinnedSkillNames)
}

func redundantToolSelectionObservation(index int, selectionRequest selectToolsRequest, result toolSelectionResult) turnObservation {
	requested := appendUniqueStrings(append(append([]string{}, selectionRequest.ToolNames...), selectionRequest.SkillNames...))
	directive := "These tools and skills are already available in your palette: " + strings.Join(requested, ", ") + ". Do not call select_tools again for tools you already have; call one of them now to make progress, or finish."
	content := marshalEventBody(map[string]any{
		"request":   selectionRequest,
		"result":    result,
		"redundant": true,
		"directive": directive,
	})
	observation := newContentObservation(nextObservationID(index), "select_tools", "", content)
	observation.Summary = directive
	return observation
}

func toolSelectionResultFailed(result toolSelectionResult) bool {
	return len(result.UnknownToolNames) > 0 ||
		len(result.UnavailableToolNames) > 0 ||
		len(result.UnknownSkillNames) > 0 ||
		len(result.SkillsMissingAllowedTools) > 0 ||
		result.EmptyRequirement
}

func toolSelectionFailureSummary(result toolSelectionResult) string {
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

func marshalToolSelectionResult(selectionRequest selectToolsRequest, result toolSelectionResult) string {
	document, errorValue := json.Marshal(map[string]any{
		"request": selectionRequest,
		"result":  result,
	})
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}
