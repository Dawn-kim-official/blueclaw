package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

const maxSchemaCallableToolCount = 8

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
	SelectionAttempted     bool               `json:"selectionAttempted,omitempty"`
	SelectionFailed        bool               `json:"selectionFailed,omitempty"`
	SelectionFailureReason string             `json:"selectionFailureReason,omitempty"`
	UsedFallbackGroups     bool               `json:"usedFallbackGroups"`
	ExposedToolIDs         []string           `json:"exposedToolIDs"`
	DroppedGroups          []droppedToolGroup `json:"droppedGroups,omitempty"`
}

type ToolSelectionDecision struct {
	SelectedToolIDs []string `json:"selectedToolIDs"`
	Reason          string   `json:"reason"`
}

type ToolSelectionRouter struct {
	languageModel llm.LanguageModelProvider
}

func NewToolSelectionRouter(languageModel llm.LanguageModelProvider) ToolSelectionRouter {
	return ToolSelectionRouter{languageModel: languageModel}
}

func (router ToolSelectionRouter) Select(ctx context.Context, request toolSelectionRequest) (ToolSelectionDecision, ToolExposureEvent) {
	event := ToolExposureEvent{}
	if router.languageModel == nil || len(request.CandidateGroups) == 0 {
		return ToolSelectionDecision{}, event
	}
	event.SelectionAttempted = true
	response, errorValue := router.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: router.buildMessages(request),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_tool_selection",
			Document:           `{"type":"object","properties":{"selectedToolIDs":{"type":"array","minItems":0,"maxItems":8,"items":{"type":"string"}},"reason":{"type":"string"}},"required":["selectedToolIDs","reason"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		event.SelectionFailed = true
		event.SelectionFailureReason = errorValue.Error()
		return ToolSelectionDecision{}, event
	}
	var decision ToolSelectionDecision
	if errorValue := json.Unmarshal([]byte(response.Content), &decision); errorValue != nil {
		event.SelectionFailed = true
		event.SelectionFailureReason = errorValue.Error()
		return ToolSelectionDecision{}, event
	}
	decision.SelectedToolIDs = stableUniqueToolIDs(decision.SelectedToolIDs)
	decision.Reason = strings.TrimSpace(decision.Reason)
	event.SelectedToolIDs = append([]string{}, decision.SelectedToolIDs...)
	event.SelectionReason = decision.Reason
	event.SelectionSource = "model"
	return decision, event
}

func (router ToolSelectionRouter) buildMessages(request toolSelectionRequest) []llm.Message {
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "Select optional tool IDs needed for the next Blueclaw action. Core groups are normally available and should not be selected. Return [] when unsure. Use only exact IDs from candidate optional tools. Do not answer the user.",
		},
		{
			Role:    "system",
			Content: renderCoreGroupSummary(request.CoreGroups),
		},
		{
			Role:    "system",
			Content: "Candidate optional tools:\n" + renderCompactToolCards(request.ToolSet, request.CandidateGroups),
		},
	}
	if description := activeGoalDescription(request.ActiveGoal); description != "" {
		messages = append(messages, llm.Message{Role: "system", Content: description})
	}
	if strings.TrimSpace(request.OutcomeSummary) != "" {
		messages = append(messages, llm.Message{Role: "system", Content: request.OutcomeSummary})
	}
	if strings.TrimSpace(request.RecentProgress) != "" {
		messages = append(messages, llm.Message{Role: "system", Content: request.RecentProgress})
	}
	if contextDescription := buildVisibleContextDescription(request.VisibleContext); contextDescription != "" {
		messages = append(messages, llm.Message{Role: "system", Content: contextDescription})
	}
	messages = append(messages, llm.Message{Role: "user", Content: request.Prompt})
	return messages
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

func shouldSelectOptionalToolsWithModel(request toolSelectionRequest) bool {
	for _, group := range request.CandidateGroups {
		if group.Name == "G7 generic candidates" {
			continue
		}
		if len(group.ToolIDs) > 0 {
			return true
		}
	}
	return false
}

func deterministicToolSelectionDecision(request toolSelectionRequest) (ToolSelectionDecision, ToolExposureEvent, bool) {
	if recoveryTools := firstGroupToolIDs(request.CandidateGroups, "G4 recovery/pinned candidates"); len(recoveryTools) > 0 {
		selection, event := deterministicToolSelection(recoveryTools, "pinned tools are required for the next step")
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
		SelectedToolIDs:    append([]string{}, selection.SelectedToolIDs...),
		SelectionReason:    reason,
		SelectionSource:    "deterministic",
		SelectionAttempted: false,
	}
	return selection, event
}

func toolSelectionFallbackFitsCap(request toolSelectionRequest) bool {
	groups := fallbackToolExposureGroups(request.CoreGroups, request.CandidateGroups)
	_, droppedGroups := applyGroupCap(groups, maxSchemaCallableToolCount)
	return len(droppedGroups) == 0
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
		filterGroupTools(toolSet, toolExposureGroup{Name: "G1 control-core", ToolIDs: []string{"skill.search", "ask.confirm"}}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "G2 interaction-core", ToolIDs: []string{"ask.choice", "ask.input", "memory.search"}}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "G3 memory-context-core", ToolIDs: []string{"conversation.history", "memory.remember", "tool.describe"}}),
	}
}

func collectOptionalCandidateGroups(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, observations []turnObservation) []toolExposureGroup {
	selectedSkillToolNames := selectedAndPinnedSkillToolNameSet(instructionBundle, request.PinnedSkillNames)
	return []toolExposureGroup{
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G4 recovery/pinned candidates", ToolIDs: recoveryPinnedToolNames(instructionBundle, request, observations)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G5 selected-skill candidates", ToolIDs: selectedAndPinnedSkillToolNames(instructionBundle, request.PinnedSkillNames)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G6 active-goal candidates", ToolIDs: activeGoalCandidateToolNames(request, executionPlan, hasExecutionPlan, outcomeContract)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G7 generic candidates", ToolIDs: genericCandidateToolNames(toolSet, len(selectedSkillToolNames) > 0)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
	}
}

func genericCandidateToolNames(toolSet *ToolSet, hasSelectedSkill bool) []string {
	if hasSelectedSkill {
		return genericBuiltInToolNames()
	}
	toolNames := nonGenericRegisteredToolNames(toolSet)
	toolNames = appendUniqueStrings(toolNames, genericBuiltInToolNames()...)
	return toolNames
}

func nonGenericRegisteredToolNames(toolSet *ToolSet) []string {
	toolNames := []string{}
	if toolSet != nil {
		genericToolNameSet := stringSet(genericBuiltInToolNames())
		genericToolNameSet = mergeStringSet(genericToolNameSet, stringSet(coreAgentToolNames()))
		for _, toolName := range toolSet.ListToolNames() {
			if !genericToolNameSet[toolName] {
				toolNames = appendUniqueStrings(toolNames, toolName)
			}
		}
	}
	return toolNames
}

func mergeStringSet(left map[string]bool, right map[string]bool) map[string]bool {
	for value := range right {
		left[value] = true
	}
	return left
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

func exposedToolIDsForFiltering(exposedToolIDs []string) []string {
	if len(exposedToolIDs) > 0 {
		return exposedToolIDs
	}
	return []string{"__blueclaw_no_callable_tools__"}
}

func fallbackToolExposureGroups(coreGroups []toolExposureGroup, candidateGroups []toolExposureGroup) []toolExposureGroup {
	orderedGroups := []toolExposureGroup{}
	orderedGroups = append(orderedGroups, candidateGroupsWithName(candidateGroups, "G4 recovery/pinned candidates")...)
	orderedGroups = append(orderedGroups, candidateGroupsWithName(candidateGroups, "G5 selected-skill candidates")...)
	orderedGroups = append(orderedGroups, candidateGroupsWithName(candidateGroups, "G6 active-goal candidates")...)
	orderedGroups = append(orderedGroups, coreGroups...)
	orderedGroups = append(orderedGroups, candidateGroupsWithName(candidateGroups, "G7 generic candidates")...)
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
		if trimmedToolID != "" && toolSet != nil && toolSet.CanExpose(trimmedToolID) {
			filteredToolIDs = appendUniqueStrings(filteredToolIDs, trimmedToolID)
		}
	}
	return toolExposureGroup{Name: group.Name, ToolIDs: filteredToolIDs}
}

func filterGroupToolsForTurn(toolSet *ToolSet, group toolExposureGroup, selectedSkillToolNames map[string]bool, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) toolExposureGroup {
	filteredToolIDs := []string{}
	for _, toolID := range group.ToolIDs {
		trimmedToolID := strings.TrimSpace(toolID)
		if trimmedToolID == "" || toolSet == nil || !toolSet.CanExpose(trimmedToolID) {
			continue
		}
		if selectedSkillToolNames[trimmedToolID] {
			if selectedSkillToolShouldExpose(trimmedToolID, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract) {
				filteredToolIDs = appendUniqueStrings(filteredToolIDs, trimmedToolID)
			}
			continue
		}
		if shouldExposeToolForOutcome(trimmedToolID, request, executionPlan, hasExecutionPlan, outcomeContract) {
			filteredToolIDs = appendUniqueStrings(filteredToolIDs, trimmedToolID)
		}
	}
	return toolExposureGroup{Name: group.Name, ToolIDs: filteredToolIDs}
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

func selectedAndPinnedSkillToolNames(instructionBundle InstructionBundle, pinnedSkillNames []string) []string {
	toolNames := []string{}
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	pinnedSkillName := stringSet(pinnedSkillNames)
	for _, skillInstruction := range instructionBundle.Skills {
		if !selectedSkillName[skillInstruction.Name] && !pinnedSkillName[skillInstruction.Name] {
			continue
		}
		toolNames = appendUniqueStrings(toolNames, SkillToolNames(skillInstruction)...)
	}
	return toolNames
}

func activeGoalCandidateToolNames(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) []string {
	toolNames := append([]string{}, outcomeContractToolNames(outcomeContract)...)
	toolNames = appendUniqueStrings(toolNames, outcomeContractToolNames(request.ActiveGoal.OutcomeContract)...)
	if requestLooksLikeCalendarWork(request) {
		toolNames = appendUniqueStrings(toolNames, "calendar.event.add", "calendar.event.delete")
	}
	return toolNames
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
	if !activeGoalOutcomeContractHasRequirements(contract) {
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
			toolNames = appendUniqueStrings(toolNames, "document.read")
		}
	}
	return toolNames
}

func allVisibleContextMaterials(visibleContext VisibleContext) []VisibleContextMaterial {
	materials := append([]VisibleContextMaterial{}, visibleContext.Materials...)
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
