package bluecollar

import (
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
		toolName := suggestedNextToolFromObservation(observation)
		if observation.Failed() && toolName == "" {
			continue
		}
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
	if observation.RecoveryPacket != nil {
		for _, toolName := range observation.RecoveryPacket.AllowedTools {
			if trimmedToolName := strings.TrimSpace(toolName); trimmedToolName != "" {
				return trimmedToolName
			}
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
