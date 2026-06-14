package agent

import "strings"

type progressEvent struct {
	Kind string
	Key  string
}

func progressEvents(observations []turnObservation) []progressEvent {
	events := []progressEvent{}
	seenFailures := map[string]bool{}
	for index, observation := range observations {
		if observation.Action == "set_quality_criteria" {
			events = append(events, progressEvent{Kind: "quality_criteria", Key: observation.ObservationID})
		}
		if observation.Action == "select_tools" && !observation.Failed() && hasSuccessfulToolCallAfter(observations, index) {
			events = append(events, progressEvent{Kind: "tool_palette_selected", Key: observation.ObservationID + ":" + observation.ContentText()})
		}
		if observation.Action == "continue" && !observation.Failed() {
			events = append(events, progressEvent{Kind: "tool_success", Key: observation.ObservationID + ":" + observation.Tool})
		}
		if observation.Failed() && strings.TrimSpace(observation.AttemptFingerprint) != "" && !seenFailures[observation.AttemptFingerprint] {
			seenFailures[observation.AttemptFingerprint] = true
			events = append(events, progressEvent{Kind: "failure_fingerprint", Key: observation.AttemptFingerprint})
		}
		for _, attachment := range observation.Attachments {
			if strings.TrimSpace(attachment.DevicePath) != "" {
				events = append(events, progressEvent{Kind: "attachment", Key: attachment.DevicePath})
			}
		}
		if observation.Action == "continue" && observation.Tool == "file.promote" && !observation.Failed() {
			events = append(events, progressEvent{Kind: "artifact_promoted", Key: observation.Output.Content})
		}
		if observation.Action == "continue" && (observation.Tool == "file.write" || observation.Tool == "file.edit" || observation.Tool == "file.patch") && !observation.Failed() {
			events = append(events, progressEvent{Kind: "file_rewrite", Key: observation.ToolInputKey + ":" + observation.Output.Content})
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
