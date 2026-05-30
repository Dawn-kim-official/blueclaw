package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
)

const maxSchemaToolCount = 20

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
	ToolSet         *ToolSet
	CoreGroups      []toolExposureGroup
	CandidateGroups []toolExposureGroup
}

func buildToolSelectionRequest(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) toolSelectionRequest {
	coreGroups := collectCoreGroups(toolSet)
	candidateGroups := collectOptionalCandidateGroups(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract)
	return toolSelectionRequest{
		Prompt:          request.Prompt,
		VisibleContext:  request.VisibleContext,
		ActiveGoal:      activeGoalForTurn(request, outcomeContract, executionPlan, hasExecutionPlan),
		OutcomeSummary:  outcomeContractSummary(outcomeContract),
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

func collectCoreGroups(toolSet *ToolSet) []toolExposureGroup {
	return []toolExposureGroup{
		filterGroupTools(toolSet, toolExposureGroup{Name: "G1 control-core", ToolIDs: []string{"skill.search", "tool.describe", "ask.confirm"}}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "G2 interaction-core", ToolIDs: []string{"ask.choice", "ask.input", "memory.search"}}),
		filterGroupTools(toolSet, toolExposureGroup{Name: "G3 memory-context-core", ToolIDs: []string{"conversation.history", "memory.remember"}}),
	}
}

func collectOptionalCandidateGroups(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) []toolExposureGroup {
	selectedSkillToolNames := selectedAndPinnedSkillToolNameSet(instructionBundle, request.PinnedSkillNames)
	return []toolExposureGroup{
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G4 recovery/pinned candidates", ToolIDs: recoveryPinnedToolNames(instructionBundle, request)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G5 selected-skill candidates", ToolIDs: selectedAndPinnedSkillToolNames(instructionBundle, request.PinnedSkillNames)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G6 active-goal candidates", ToolIDs: activeGoalCandidateToolNames(request, executionPlan, hasExecutionPlan, outcomeContract)}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
		filterGroupToolsForTurn(toolSet, toolExposureGroup{Name: "G7 generic candidates", ToolIDs: genericBuiltInToolNames()}, selectedSkillToolNames, request, executionPlan, hasExecutionPlan, outcomeContract),
	}
}

func toolSetForAgentTurnWithExposure(toolSet *ToolSet, instructionBundle InstructionBundle, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract, selection ToolSelectionDecision, selectionEvent ToolExposureEvent) (*ToolSet, ToolExposureEvent) {
	if toolSet == nil {
		return nil, selectionEvent
	}
	coreGroups := collectCoreGroups(toolSet)
	candidateGroups := collectOptionalCandidateGroups(toolSet, instructionBundle, request, executionPlan, hasExecutionPlan, outcomeContract)
	selectedGroup := selectedOptionalGroup(selection.SelectedToolIDs, candidateGroups)
	selectionEvent.ValidSelectedToolIDs = append([]string{}, selectedGroup.ToolIDs...)

	groups := []toolExposureGroup{}
	if len(selectedGroup.ToolIDs) > 0 {
		groups = append(groups, selectedGroup)
		groups = append(groups, coreGroups...)
	} else {
		selectionEvent.UsedFallbackGroups = true
		groups = append(groups, coreGroups...)
		groups = append(groups, candidateGroups...)
	}

	exposedToolIDs, droppedGroups := applyGroupCap(groups, maxSchemaToolCount)
	selectionEvent.ExposedToolIDs = append([]string{}, exposedToolIDs...)
	selectionEvent.DroppedGroups = droppedGroups
	return toolSet.WithAllowedToolNames(exposedToolIDs), selectionEvent
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

func recoveryPinnedToolNames(instructionBundle InstructionBundle, request AgentRequest) []string {
	toolNames := append([]string{}, request.PinnedToolNames...)
	toolNames = appendUniqueStrings(toolNames, pinnedSkillToolNames(instructionBundle, request.PinnedSkillNames)...)
	toolNames = appendUniqueStrings(toolNames, outcomeContractRequiredToolNames(request.ActiveGoal.OutcomeContract)...)
	return toolNames
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
	toolNames := append([]string{}, outcomeContractRequiredToolNames(outcomeContract)...)
	toolNames = appendUniqueStrings(toolNames, outcomeContractRequiredToolNames(request.ActiveGoal.OutcomeContract)...)
	if outcomeNeedsArtifactWorkflow(outcomeContract) || outcomeNeedsArtifactWorkflow(request.ActiveGoal.OutcomeContract) {
		toolNames = appendUniqueStrings(toolNames, artifactWorkflowToolNames()...)
	}
	if outcomeAllowsSiteTools(request, executionPlan, hasExecutionPlan, outcomeContract) {
		toolNames = appendUniqueStrings(toolNames, "site.app.status", "site.app.create", "site.app.build", "site.app.repair", "site.app.publish", "site.app.history", "site.app.diff", "site.app.rollback")
	}
	return toolNames
}

func renderCoreGroupSummary(groups []toolExposureGroup) string {
	lines := []string{"Core groups are normally available in the action schema unless the 20-tool cap is reached:"}
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
	sideEffect := firstNonEmptyString(strings.TrimSpace(toolDefinition.SideEffectClass), "read")
	description := firstNonEmptyString(specificToolDescription(toolID), strings.TrimSpace(toolDefinition.Description), "No description.")
	description = compactToolCardText(description)
	return "- " + toolID + ": " + sideEffect + "; " + description + " Use when needed for the selected skill or active goal. Avoid guessing inputs."
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
