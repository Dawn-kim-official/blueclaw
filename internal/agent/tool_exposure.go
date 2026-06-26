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
	DroppedGroups          []droppedToolGroup `json:"droppedGroups,omitempty"`
}

type ToolSelectionDecision struct {
	SelectedToolIDs []string `json:"selectedToolIDs"`
	Reason          string   `json:"reason"`
}

type toolSelectionRequest struct {
	Prompt          string
	VisibleContext  VisibleContext
	ActiveGoal      ActiveGoal
	OutcomeSummary  string
	RecentProgress  string
	ToolSet         *ToolSet
	CoreGroups      []toolExposureGroup
	CandidateGroups []toolExposureGroup
}

func buildToolSelectionRequest(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, observations ...[]turnObservation) toolSelectionRequest {
	recentObservations := []turnObservation{}
	if len(observations) > 0 {
		recentObservations = observations[0]
	}
	coreGroups := collectCoreGroups(toolSet)
	candidateGroups := collectOptionalCandidateGroups(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract, recentObservations)
	return toolSelectionRequest{
		Prompt:          request.Prompt,
		VisibleContext:  request.VisibleContext,
		ActiveGoal:      activeGoalForTurn(request, outcomeContract, executionPlan, hasExecutionPlan),
		OutcomeSummary:  outcomeContractSummary(outcomeContract),
		RecentProgress:  toolSelectionRecentProgress(recentObservations),
		ToolSet:         toolSet,
		CoreGroups:      coreGroups,
		CandidateGroups: candidateGroups,
	}
}

func deterministicToolSelectionDecision(request toolSelectionRequest) (ToolSelectionDecision, ToolExposureEvent, bool) {
	if recoveryTools := firstGroupToolIDs(request.CandidateGroups, "G4 recovery/pinned candidates"); len(recoveryTools) > 0 {
		selection, event := deterministicToolSelection(recoveryTools, "pinned or recovery tools are needed for the next step")
		return selection, event, true
	}
	if materialTools := visibleContextMaterialReadToolNames(request.VisibleContext); len(materialTools) > 0 {
		selection, event := deterministicToolSelection(materialTools, "visible attachment materials can be read by materialID")
		return selection, event, true
	}
	return ToolSelectionDecision{}, ToolExposureEvent{}, false
}

func deterministicToolSelection(toolIDs []string, reason string) (ToolSelectionDecision, ToolExposureEvent) {
	selection := ToolSelectionDecision{
		SelectedToolIDs: stableUniqueToolIDs(toolIDs),
		Reason:          reason,
	}
	event := ToolExposureEvent{
		SelectedToolIDs: append([]string{}, selection.SelectedToolIDs...),
		SelectionReason: reason,
		SelectionSource: "deterministic",
	}
	return selection, event
}

func firstGroupToolIDs(groups []toolExposureGroup, groupName string) []string {
	for _, group := range groups {
		if group.Name == groupName {
			return append([]string{}, group.ToolIDs...)
		}
	}
	return nil
}

func collectCoreGroups(toolSet *ToolSet) []toolExposureGroup {
	return []toolExposureGroup{
		filterGroupTools(toolSet, toolExposureGroup{Name: "G1 control-core", ToolIDs: []string{"skill.search"}}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "G2 interaction-core", ToolIDs: []string{"ask.choice", "ask.input", "memory.search"}}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "G3 memory-context-core", ToolIDs: []string{"conversation.history", "memory.remember"}}),
	}
}

func collectOptionalCandidateGroups(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, observations []turnObservation) []toolExposureGroup {
	return []toolExposureGroup{
		filterGroupTools(toolSet, toolExposureGroup{Name: "G4 recovery/pinned candidates", ToolIDs: recoveryPinnedToolNames(instructionBundle, request, observations)}),
	}
}

func toolSetForAgentTurnWithExposure(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, selection ToolSelectionDecision, selectionEvent ToolExposureEvent, observations ...[]turnObservation) (*ToolSet, ToolExposureEvent) {
	if toolSet == nil {
		return nil, selectionEvent
	}
	recentObservations := []turnObservation{}
	if len(observations) > 0 {
		recentObservations = observations[0]
	}
	coreGroups := collectCoreGroups(toolSet)
	candidateGroups := collectOptionalCandidateGroups(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract, recentObservations)
	selectedGroup := selectedOptionalGroup(selection.SelectedToolIDs, candidateGroups)
	if len(selectedGroup.ToolIDs) == 0 {
		selectedGroup = selectedRegisteredToolGroup(toolSet, selection.SelectedToolIDs)
	}
	selectionEvent.ValidSelectedToolIDs = append([]string{}, selectedGroup.ToolIDs...)

	groups := []toolExposureGroup{}
	if len(selectedGroup.ToolIDs) > 0 {
		groups = append(groups, selectedGroup)
		if selectionEvent.SelectionSource != "deterministic" {
			groups = append(groups, coreGroups...)
		}
	} else {
		selectionEvent.UsedFallbackGroups = true
		groups = fallbackToolExposureGroups(coreGroups, candidateGroups)
	}

	exposedToolIDs, droppedGroups := applyGroupCap(groups, maxSchemaCallableToolCount)
	selectionEvent.ExposedToolIDs = append([]string{}, exposedToolIDs...)
	selectionEvent.DroppedGroups = droppedGroups
	return toolSet.WithAllowedToolNames(exposedToolIDsForFiltering(exposedToolIDs)), selectionEvent
}

func selectedRegisteredToolGroup(toolSet *ToolSet, selectedToolIDs []string) toolExposureGroup {
	if toolSet == nil || len(selectedToolIDs) == 0 {
		return toolExposureGroup{}
	}
	return filterGroupTools(toolSet, toolExposureGroup{Name: "G0 selected tools", ToolIDs: stableUniqueToolIDs(selectedToolIDs)})
}

func exposedToolIDsForFiltering(exposedToolIDs []string) []string {
	if len(exposedToolIDs) > 0 {
		return exposedToolIDs
	}
	return []string{"__blueclaw_no_callable_tools__"}
}

func fallbackToolExposureGroups(coreGroups []toolExposureGroup, candidateGroups []toolExposureGroup) []toolExposureGroup {
	orderedGroups := []toolExposureGroup{}
	orderedGroups = append(orderedGroups, candidateGroupsWithName(candidateGroups, "G4 recovery/pinned candidates")...)
	orderedGroups = append(orderedGroups, coreGroups...)
	return orderedGroups
}

func candidateGroupsWithName(groups []toolExposureGroup, name string) []toolExposureGroup {
	matchingGroups := []toolExposureGroup{}
	for _, group := range groups {
		if group.Name == name {
			matchingGroups = append(matchingGroups, group)
		}
	}
	return matchingGroups
}

func toolSetForAgentTurn(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, selections ...ToolSelectionDecision) *ToolSet {
	selection := ToolSelectionDecision{}
	if len(selections) > 0 {
		selection = selections[0]
	}
	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract, selection, ToolExposureEvent{})
	return filteredToolSet
}

func applyGroupCap(groups []toolExposureGroup, limit int) ([]string, []droppedToolGroup) {
	exposedToolIDs := []string{}
	droppedGroups := []droppedToolGroup{}
	for _, group := range groups {
		groupToolIDs := stableUniqueToolIDs(group.ToolIDs)
		if len(groupToolIDs) == 0 {
			continue
		}
		remaining := limit - len(exposedToolIDs)
		if remaining <= 0 {
			droppedGroups = append(droppedGroups, droppedToolGroup{Name: group.Name, ToolIDs: groupToolIDs})
			continue
		}
		if len(groupToolIDs) <= remaining {
			exposedToolIDs = appendUniqueStrings(exposedToolIDs, groupToolIDs...)
			continue
		}
		exposedToolIDs = appendUniqueStrings(exposedToolIDs, groupToolIDs[:remaining]...)
		droppedGroups = append(droppedGroups, droppedToolGroup{Name: group.Name, ToolIDs: groupToolIDs[remaining:], IsPartial: true})
	}
	return exposedToolIDs, droppedGroups
}

func selectedOptionalGroup(selectedToolIDs []string, candidateGroups []toolExposureGroup) toolExposureGroup {
	candidateToolIDByID := map[string]bool{}
	for _, group := range candidateGroups {
		for _, toolID := range group.ToolIDs {
			candidateToolIDByID[strings.TrimSpace(toolID)] = true
		}
	}
	validToolIDs := []string{}
	for _, toolID := range selectedToolIDs {
		trimmedToolID := strings.TrimSpace(toolID)
		if candidateToolIDByID[trimmedToolID] {
			validToolIDs = appendUniqueStrings(validToolIDs, trimmedToolID)
		}
	}
	return toolExposureGroup{Name: "G0 selected", ToolIDs: validToolIDs}
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
	return strings.TrimSpace(toolID) != "ask.confirm"
}

func recoveryPinnedToolNames(instructionBundle InstructionBundle, request AgentRequest, observations []turnObservation) []string {
	toolNames := filterExhaustedRecoveryToolNames(request.PinnedToolNames, observations)
	toolNames = appendUniqueStrings(toolNames, activeRecoveryToolNames(observations)...)
	return toolNames
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
		strings.Contains(strings.ToLower(observation.ContentText()), "recovery budget")
}

func toolSelectionRecentProgress(observations []turnObservation) string {
	recentObservations := recentProgressObservations(observations)
	if len(recentObservations) == 0 {
		return ""
	}
	document, errorValue := json.Marshal(recentObservations)
	if errorValue != nil {
		return ""
	}
	return "Recent observation progress: " + string(document)
}

func renderCoreGroupSummary(groups []toolExposureGroup) string {
	lines := []string{"Core groups are normally available in the action schema unless the provider function budget is reached:"}
	for _, group := range groups {
		if len(group.ToolIDs) == 0 {
			continue
		}
		lines = append(lines, group.Name+": "+strings.Join(group.ToolIDs, ", "))
	}
	return strings.Join(lines, "\n")
}

func renderCompactToolCards(toolSet *ToolSet, groups []toolExposureGroup) string {
	lines := []string{}
	for _, group := range groups {
		for _, toolID := range group.ToolIDs {
			toolDefinition, isFound := toolSet.ToolDefinition(toolID)
			if !isFound {
				continue
			}
			lines = append(lines, compactToolCard(toolID, toolDefinition))
		}
	}
	if len(lines) == 0 {
		return "(none)"
	}
	return strings.Join(lines, "\n")
}

func compactToolCard(toolID string, toolDefinition ToolDefinition) string {
	card := recoveryCardForTool(toolDefinition)
	return "- " + toolID + ": " +
		"sideEffect=" + compactToolCardText(card.SideEffect) + "; " +
		"does=" + compactToolCardText(card.Does) + "; " +
		"produces=" + compactToolCardText(card.Produces) + "; " +
		"useWhen=" + compactToolCardText(card.UseWhen) + "; " +
		"avoidWhen=" + compactToolCardText(card.AvoidWhen) + "."
}

func recoveryCardForTool(toolDefinition ToolDefinition) ToolRecoveryCard {
	card := toolDefinition.RecoveryCard
	card.SideEffect = firstNonEmptyString(strings.TrimSpace(card.SideEffect), strings.TrimSpace(toolDefinition.SideEffectClass), "read")
	card.Does = firstNonEmptyString(strings.TrimSpace(card.Does), strings.TrimSpace(toolDefinition.Description), "Run this tool.")
	card.Produces = firstNonEmptyString(strings.TrimSpace(card.Produces), "A tool observation.")
	card.UseWhen = firstNonEmptyString(strings.TrimSpace(card.UseWhen), "The expected result, step plan, or recovery context calls for this capability.")
	card.AvoidWhen = firstNonEmptyString(strings.TrimSpace(card.AvoidWhen), "Required inputs are unknown or the task contract points to a different deliverable.")
	return card
}

func compactToolCardText(value string) string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	text := strings.Join(words, " ")
	if len(text) <= 180 {
		return text
	}
	return strings.TrimSpace(text[:180])
}

func outcomeContractSummary(contract OutcomeContract) string {
	if !OutcomeContractHasRequirements(contract) {
		return ""
	}
	document, errorValue := json.Marshal(contract)
	if errorValue != nil {
		return ""
	}
	return "Outcome contract: " + string(document)
}

func stableUniqueToolIDs(toolIDs []string) []string {
	result := []string{}
	return appendUniqueStrings(result, toolIDs...)
}

func visibleContextMaterialReadToolNames(visibleContext VisibleContext) []string {
	toolNames := []string{}
	for _, material := range allVisibleContextMaterials(visibleContext) {
		if visibleContextMaterialLooksLikeImage(material) {
			toolNames = appendUniqueStrings(toolNames, "image.read")
			continue
		}
		if visibleContextMaterialLooksReadableDocument(material) {
			toolNames = appendUniqueStrings(toolNames, "file.preview")
		}
	}
	return toolNames
}

func allVisibleContextMaterials(visibleContext VisibleContext) []VisibleContextMaterial {
	materials := append([]VisibleContextMaterial{}, visibleContext.CurrentMaterials...)
	materials = append(materials, visibleContext.Materials...)
	for _, message := range visibleContext.Messages {
		materials = append(materials, message.Materials...)
	}
	return materials
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

func visibleContextMaterialLooksReadableDocument(material VisibleContextMaterial) bool {
	if visibleContextMaterialLooksLikeImage(material) {
		return false
	}
	return strings.TrimSpace(material.Filename) != "" ||
		strings.TrimSpace(material.ContentType) != "" ||
		strings.TrimSpace(material.Path) != "" ||
		strings.TrimSpace(material.MaterialID) != ""
}
