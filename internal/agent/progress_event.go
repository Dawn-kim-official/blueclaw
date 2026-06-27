package agent

import (
	"encoding/json"
	"strings"

	"blueclaw/internal/task"
)

type progressEvent struct {
	Kind string
	Key  string
}

type qualifyingProgressEvent struct {
	ObservationID string `json:"observationID"`
	Kind          string `json:"kind"`
	Tool          string `json:"tool"`
}

const maxFailureProgressSinceSuccess = 4

func progressEvents(observations []turnObservation) []progressEvent {
	events := []progressEvent{}
	seenFailures := map[string]bool{}
	failureProgressSinceSuccess := 0
	for index, observation := range observations {
		recordSuccess := func(event progressEvent) {
			events = append(events, event)
			failureProgressSinceSuccess = 0
		}
		if observation.Action == "set_quality_criteria" {
			recordSuccess(progressEvent{Kind: "quality_criteria", Key: observation.ObservationID})
		}
		if observation.Action == "tool.request" && !observation.Failed() && hasSuccessfulToolCallAfter(observations, index) {
			recordSuccess(progressEvent{Kind: "tool_palette_selected", Key: observation.ObservationID + ":" + observation.ContentText()})
		}
		if observation.Action == "continue" && !observation.Failed() && !isInspectionProgressTool(observation.Tool) {
			recordSuccess(progressEvent{Kind: "tool_success", Key: observation.ObservationID + ":" + observation.Tool})
		}
		if observation.Failed() && strings.TrimSpace(observation.AttemptFingerprint) != "" && !seenFailures[observation.AttemptFingerprint] {
			seenFailures[observation.AttemptFingerprint] = true
			if failureProgressSinceSuccess < maxFailureProgressSinceSuccess {
				events = append(events, progressEvent{Kind: "failure_fingerprint", Key: observation.AttemptFingerprint})
				failureProgressSinceSuccess++
			}
		}
		for _, attachment := range observation.Attachments {
			if strings.TrimSpace(attachment.DevicePath) != "" {
				recordSuccess(progressEvent{Kind: "attachment", Key: attachment.DevicePath})
			}
		}
		if observation.Action == "continue" && observation.Tool == "file.promote" && !observation.Failed() {
			recordSuccess(progressEvent{Kind: "artifact_promoted", Key: observation.Output.Content})
		}
		if observation.Action == "continue" && (observation.Tool == "file.write" || observation.Tool == "file.edit" || observation.Tool == "file.patch") && !observation.Failed() {
			recordSuccess(progressEvent{Kind: "file_rewrite", Key: observation.ToolInputKey + ":" + observation.Output.Content})
		}
	}
	return events
}

func hasSuccessfulToolCallAfter(observations []turnObservation, index int) bool {
	for _, observation := range observations[index+1:] {
		if observation.Action == "continue" && strings.TrimSpace(observation.Tool) != "" && !observation.Failed() {
			return true
		}
	}
	return false
}

func progressEventCount(observations []turnObservation) int {
	return len(progressEvents(observations))
}

func qualifyingDurableProgressEventsSinceTierStart(taskEvents []task.TaskEvent, observations []turnObservation) []qualifyingProgressEvent {
	startObservationID := currentTierStartedAfterObservationID(taskEvents)
	startIndex := observationIndex(observations, startObservationID)
	events := []qualifyingProgressEvent{}
	for index, observation := range observations {
		if index <= startIndex {
			continue
		}
		if event, isQualified := qualifyingDurableProgressEvent(observation); isQualified {
			events = append(events, event)
		}
	}
	return events
}

func currentTierStartedAfterObservationID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "agent.budget_escalated" {
			continue
		}
		var eventBody budgetEscalatedEventBody
		if json.Unmarshal([]byte(taskEvent.Body), &eventBody) != nil {
			return ""
		}
		return lastString(eventBody.QualifyingEventIDs)
	}
	return ""
}

func observationIndex(observations []turnObservation, observationID string) int {
	trimmedObservationID := strings.TrimSpace(observationID)
	if trimmedObservationID == "" {
		return -1
	}
	for index, observation := range observations {
		if observation.ObservationID == trimmedObservationID {
			return index
		}
	}
	return -1
}

func qualifyingDurableProgressEvent(observation turnObservation) (qualifyingProgressEvent, bool) {
	if observation.Action != "continue" || observation.Failed() || strings.TrimSpace(observation.ObservationID) == "" {
		return qualifyingProgressEvent{}, false
	}
	toolName := strings.TrimSpace(observation.Tool)
	if isInspectionProgressTool(toolName) {
		return qualifyingProgressEvent{}, false
	}
	switch toolName {
	case "file.write", "file.edit", "file.patch":
		return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "file_change", Tool: toolName}, true
	case "site.app.build":
		return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "site_build", Tool: toolName}, true
	case "site.app.publish":
		return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "site_publish", Tool: toolName}, true
	case "file.promote":
		return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "artifact_promotion", Tool: toolName}, true
	case "file.attach":
		return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "attachment", Tool: toolName}, true
	case "terminal.run":
		if terminalObservationExitCode(observation) == 0 {
			return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "terminal_success", Tool: toolName}, true
		}
	}
	if len(observation.Attachments) > 0 {
		return qualifyingProgressEvent{ObservationID: observation.ObservationID, Kind: "attachment", Tool: toolName}, true
	}
	return qualifyingProgressEvent{}, false
}

func isInspectionProgressTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "file.read", "memory.search", "site.app.status", "conversation.history":
		return true
	default:
		return false
	}
}

func terminalObservationExitCode(observation turnObservation) int {
	var output struct {
		ExitCode *int `json:"exitCode"`
	}
	if json.Unmarshal([]byte(observation.ContentText()), &output) != nil || output.ExitCode == nil {
		return -1
	}
	return *output.ExitCode
}

func qualifyingProgressEventIDs(events []qualifyingProgressEvent) []string {
	eventIDs := []string{}
	for _, event := range events {
		if strings.TrimSpace(event.ObservationID) != "" {
			eventIDs = append(eventIDs, event.ObservationID)
		}
	}
	return eventIDs
}

func lastString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[len(values)-1])
}
