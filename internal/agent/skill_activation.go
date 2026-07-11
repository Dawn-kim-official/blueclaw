package agent

import (
	"encoding/json"
	"strings"
)

const maximumSelectableSkillSearchResultCount = 8

func selectableSkillNamesAfterSearch(request AgentTurnRequest, observations []turnObservation) []string {
	selectedSkillNames := selectedSkillNameSet(request.SkillDecisions)
	for _, skillName := range request.PinnedSkillNames {
		selectedSkillNames[strings.TrimSpace(skillName)] = true
	}
	availableSkills := availableSelectableSkills(request)
	for observationIndex := len(observations) - 1; observationIndex >= 0; observationIndex-- {
		observation := observations[observationIndex]
		if observation.Tool != SkillSearchToolName || observation.Failed() {
			continue
		}
		return selectableSkillNamesFromSearchResult(observation.ContentText(), availableSkills, selectedSkillNames)
	}
	return nil
}

func applySearchedSkillSelection(request AgentTurnRequest, observations []turnObservation, requestArguments requestToolsArguments) (AgentTurnRequest, toolRequestResult) {
	selectableSkillNames := selectableSkillNamesAfterSearch(request, observations)
	selectableSkills := map[string]bool{}
	for _, skillName := range selectableSkillNames {
		selectableSkills[skillName] = true
	}
	requestedSkillNames := appendUniqueStrings(requestArguments.SkillNames)
	result := toolRequestResult{}
	if len(requestedSkillNames) == 0 {
		result.EmptyRequirement = true
		return request, result
	}
	for _, skillName := range requestedSkillNames {
		trimmedSkillName := strings.TrimSpace(skillName)
		if trimmedSkillName == "" || !selectableSkills[trimmedSkillName] {
			result.UnsearchedSkillNames = appendUniqueStrings(result.UnsearchedSkillNames, trimmedSkillName)
		}
	}
	if len(result.UnsearchedSkillNames) > 0 {
		return request, result
	}
	requestArguments.ToolNames = nil
	requestArguments.SkillNames = requestedSkillNames
	return applyToolRequest(request, requestArguments)
}

func applyLegacyModelPaletteRequest(request AgentTurnRequest, observations []turnObservation, requestArguments requestToolsArguments) (AgentTurnRequest, toolRequestResult) {
	if len(appendUniqueStrings(requestArguments.SkillNames)) > 0 {
		if len(appendUniqueStrings(requestArguments.ToolNames)) > 0 {
			return request, toolRequestResult{UnavailableToolNames: appendUniqueStrings(requestArguments.ToolNames)}
		}
		return applySearchedSkillSelection(request, observations, requestArguments)
	}
	result := toolRequestResult{}
	requestedToolNames := normalizeRequestedToolNames(requestArguments.ToolNames, request.ToolSet)
	if len(appendUniqueStrings(requestedToolNames)) == 0 {
		result.EmptyRequirement = true
		return request, result
	}
	for _, toolName := range appendUniqueStrings(requestedToolNames) {
		if request.ToolSet == nil || !request.ToolSet.IsAllowed(toolName) {
			result.UnavailableToolNames = appendUniqueStrings(result.UnavailableToolNames, toolName)
			continue
		}
		result.PinnedToolNames = appendUniqueStrings(result.PinnedToolNames, toolName)
	}
	return request, result
}

func availableSelectableSkills(request AgentTurnRequest) map[string]bool {
	availableSkills := map[string]bool{}
	for _, skillInstruction := range request.AvailableSkills {
		if !skillProfileAllows(skillInstruction, normalizedAgentProfileName(request.ProfileName)) {
			continue
		}
		if len(missingAllowedTools(skillInstruction, agentRequestFromTurnRequest(request))) > 0 {
			continue
		}
		availableSkills[skillInstruction.Name] = true
	}
	return availableSkills
}

func selectableSkillNamesFromSearchResult(content string, availableSkills map[string]bool, selectedSkills map[string]bool) []string {
	var searchResult SkillSearchResult
	if json.Unmarshal([]byte(content), &searchResult) != nil {
		return nil
	}
	if len(searchResult.Skills) == 0 || len(searchResult.Skills) > maximumSelectableSkillSearchResultCount {
		return nil
	}
	selectableSkillNames := []string{}
	for _, skill := range searchResult.Skills {
		skillName := strings.TrimSpace(skill.Name)
		if skillName == "" || !availableSkills[skillName] || selectedSkills[skillName] {
			continue
		}
		selectableSkillNames = appendUniqueStrings(selectableSkillNames, skillName)
	}
	return selectableSkillNames
}
