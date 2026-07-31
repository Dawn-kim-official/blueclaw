package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

const taskContextSummaryEventName = "agent.context_summary"
const defaultCompactionTriggerTokens = 96000
const taskContextCompactionRecentObservationCount = 10
const taskContextCompactionMinimumNewObservations = 6
const taskContextCompactionMinimumNewCharacters = 20000
const taskContextSummaryMaxTokens = 1800

type TaskContextSummary struct {
	ObservationID                 string   `json:"observationID,omitempty"`
	CompactedThroughObservationID string   `json:"compactedThroughObservationID,omitempty"`
	CompactedObservationIDs       []string `json:"compactedObservationIDs,omitempty"`
	Goal                          string   `json:"goal,omitempty"`
	CompletedSteps                []string `json:"completedSteps,omitempty"`
	Artifacts                     []string `json:"artifacts,omitempty"`
	KeyDecisions                  []string `json:"keyDecisions,omitempty"`
	ExhaustedRecoveryRoutes       []string `json:"exhaustedRecoveryRoutes,omitempty"`
	ActiveFailureDebt             []string `json:"activeFailureDebt,omitempty"`
	NextPlan                      []string `json:"nextPlan,omitempty"`
}

func taskContextSummaryFromTaskEvents(events []task.TaskEvent) TaskContextSummary {
	for index := len(events) - 1; index >= 0; index-- {
		if strings.TrimSpace(events[index].Name) != taskContextSummaryEventName {
			continue
		}
		var summary TaskContextSummary
		if json.Unmarshal([]byte(events[index].Body), &summary) == nil {
			return normalizeTaskContextSummary(summary)
		}
	}
	return TaskContextSummary{}
}

func (agentTurnRunner *AgentTurnRunner) promptVisibleObservationsForAction(ctx context.Context, taskRunID string, state agentTaskState) []turnObservation {
	taskEvents := agentTurnRunner.taskRunService.ListTaskEvent(taskRunID)
	currentSummary := latestTaskContextSummary(state.ContextSummary, taskEvents)
	pinnedObservationIDs := pinnedPromptObservationIDs(state.Observations, taskEvents)
	promptObservations := promptVisibleObservations(state.Observations, currentSummary, pinnedObservationIDs)
	estimatedTokenCount := estimatePromptTokenCount(BuildAgentActionRequest(withPromptObservations(state, promptObservations)).Messages)
	if estimatedTokenCount <= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) {
		return promptObservations
	}
	plan, shouldCompact := buildTaskContextCompactionPlan(state.Observations, currentSummary, pinnedObservationIDs)
	if !shouldCompact {
		return promptObservations
	}
	summary, ok := agentTurnRunner.generateTaskContextSummary(ctx, state.Request, currentSummary, plan.CompactableObservations)
	if !ok {
		return promptObservations
	}
	summary.ObservationID = "context-summary-" + plan.CompactedThroughObservationID
	summary.CompactedThroughObservationID = plan.CompactedThroughObservationID
	summary.CompactedObservationIDs = append([]string{}, plan.CompactedObservationIDs...)
	summary = normalizeTaskContextSummary(summary)
	agentTurnRunner.appendEvent(taskRunID, taskContextSummaryEventName, marshalEventBody(summary))
	return promptVisibleObservations(state.Observations, summary, pinnedObservationIDs)
}

func latestTaskContextSummary(fallback TaskContextSummary, events []task.TaskEvent) TaskContextSummary {
	summary := taskContextSummaryFromTaskEvents(events)
	if strings.TrimSpace(summary.ObservationID) != "" {
		return summary
	}
	return fallback
}

func withPromptObservations(state agentTaskState, observations []turnObservation) agentTaskState {
	state.Observations = append([]turnObservation{}, observations...)
	return state
}

type taskContextCompactionPlan struct {
	CompactableObservations       []turnObservation
	CompactedObservationIDs       []string
	CompactedThroughObservationID string
}

func buildTaskContextCompactionPlan(observations []turnObservation, summary TaskContextSummary, pinnedObservationIDs map[string]bool) (taskContextCompactionPlan, bool) {
	if !hasEnoughNewObservationsForCompaction(observations, summary.CompactedThroughObservationID) {
		return taskContextCompactionPlan{}, false
	}
	cutoffIndex := len(observations) - taskContextCompactionRecentObservationCount
	if cutoffIndex <= 0 {
		return taskContextCompactionPlan{}, false
	}
	plan := taskContextCompactionPlan{}
	for _, observation := range observations[:cutoffIndex] {
		if pinnedObservationIDs[strings.TrimSpace(observation.ObservationID)] {
			continue
		}
		plan.CompactableObservations = append(plan.CompactableObservations, observation)
		plan.CompactedObservationIDs = append(plan.CompactedObservationIDs, observation.ObservationID)
		plan.CompactedThroughObservationID = observation.ObservationID
	}
	return plan, len(plan.CompactableObservations) > 0
}

func hasEnoughNewObservationsForCompaction(observations []turnObservation, compactedThroughObservationID string) bool {
	newObservations := observationsAfterObservationID(observations, compactedThroughObservationID)
	if len(newObservations) >= taskContextCompactionMinimumNewObservations {
		return true
	}
	return observationsCharacterCount(newObservations) >= taskContextCompactionMinimumNewCharacters
}

func observationsAfterObservationID(observations []turnObservation, observationID string) []turnObservation {
	trimmedObservationID := strings.TrimSpace(observationID)
	if trimmedObservationID == "" {
		return observations
	}
	for index, observation := range observations {
		if strings.TrimSpace(observation.ObservationID) == trimmedObservationID {
			return observations[index+1:]
		}
	}
	return observations
}

func observationsCharacterCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		count += len(observation.ContentText()) + len(observation.Summary)
	}
	return count
}

func promptVisibleObservations(observations []turnObservation, summary TaskContextSummary, pinnedObservationIDs map[string]bool) []turnObservation {
	if strings.TrimSpace(summary.ObservationID) == "" || len(summary.CompactedObservationIDs) == 0 {
		return append([]turnObservation{}, observations...)
	}
	compactedObservationIDs := stringSet(summary.CompactedObservationIDs)
	promptObservations := []turnObservation{summaryObservation(summary)}
	for _, observation := range observations {
		observationID := strings.TrimSpace(observation.ObservationID)
		if compactedObservationIDs[observationID] && !pinnedObservationIDs[observationID] {
			continue
		}
		promptObservations = append(promptObservations, observation)
	}
	return promptObservations
}

func summaryObservation(summary TaskContextSummary) turnObservation {
	content := marshalEventBody(normalizeTaskContextSummary(summary))
	return turnObservation{
		ObservationID: strings.TrimSpace(summary.ObservationID),
		Action:        "context_summary",
		Output:        ToolOutput{Content: content},
		Summary:       "Compacted task context through " + strings.TrimSpace(summary.CompactedThroughObservationID) + ": " + content,
	}
}

func pinnedPromptObservationIDs(observations []turnObservation, events []task.TaskEvent) map[string]bool {
	pinnedObservationIDs := completionEvidenceObservationIDs(events)
	pinActiveFailureDebtObservations(pinnedObservationIDs, observations)
	return pinnedObservationIDs
}

func pinActiveFailureDebtObservations(pinnedObservationIDs map[string]bool, observations []turnObservation) {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return
	}
	for index, observation := range observations {
		if strings.TrimSpace(observation.ObservationID) != strings.TrimSpace(failureDebt.LatestFailure.ObservationID) {
			continue
		}
		for _, activeObservation := range observations[index:] {
			pinnedObservationIDs[strings.TrimSpace(activeObservation.ObservationID)] = true
		}
		return
	}
}

func completionEvidenceObservationIDs(events []task.TaskEvent) map[string]bool {
	observationIDs := map[string]bool{}
	for _, event := range events {
		if strings.TrimSpace(event.Name) != "agent.action" {
			continue
		}
		var actionDocument turnActionDocument
		if json.Unmarshal([]byte(event.Body), &actionDocument) != nil {
			continue
		}
		actionDocument = normalizeParsedEvidence(actionDocument)
		for _, reference := range actionDocument.CompletionEvidence {
			if observationID := strings.TrimSpace(reference.ObservationID); observationID != "" {
				observationIDs[observationID] = true
			}
		}
		for _, reviewItem := range actionDocument.QualityReview {
			for _, reference := range reviewItem.Evidence {
				if observationID := strings.TrimSpace(reference.ObservationID); observationID != "" {
					observationIDs[observationID] = true
				}
			}
		}
	}
	return observationIDs
}

func (agentTurnRunner *AgentTurnRunner) generateTaskContextSummary(ctx context.Context, request AgentTurnRequest, currentSummary TaskContextSummary, observations []turnObservation) (TaskContextSummary, bool) {
	maxTokens := taskContextSummaryMaxTokens
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{{
			Role:    "system",
			Content: taskContextSummaryInstruction(),
		}, {
			Role:    "user",
			Content: taskContextSummaryInput(request, currentSummary, observations),
		}},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_task_context_summary",
			Document:           taskContextSummarySchema(),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: llm.GenerationOptions{MaxTokens: &maxTokens},
	})
	if errorValue != nil {
		return TaskContextSummary{}, false
	}
	var summary TaskContextSummary
	if json.Unmarshal([]byte(structuredResponse.Content), &summary) != nil {
		return TaskContextSummary{}, false
	}
	return normalizeTaskContextSummary(summary), true
}

func taskContextSummaryInstruction() string {
	return strings.Join([]string{
		"Summarize old task observations into a rolling TaskContextSummary JSON object.",
		"Preserve exact operational state needed for the next step.",
		"never invent IDs/paths/URLs, copy them exactly from observations",
		"Use empty arrays for fields with no supported facts.",
	}, "\n")
}

func taskContextSummaryInput(request AgentTurnRequest, currentSummary TaskContextSummary, observations []turnObservation) string {
	return marshalEventBody(map[string]any{
		"goal":            strings.TrimSpace(request.Prompt),
		"currentSummary":  normalizeTaskContextSummary(currentSummary),
		"observations":    observations,
		"responseFields":  []string{"goal", "completedSteps", "artifacts", "keyDecisions", "exhaustedRecoveryRoutes", "activeFailureDebt", "nextPlan"},
		"copyExactValues": []string{"observationID", "siteID", "URL", "path"},
	})
}

func taskContextSummarySchema() string {
	return `{"type":"object","properties":{"goal":{"type":"string"},"completedSteps":{"type":"array","items":{"type":"string"}},"artifacts":{"type":"array","items":{"type":"string"}},"keyDecisions":{"type":"array","items":{"type":"string"}},"exhaustedRecoveryRoutes":{"type":"array","items":{"type":"string"}},"activeFailureDebt":{"type":"array","items":{"type":"string"}},"nextPlan":{"type":"array","items":{"type":"string"}}},"required":["goal","completedSteps","artifacts","keyDecisions","exhaustedRecoveryRoutes","activeFailureDebt","nextPlan"],"additionalProperties":false}`
}

func normalizeTaskContextSummary(summary TaskContextSummary) TaskContextSummary {
	return TaskContextSummary{
		ObservationID:                 strings.TrimSpace(summary.ObservationID),
		CompactedThroughObservationID: strings.TrimSpace(summary.CompactedThroughObservationID),
		CompactedObservationIDs:       normalizeTaskContextSummaryList(summary.CompactedObservationIDs, 64),
		Goal:                          truncateText(compactWhitespace(summary.Goal), 500),
		CompletedSteps:                normalizeTaskContextSummaryList(summary.CompletedSteps, 24),
		Artifacts:                     normalizeTaskContextSummaryList(summary.Artifacts, 24),
		KeyDecisions:                  normalizeTaskContextSummaryList(summary.KeyDecisions, 24),
		ExhaustedRecoveryRoutes:       normalizeTaskContextSummaryList(summary.ExhaustedRecoveryRoutes, 16),
		ActiveFailureDebt:             normalizeTaskContextSummaryList(summary.ActiveFailureDebt, 16),
		NextPlan:                      normalizeTaskContextSummaryList(summary.NextPlan, 16),
	}
}

func normalizeTaskContextSummaryList(values []string, limit int) []string {
	normalizedValues := []string{}
	seenValues := map[string]bool{}
	for _, value := range values {
		trimmedValue := truncateText(compactWhitespace(value), 500)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		normalizedValues = append(normalizedValues, trimmedValue)
		if len(normalizedValues) >= limit {
			break
		}
	}
	return normalizedValues
}

func estimatePromptTokenCount(messages []llm.Message) int {
	byteCount := 0
	for _, message := range messages {
		byteCount += len(message.Role) + len(message.Content)
		for _, part := range message.Parts {
			byteCount += len(part.Type) + len(part.Text) + len(part.MimeType) + len(part.DataBase64)
		}
	}
	return (byteCount + 3) / 4
}

func compactionTriggerTokenThreshold(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		return defaultCompactionTriggerTokens
	}
	threshold := contextWindowTokens * 6 / 10
	if threshold <= 0 {
		return defaultCompactionTriggerTokens
	}
	return threshold
}
