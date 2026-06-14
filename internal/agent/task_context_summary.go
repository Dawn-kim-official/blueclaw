package agent

import (
	"encoding/json"
	"strings"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

const taskContextSummaryEventName = "agent.context_summary"
const defaultCompactionTriggerTokens = 96000

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
