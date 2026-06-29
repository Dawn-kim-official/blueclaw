package agent

import (
	"encoding/json"
	"strings"
)

func terminalObservationMatchesCapabilityTool(observation turnObservation, toolName string) bool {
	invokedToolName, _, isFound := terminalCapabilityResponse(observation)
	return isFound && strings.TrimSpace(invokedToolName) == strings.TrimSpace(toolName)
}

func terminalCapabilityFacts(observation turnObservation) []ObservedFact {
	toolName, resultDocument, isFound := terminalCapabilityResponse(observation)
	if !isFound {
		return nil
	}
	syntheticObservation := observation
	syntheticObservation.Tool = toolName
	if len(resultDocument) > 0 {
		content, errorValue := json.Marshal(resultDocument)
		if errorValue == nil {
			syntheticObservation.Output.Content = string(content)
		}
	}
	return factsFromCapabilityObservation(syntheticObservation)
}

func factsFromCapabilityObservation(observation turnObservation) []ObservedFact {
	switch strings.TrimSpace(observation.Tool) {
	case "calendar.add":
		return toolObjectFact(observation, "calendar_event", "scheduled")
	case "calendar.update":
		return toolObjectFact(observation, "calendar_event", "updated")
	case "calendar.delete":
		return toolObjectFact(observation, "calendar_event", "deleted")
	case "task.add":
		return toolObjectFact(observation, "flow_task", "created")
	case "task.update":
		return toolObjectFact(observation, "flow_task", "updated")
	case "task.delete":
		return toolObjectFact(observation, "flow_task", "deleted")
	case "schedule.create":
		return toolObjectFact(observation, "schedule", "created")
	case "schedule.update":
		return toolObjectFact(observation, "schedule", "updated")
	case "schedule.cancel":
		return toolObjectFact(observation, "schedule", "deleted")
	case "site.publish":
		return siteObservationFacts(observation, "published")
	case "site.create":
		return append(siteObservationFacts(observation, "created"), siteWorkspaceModifiedFacts(observation)...)
	case "site.status":
		return siteStatusFacts(observation)
	default:
		if isSendEvidenceTool(observation.Tool) {
			return toolObjectFact(observation, "message", "sent")
		}
		return nil
	}
}

func terminalCapabilityResponse(observation turnObservation) (string, map[string]any, bool) {
	if observation.Failed() || strings.TrimSpace(observation.Tool) != TerminalRunToolName {
		return "", nil, false
	}
	commandResult := observationOutputDocument(observation)
	stdout := strings.TrimSpace(stringValue(commandResult["stdout"]))
	if stdout == "" {
		return "", nil, false
	}
	response, isFound := parseJSONDocument(stdout)
	if !isFound || responseIsError(response) {
		return "", nil, false
	}
	toolName := capabilityResponseToolName(response)
	if toolName == "" {
		return "", nil, false
	}
	return toolName, capabilityResultDocument(response), true
}

func capabilityResponseToolName(response map[string]any) string {
	toolName := strings.TrimSpace(stringValue(response["toolName"]))
	if toolName != "" {
		return toolName
	}
	result, isDocument := response["result"].(map[string]any)
	if !isDocument {
		return ""
	}
	return strings.TrimSpace(stringValue(result["toolName"]))
}

func parseJSONDocument(value string) (map[string]any, bool) {
	var document map[string]any
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(value)), &document); errorValue == nil {
		return document, true
	}
	startIndex := strings.Index(value, "{")
	endIndex := strings.LastIndex(value, "}")
	if startIndex < 0 || endIndex <= startIndex {
		return nil, false
	}
	if errorValue := json.Unmarshal([]byte(value[startIndex:endIndex+1]), &document); errorValue != nil {
		return nil, false
	}
	return document, true
}

func responseIsError(response map[string]any) bool {
	if isError, isBoolean := response["isError"].(bool); isBoolean && isError {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(stringValue(response["status"]))) {
	case "error", "denied":
		return true
	default:
		return false
	}
}

func capabilityResultDocument(response map[string]any) map[string]any {
	result, isDocument := response["result"].(map[string]any)
	if isDocument {
		return result
	}
	return response
}
