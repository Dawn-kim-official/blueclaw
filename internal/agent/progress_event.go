package agent

import "strings"

type progressEvent struct {
	Kind string
	Key  string
}

func progressEvents(observations []turnObservation) []progressEvent {
	events := []progressEvent{}
	seenFailures := map[string]bool{}
	for _, observation := range observations {
		if observation.Action == "set_quality_criteria" {
			events = append(events, progressEvent{Kind: "quality_criteria", Key: observation.ObservationID})
		}
		if observation.Action == "require_capabilities" && !observation.Failed() {
			events = append(events, progressEvent{Kind: "capability_required", Key: observation.ObservationID + ":" + observation.ContentText()})
		}
		if observation.Action == "call_tool" && !observation.Failed() {
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
		if observation.Action == "call_tool" && observation.Tool == "file.promote" && !observation.Failed() {
			events = append(events, progressEvent{Kind: "artifact_promoted", Key: observation.Output.Content})
		}
		if observation.Action == "call_tool" && observation.Tool == "file.write" && !observation.Failed() {
			events = append(events, progressEvent{Kind: "file_rewrite", Key: observation.ToolInputKey + ":" + observation.Output.Content})
		}
	}
	return events
}

func progressEventCount(observations []turnObservation) int {
	return len(progressEvents(observations))
}
