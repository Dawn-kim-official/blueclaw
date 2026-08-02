package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
)

const maxExtensionCallableToolCount = 15

type toolExposureGroup struct {
	Name    string
	ToolIDs []string
}

func toolSetForAgentTurnWithExposure(toolSet *toolcontract.ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, selectionEvent ToolExposureEvent, observations ...[]turnObservation) (*toolcontract.ToolSet, ToolExposureEvent) {
	if toolSet == nil {
		return nil, selectionEvent
	}
	recentObservations := []turnObservation{}
	if len(observations) > 0 {
		recentObservations = observations[0]
	}
	interactionGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "required interaction", ToolIDs: requiredInteractionToolNames(outcomeContract, recentObservations)})
	recoveryToolNames := activeRecoveryToolNames(recentObservations)
	recoveryGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "recovery tools", ToolIDs: recoveryToolNames})
	pendingToolName := firstPendingRequiredToolName(instructionBundle.RequiredNextTools, recentObservations)
	pendingGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "pending working-set tool", ToolIDs: []string{pendingToolName}})
	requiredEvidenceGroup, evidenceAlternativesGroup := outcomeContractEvidenceGroups(toolSet, outcomeContract)
	selectedSkillGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "selected skills", ToolIDs: selectedSkillToolNames(instructionBundle)})
	pinnedGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "pinned tools", ToolIDs: request.PinnedToolNames})
	requiredNextGroup := filterGroupTools(toolSet, toolExposureGroup{Name: "required next tools", ToolIDs: instructionBundle.RequiredNextTools})
	hasAuthoritativeWorkingSet := instructionBundle.HasContractSkillArbitration &&
		len(selectedSkillInstructionList(instructionBundle)) > 0 &&
		(len(instructionBundle.RequiredNextTools) > 0 || len(instructionBundle.RequiredEvidenceTools) > 0)
	groups := []toolExposureGroup{interactionGroup, recoveryGroup, pendingGroup, requiredEvidenceGroup, pinnedGroup, selectedSkillGroup, evidenceAlternativesGroup}
	if hasAuthoritativeWorkingSet {
		groups = []toolExposureGroup{interactionGroup, recoveryGroup, pendingGroup, requiredEvidenceGroup, pinnedGroup, requiredNextGroup, selectedSkillGroup, evidenceAlternativesGroup}
	}
	extensionToolIDs, droppedGroups := selectToolGroups(extensionToolGroups(groups), maxExtensionCallableToolCount)
	kernelToolIDs := []string{}
	if requestNeedsToolAccess(request, groups) {
		kernelToolIDs = filterGroupTools(toolSet, toolExposureGroup{ToolIDs: kernelToolNamesForInstructionBundle(instructionBundle)}).ToolIDs
	}
	exposedToolIDs := appendUniqueStrings(kernelToolIDs, extensionToolIDs...)
	selectionEvent.SelectionSource = firstNonEmptyString(selectionEvent.SelectionSource, toolSelectionSource(selectedSkillGroup, hasAuthoritativeWorkingSet))
	selectionEvent.SelectionReason = firstNonEmptyString(selectionEvent.SelectionReason, toolSelectionReason(selectedSkillGroup, hasAuthoritativeWorkingSet))
	selectionEvent.ValidSelectedToolIDs = nil
	selectionEvent.ExposedToolIDs = append([]string{}, exposedToolIDs...)
	selectionEvent.SelectedSkillToolIDs = exposedGroupToolIDs(selectedSkillGroup, exposedToolIDs)
	selectionEvent.PinnedGroupToolIDs = append([]string{}, pinnedGroup.ToolIDs...)
	selectionEvent.DroppedGroups = droppedGroups
	selectionEvent.UsedFallbackGroups = false
	return toolSet.WithAllowedToolNames(exposedToolIDsForFiltering(exposedToolIDs)), selectionEvent
}

func kernelToolNamesForInstructionBundle(instructionBundle InstructionBundle) []string {
	if len(selectedSkillInstructionList(instructionBundle)) == 0 {
		return toolcontract.KernelToolNames()
	}
	toolNames := []string{}
	for _, toolName := range toolcontract.KernelToolNames() {
		if toolName != toolcontract.SkillSearchToolName {
			toolNames = append(toolNames, toolName)
		}
	}
	return toolNames
}

func outcomeContractEvidenceGroups(toolSet *toolcontract.ToolSet, outcomeContract OutcomeContract) (toolExposureGroup, toolExposureGroup) {
	requiredToolNames := appendUniqueStrings(outcomeContract.RequiredEvidenceTools)
	alternativeToolNames := []string{}
	for _, toolNameGroup := range outcomeContract.RequiredEvidenceAnyOf {
		availableGroup := filterGroupTools(toolSet, toolExposureGroup{ToolIDs: toolNameGroup})
		if len(availableGroup.ToolIDs) == 0 {
			continue
		}
		requiredToolNames = appendUniqueStrings(requiredToolNames, availableGroup.ToolIDs[0])
		alternativeToolNames = appendUniqueStrings(alternativeToolNames, availableGroup.ToolIDs[1:]...)
	}
	return filterGroupTools(toolSet, toolExposureGroup{Name: "required evidence", ToolIDs: requiredToolNames}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "evidence alternatives", ToolIDs: alternativeToolNames})
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
	skillToolNameLists := [][]string{}
	for _, skillInstruction := range selectedSkillInstructionList(instructionBundle) {
		skillToolNameLists = append(skillToolNameLists, SkillToolNames(skillInstruction))
	}
	return interleaveToolNameLists(skillToolNameLists)
}

func interleaveToolNameLists(toolNameLists [][]string) []string {
	toolNames := []string{}
	for depth := 0; ; depth++ {
		hasRemainingToolName := false
		for _, toolNameList := range toolNameLists {
			if depth >= len(toolNameList) {
				continue
			}
			hasRemainingToolName = true
			toolNames = appendUniqueStrings(toolNames, toolNameList[depth])
		}
		if !hasRemainingToolName {
			return toolNames
		}
	}
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

func droppedExposureToolNames(exposure ToolExposureEvent) []string {
	toolNames := []string{}
	for _, droppedGroup := range exposure.DroppedGroups {
		toolNames = appendUniqueStrings(toolNames, droppedGroup.ToolIDs...)
	}
	return toolNames
}

func toolSelectionSource(selectedSkillGroup toolExposureGroup, hasAuthoritativeWorkingSet bool) string {
	if hasAuthoritativeWorkingSet {
		return "contract_arbitration"
	}
	if len(selectedSkillGroup.ToolIDs) > 0 {
		return "selected_skills"
	}
	return "fixed_kernel"
}

func toolSelectionReason(selectedSkillGroup toolExposureGroup, hasAuthoritativeWorkingSet bool) string {
	if hasAuthoritativeWorkingSet {
		return "Blueclaw exposes the validated contract working set"
	}
	if len(selectedSkillGroup.ToolIDs) > 0 {
		return "Blueclaw exposes direct tools declared by the selected skills"
	}
	return "Blueclaw exposes the compact kernel tools"
}

func extensionToolGroups(groups []toolExposureGroup) []toolExposureGroup {
	extensionGroups := make([]toolExposureGroup, 0, len(groups))
	for _, group := range groups {
		toolIDs := []string{}
		for _, toolID := range group.ToolIDs {
			if !toolcontract.IsKernelToolName(toolID) {
				toolIDs = appendUniqueStrings(toolIDs, toolID)
			}
		}
		extensionGroups = append(extensionGroups, toolExposureGroup{Name: group.Name, ToolIDs: toolIDs})
	}
	return extensionGroups
}

func requestNeedsToolAccess(request AgentRequest, groups []toolExposureGroup) bool {
	if request.TaskShape != TaskShapeImmediateReply {
		return true
	}
	for _, group := range groups {
		if len(group.ToolIDs) > 0 {
			return true
		}
	}
	return false
}

func requiredInteractionToolNames(outcomeContract OutcomeContract, observations []turnObservation) []string {
	if expectedResultRequiresTool(outcomeContract, toolcontract.AskInputToolName) {
		return []string{toolcontract.AskInputToolName}
	}
	for _, toolName := range activeRecoveryToolNames(observations) {
		if toolName == toolcontract.AskInputToolName {
			return []string{toolcontract.AskInputToolName}
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

func toolSetForAgentTurn(toolSet *toolcontract.ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) *toolcontract.ToolSet {
	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract, ToolExposureEvent{})
	return filteredToolSet
}

func filterGroupTools(toolSet *toolcontract.ToolSet, group toolExposureGroup) toolExposureGroup {
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
	return strings.TrimSpace(toolID) != ""
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
			exhaustedToolNames["file_read"] = true
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
	return strings.TrimSpace(observation.Tool) == "file_read" &&
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

func firstPendingRequiredToolName(requiredNextToolNames []string, observations []turnObservation) string {
	nextToolIndex := 0
	requiredNextToolNames = appendUniqueStrings(requiredNextToolNames)
	for _, observation := range observations {
		if nextToolIndex >= len(requiredNextToolNames) {
			break
		}
		if observation.Failed() || strings.TrimSpace(observation.Tool) != requiredNextToolNames[nextToolIndex] {
			continue
		}
		nextToolIndex++
	}
	if nextToolIndex < len(requiredNextToolNames) {
		return requiredNextToolNames[nextToolIndex]
	}
	return ""
}
