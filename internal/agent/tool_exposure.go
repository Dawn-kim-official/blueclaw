package agent

import (
	"encoding/json"
	"strings"
)

const maxSchemaCallableToolCount = 15

type toolExposureGroup struct {
	Name    string
	ToolIDs []string
}

type droppedToolGroup struct {
	Name      string   `json:"name"`
	ToolIDs   []string `json:"toolIDs"`
	IsPartial bool     `json:"isPartial"`
}

type ToolExposureEvent struct {
	SelectedToolIDs        []string           `json:"selectedToolIDs,omitempty"`
	ValidSelectedToolIDs   []string           `json:"validSelectedToolIDs,omitempty"`
	SelectionReason        string             `json:"selectionReason,omitempty"`
	SelectionSource        string             `json:"selectionSource,omitempty"`
	SelectionFailureReason string             `json:"selectionFailureReason,omitempty"`
	UsedFallbackGroups     bool               `json:"usedFallbackGroups"`
	ExposedToolIDs         []string           `json:"exposedToolIDs"`
	SelectedSkillToolIDs   []string           `json:"selectedSkillToolIDs,omitempty"`
	PinnedGroupToolIDs     []string           `json:"pinnedGroupToolIDs,omitempty"`
	DroppedGroups          []droppedToolGroup `json:"droppedGroups,omitempty"`
}

func toolSetForAgentTurnWithExposure(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, selectionEvent ToolExposureEvent, observations ...[]turnObservation) (*ToolSet, ToolExposureEvent) {
	if toolSet == nil {
		return nil, selectionEvent
	}
	recentObservations := []turnObservation{}
	if len(observations) > 0 {
		recentObservations = observations[0]
	}
	kernelGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "fixed kernel", ToolIDs: kernelToolNamesForRequest(request)})
	interactionGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "required interaction", ToolIDs: requiredInteractionToolNames(outcomeContract, recentObservations)})
	recoveryToolNames := appendUniqueStrings(activeRecoveryToolNames(recentObservations), activeRecoveryPreconditionToolNames(toolSet, recentObservations)...)
	recoveryGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "recovery tools", ToolIDs: recoveryToolNames})
	selectedSkillGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "selected skills", ToolIDs: selectedSkillToolNames(instructionBundle)})
	pinnedGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "pinned tools", ToolIDs: request.PinnedToolNames})
	groups := []toolExposureGroup{interactionGroup, recoveryGroup, pinnedGroup, selectedSkillGroup, kernelGroup}
	exposedToolIDs, droppedGroups := selectToolGroups(groups, maxSchemaCallableToolCount)
	selectionEvent.SelectionSource = firstNonEmptyString(selectionEvent.SelectionSource, toolSelectionSource(selectedSkillGroup))
	selectionEvent.SelectionReason = firstNonEmptyString(selectionEvent.SelectionReason, toolSelectionReason(selectedSkillGroup))
	selectionEvent.ValidSelectedToolIDs = nil
	selectionEvent.ExposedToolIDs = append([]string{}, exposedToolIDs...)
	selectionEvent.SelectedSkillToolIDs = exposedGroupToolIDs(selectedSkillGroup, exposedToolIDs)
	selectionEvent.PinnedGroupToolIDs = append([]string{}, pinnedGroup.ToolIDs...)
	selectionEvent.DroppedGroups = droppedGroups
	selectionEvent.UsedFallbackGroups = false
	return toolSet.WithAllowedToolNames(exposedToolIDsForFiltering(exposedToolIDs)), selectionEvent
}

func exposedGroupToolIDs(group toolExposureGroup, exposedToolIDs []string) []string {
	toolIDs := []string{}
	for _, toolID := range group.ToolIDs {
		if stringSliceContains(exposedToolIDs, toolID) {
			toolIDs = append(toolIDs, toolID)
		}
	}
	return toolIDs
}

func selectedSkillToolNames(instructionBundle InstructionBundle) []string {
	toolNames := []string{}
	for _, skillInstruction := range selectedSkillInstructionList(instructionBundle) {
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	return toolNames
}

func selectToolGroups(groups []toolExposureGroup, limit int) ([]string, []droppedToolGroup) {
	toolIDs := []string{}
	droppedGroups := []droppedToolGroup{}
	for _, group := range groups {
		droppedToolIDs := []string{}
		hasSelectedTool := false
		for _, toolID := range group.ToolIDs {
			if stringSliceContains(toolIDs, toolID) {
				hasSelectedTool = true
				continue
			}
			if len(toolIDs) >= limit {
				droppedToolIDs = append(droppedToolIDs, toolID)
				continue
			}
			toolIDs = append(toolIDs, toolID)
			hasSelectedTool = true
		}
		if len(droppedToolIDs) > 0 {
			droppedGroups = append(droppedGroups, droppedToolGroup{Name: group.Name, ToolIDs: droppedToolIDs, IsPartial: hasSelectedTool})
		}
	}
	return toolIDs, droppedGroups
}

func toolSelectionSource(selectedSkillGroup toolExposureGroup) string {
	if len(selectedSkillGroup.ToolIDs) > 0 {
		return "selected_skills"
	}
	return "fixed_kernel"
}

func toolSelectionReason(selectedSkillGroup toolExposureGroup) string {
	if len(selectedSkillGroup.ToolIDs) > 0 {
		return "Blueclaw exposes direct tools declared by the selected skills"
	}
	return "Blueclaw exposes the compact kernel tools"
}

func kernelToolNamesForRequest(request AgentRequest) []string {
	toolNames := KernelToolNames()
	if request.TaskShape != TaskShapeImmediateReply {
		return toolNames
	}
	return removeToolName(toolNames, SkillSearchToolName)
}

func requiredInteractionToolNames(outcomeContract OutcomeContract, observations []turnObservation) []string {
	if expectedResultRequiresTool(outcomeContract, AskInputToolName) {
		return []string{AskInputToolName}
	}
	for _, toolName := range activeRecoveryToolNames(observations) {
		if toolName == AskInputToolName {
			return []string{AskInputToolName}
		}
	}
	return nil
}

func exposedToolIDsForFiltering(exposedToolIDs []string) []string {
	if len(exposedToolIDs) > 0 {
		return exposedToolIDs
	}
	return []string{"__blueclaw_no_callable_tools__"}
}

func toolSetForAgentTurn(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) *ToolSet {
	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract, ToolExposureEvent{})
	return filteredToolSet
}

func filterGroupTools(toolSet *ToolSet, group toolExposureGroup) toolExposureGroup {
	filteredToolIDs := []string{}
	for _, toolID := range group.ToolIDs {
		trimmedToolID := strings.TrimSpace(toolID)
		if trimmedToolID != "" && toolIsModelCallable(trimmedToolID) && toolSet != nil && toolSet.CanExpose(trimmedToolID) {
			filteredToolIDs = appendUniqueStrings(filteredToolIDs, trimmedToolID)
		}
	}
	return toolExposureGroup{Name: group.Name, ToolIDs: filteredToolIDs}
}

func toolIsModelCallable(toolID string) bool {
	trimmedToolID := strings.TrimSpace(toolID)
	return trimmedToolID != "" &&
		trimmedToolID != AskConfirmToolName &&
		trimmedToolID != CapabilityInvokeToolName &&
		trimmedToolID != TaskHistoryToolName
}

func activeRecoveryToolNames(observations []turnObservation) []string {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return nil
	}
	toolNames := []string{}
	if failureDebt.LatestFailure.Failure != nil {
		for _, recoveryHint := range failureDebt.LatestFailure.Failure.RecoveryHints {
			toolNames = appendUniqueStrings(toolNames, recoveryHint.ToolNames...)
		}
	}
	if failureDebt.LatestFailure.RecoveryPacket != nil {
		toolNames = appendUniqueStrings(toolNames, failureDebt.LatestFailure.RecoveryPacket.AllowedTools...)
	}
	return filterExhaustedRecoveryToolNames(toolNames, observations)
}

func activeRecoveryPreconditionToolNames(toolSet *ToolSet, observations []turnObservation) []string {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt || toolSet == nil {
		return nil
	}
	toolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if toolCanSatisfyRecoveryPrecondition(failureDebt.LatestFailure, toolName) {
			toolNames = append(toolNames, toolName)
		}
	}
	return filterExhaustedRecoveryToolNames(toolNames, observations)
}

func filterExhaustedRecoveryToolNames(toolNames []string, observations []turnObservation) []string {
	exhaustedToolNames := exhaustedRecoveryToolNames(observations)
	if len(exhaustedToolNames) == 0 {
		return appendUniqueStrings(toolNames)
	}
	filteredToolNames := []string{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" || exhaustedToolNames[trimmedToolName] {
			continue
		}
		filteredToolNames = appendUniqueStrings(filteredToolNames, trimmedToolName)
	}
	return filteredToolNames
}

func exhaustedRecoveryToolNames(observations []turnObservation) map[string]bool {
	exhaustedToolNames := map[string]bool{}
	for _, observation := range observations {
		if observationLooksLikeFileReadRepeat(observation) {
			exhaustedToolNames["file.read"] = true
			continue
		}
		if !observationLooksLikeRecoveryBudgetExhausted(observation) {
			continue
		}
		toolName := strings.TrimSpace(observation.Tool)
		if toolName != "" {
			exhaustedToolNames[toolName] = true
		}
	}
	return exhaustedToolNames
}

func observationLooksLikeFileReadRepeat(observation turnObservation) bool {
	return strings.TrimSpace(observation.Tool) == "file.read" &&
		observation.Failure != nil &&
		strings.TrimSpace(observation.Failure.Stage) == "file_read_repeat"
}

func observationLooksLikeRecoveryBudgetExhausted(observation turnObservation) bool {
	return strings.TrimSpace(observation.Action) == "policy" &&
		strings.TrimSpace(observation.RecoveryStep) != "" &&
		strings.TrimSpace(observation.PolicyCode) == "recovery_budget_exhausted"
}

func outcomeContractJSON(contract OutcomeContract) string {
	if !OutcomeContractHasRequirements(contract) {
		return ""
	}
	document, errorValue := json.Marshal(contract)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func visibleContextMaterialLooksLikeImage(material VisibleContextMaterial) bool {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	filename := strings.ToLower(strings.TrimSpace(material.Filename))
	return strings.HasSuffix(filename, ".png") ||
		strings.HasSuffix(filename, ".jpg") ||
		strings.HasSuffix(filename, ".jpeg") ||
		strings.HasSuffix(filename, ".gif") ||
		strings.HasSuffix(filename, ".webp")
}
