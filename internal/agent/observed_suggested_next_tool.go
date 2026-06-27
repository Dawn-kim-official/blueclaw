package agent

import (
	"encoding/json"
	"strings"
)

type observedSuggestedNextTool struct {
	ToolName      string
	ObservationID string
	SourceTool    string
	Reason        string
}

func observedSuggestedNextToolNames(observations []turnObservation) []string {
	suggestion, isFound := latestObservedSuggestedNextTool(observations)
	if !isFound {
		return nil
	}
	return []string{suggestion.ToolName}
}

func hasPendingObservedSuggestedNextTool(observations []turnObservation) bool {
	_, isFound := latestObservedSuggestedNextTool(observations)
	return isFound
}

func latestObservedSuggestedNextTool(observations []turnObservation) (observedSuggestedNextTool, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Failed() {
			continue
		}
		toolName := suggestedNextToolFromObservation(observation)
		if toolName == "" || suggestedToolWasUsedAfter(observations, index, toolName) {
			continue
		}
		return observedSuggestedNextTool{
			ToolName:      toolName,
			ObservationID: observation.ObservationID,
			SourceTool:    strings.TrimSpace(observation.Tool),
			Reason:        suggestedNextToolReason(observation, toolName),
		}, true
	}
	return observedSuggestedNextTool{}, false
}

func suggestedNextToolFromObservation(observation turnObservation) string {
	document := map[string]any{}
	if json.Unmarshal([]byte(strings.TrimSpace(observation.ContentText())), &document) != nil {
		return ""
	}
	return suggestedNextToolFromValue(document)
}

func suggestedNextToolFromValue(value any) string {
	switch typedValue := value.(type) {
	case map[string]any:
		if toolName := strings.TrimSpace(stringValue(typedValue["suggestedNextTool"])); toolName != "" {
			return toolName
		}
		if toolName := firstStringValue(typedValue["suggestedNextTools"]); toolName != "" {
			return toolName
		}
		for _, key := range []string{"workspaceHealthDetails", "result", "data"} {
			if toolName := suggestedNextToolFromValue(typedValue[key]); toolName != "" {
				return toolName
			}
		}
	case []any:
		for _, item := range typedValue {
			if toolName := suggestedNextToolFromValue(item); toolName != "" {
				return toolName
			}
		}
	}
	return ""
}

func firstStringValue(value any) string {
	values, isArray := value.([]any)
	if !isArray {
		return ""
	}
	for _, item := range values {
		if toolName := strings.TrimSpace(stringValue(item)); toolName != "" {
			return toolName
		}
	}
	return ""
}

func suggestedToolWasUsedAfter(observations []turnObservation, sourceIndex int, toolName string) bool {
	for _, observation := range observations[sourceIndex+1:] {
		if strings.TrimSpace(observation.Tool) == toolName {
			return true
		}
	}
	return false
}

func suggestedNextToolReason(observation turnObservation, toolName string) string {
	sourceTool := strings.TrimSpace(observation.Tool)
	if sourceTool == "" {
		return "A previous observation suggested " + toolName + " as the next required tool."
	}
	return sourceTool + " suggested " + toolName + " as the next required tool."
}
