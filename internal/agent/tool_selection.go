package agent

import (
	"strings"
)

type requestToolsArguments struct {
	ToolNames  []string `json:"toolNames"`
	SkillNames []string `json:"skillNames"`
	Reason     string   `json:"reason,omitempty"`
}

type toolRequestResult struct {
	PinnedToolNames           []string                   `json:"pinnedToolNames,omitempty"`
	PinnedSkillNames          []string                   `json:"pinnedSkillNames,omitempty"`
	UnknownToolNames          []string                   `json:"unknownToolNames,omitempty"`
	UnavailableToolNames      []string                   `json:"unavailableToolNames,omitempty"`
	ToolCandidates            map[string][]toolCandidate `json:"toolCandidates,omitempty"`
	UnknownSkillNames         []string                   `json:"unknownSkillNames,omitempty"`
	ReclassifiedSkillsAsTools []string                   `json:"reclassifiedSkillsAsTools,omitempty"`
	SkillsMissingAllowedTools map[string][]string        `json:"skillsMissingAllowedTools,omitempty"`
	EmptyRequirement          bool                       `json:"emptyRequirement,omitempty"`
}

type toolCandidate struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Availability ToolAvailability `json:"availability"`
	MatchReason  string           `json:"matchReason,omitempty"`
}

func applyToolRequest(request AgentTurnRequest, requestArguments requestToolsArguments) (AgentTurnRequest, toolRequestResult) {
	result := toolRequestResult{SkillsMissingAllowedTools: map[string][]string{}}
	if len(appendUniqueStrings(requestArguments.ToolNames)) == 0 && len(appendUniqueStrings(requestArguments.SkillNames)) == 0 {
		result.EmptyRequirement = true
	}
	request, result = pinRequestedTools(request, requestArguments.ToolNames, result)
	request, result = pinRequestedSkills(request, requestArguments.SkillNames, result)
	request, result = reclassifySkillNamesThatAreTools(request, result)
	if len(result.SkillsMissingAllowedTools) == 0 {
		result.SkillsMissingAllowedTools = nil
	}
	if len(result.ToolCandidates) == 0 {
		result.ToolCandidates = nil
	}
	return request, result
}

func pinRequestedTools(request AgentTurnRequest, toolNames []string, result toolRequestResult) (AgentTurnRequest, toolRequestResult) {
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

func toolRequestResultFailed(result toolRequestResult) bool {
	return len(result.UnknownToolNames) > 0 ||
		len(result.UnavailableToolNames) > 0 ||
		len(result.UnknownSkillNames) > 0 ||
		len(result.SkillsMissingAllowedTools) > 0 ||
		result.EmptyRequirement
}

