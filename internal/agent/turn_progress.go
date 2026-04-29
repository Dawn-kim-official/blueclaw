package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

const progressMessageBudget = 6000
const maxProgressObservations = 12
const maxInteractiveReferences = 20
const maxSummaryTextLength = 500

type TurnProgress struct {
	Goal                          string                `json:"goal"`
	CompletedSteps                []ProgressObservation `json:"completedSteps,omitempty"`
	FailedOrBlockedSteps          []ProgressObservation `json:"failedOrBlockedSteps,omitempty"`
	LastSuccessfulObservationID   string                `json:"lastSuccessfulObservationID,omitempty"`
	LastSuccessfulObservationTool string                `json:"lastSuccessfulObservationTool,omitempty"`
	AttachmentCandidates          []ProgressAttachment  `json:"attachmentCandidates,omitempty"`
	RemainingWork                 string                `json:"remainingWork"`
	OmittedObservationCount       int                   `json:"omittedObservationCount,omitempty"`
}

type ProgressObservation struct {
	ObservationID  string               `json:"observationID"`
	ToolName       string               `json:"toolName,omitempty"`
	Status         string               `json:"status"`
	ShortSummary   string               `json:"shortSummary"`
	AttachmentRefs []ProgressAttachment `json:"attachmentRefs,omitempty"`
	RepeatCount    int                  `json:"repeatCount,omitempty"`
}

type ProgressAttachment struct {
	ObservationID    string `json:"observationID"`
	AttachmentIndex  int    `json:"attachmentIndex"`
	Filename         string `json:"filename,omitempty"`
	ContentType      string `json:"contentType,omitempty"`
	SizeBytes        int64  `json:"sizeBytes,omitempty"`
	Title            string `json:"title,omitempty"`
	HasDevicePayload bool   `json:"hasDevicePayload"`
}

func buildTurnProgress(request AgentTurnRequest, observations []turnObservation) TurnProgress {
	_ = request
	progress := TurnProgress{
		Goal:          "Answer the current user request.",
		RemainingWork: "Continue from the latest observation and complete the user's request.",
	}
	for _, observation := range compactProgressObservations(observations) {
		if observation.Status == "success" {
			progress.CompletedSteps = append(progress.CompletedSteps, observation)
			progress.LastSuccessfulObservationID = observation.ObservationID
			progress.LastSuccessfulObservationTool = observation.ToolName
			progress.AttachmentCandidates = append(progress.AttachmentCandidates, observation.AttachmentRefs...)
		} else {
			progress.FailedOrBlockedSteps = append(progress.FailedOrBlockedSteps, observation)
		}
	}
	progress.OmittedObservationCount = omittedObservationCount(progress)
	progress.CompletedSteps = keepLatestProgressObservations(progress.CompletedSteps)
	progress.FailedOrBlockedSteps = keepLatestProgressObservations(progress.FailedOrBlockedSteps)
	if len(observations) > 0 && progress.LastSuccessfulObservationID == "" {
		progress.RemainingWork = "Resolve the latest failed or blocked step, or return a truthful failure if the goal cannot be completed."
	}
	return progress
}

func compactProgressObservations(observations []turnObservation) []ProgressObservation {
	compactedObservations := []ProgressObservation{}
	for _, observation := range observations {
		progressObservation := summarizeObservation(observation)
		index := len(compactedObservations) - 1
		if index >= 0 && progressObservationSignature(compactedObservations[index]) == progressObservationSignature(progressObservation) {
			compactedObservations[index].RepeatCount++
			compactedObservations[index].ObservationID = progressObservation.ObservationID
			compactedObservations[index].AttachmentRefs = append(compactedObservations[index].AttachmentRefs, progressObservation.AttachmentRefs...)
			continue
		}
		progressObservation.RepeatCount = 1
		compactedObservations = append(compactedObservations, progressObservation)
	}
	return compactedObservations
}

func recentProgressObservations(observations []turnObservation) []ProgressObservation {
	return keepLatestProgressObservations(compactProgressObservations(observations))
}

func keepLatestProgressObservations(observations []ProgressObservation) []ProgressObservation {
	if len(observations) <= maxProgressObservations {
		return observations
	}
	return observations[len(observations)-maxProgressObservations:]
}

func omittedObservationCount(progress TurnProgress) int {
	count := len(progress.CompletedSteps) + len(progress.FailedOrBlockedSteps)
	if count <= maxProgressObservations*2 {
		return 0
	}
	return count - maxProgressObservations*2
}

func summarizeObservation(observation turnObservation) ProgressObservation {
	status := "success"
	if observation.IsError {
		status = "error"
	}
	return ProgressObservation{
		ObservationID:  observation.ObservationID,
		ToolName:       observation.Tool,
		Status:         status,
		ShortSummary:   summarizeObservationContent(observation),
		AttachmentRefs: progressAttachments(observation),
	}
}

func summarizeObservationContent(observation turnObservation) string {
	if strings.TrimSpace(observation.Summary) != "" {
		return truncateText(compactWhitespace(observation.Summary), maxSummaryTextLength)
	}
	switch strings.TrimSpace(observation.Tool) {
	case "browser.snapshot", "browser.observe":
		return summarizeBrowserSnapshot(observation.Content)
	case "browser.screenshot":
		if len(observation.Attachments) > 0 {
			return "Screenshot captured with attachment evidence."
		}
		return summarizeSafeJSONFields(observation.Content, []string{"capturedAt", "contentType", "filename", "sizeBytes"})
	case "file.pick":
		if len(observation.Attachments) > 0 {
			return "User selected a file and it is available as attachment evidence."
		}
		return summarizeSafeJSONFields(observation.Content, []string{"filename", "sizeBytes", "contentType", "expiresAt"})
	case "browser.open":
		return summarizeSafeJSONFields(observation.Content, []string{"url", "title", "status", "ok"})
	case "browser.click", "browser.fill", "browser.select", "browser.press", "browser.wait":
		return summarizeSafeJSONFields(observation.Content, []string{"ok", "action", "target", "capturedAt"})
	case "memory.search", "conversation.history":
		return summarizeCollection(observation.Content)
	default:
		if observation.IsError {
			return truncateText(compactWhitespace(redactUnsafeText(observation.Content)), 500)
		}
		return summarizeSafeJSONFields(observation.Content, []string{"ok", "status", "message", "error", "url", "title", "filename", "sizeBytes", "contentType"})
	}
}

func summarizeBrowserSnapshot(content string) string {
	var document map[string]any
	if json.Unmarshal([]byte(content), &document) != nil {
		return "Browser snapshot captured. " + truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	parts := []string{}
	if value := stringField(document, "url"); value != "" {
		parts = append(parts, "url="+value)
	}
	if value := stringField(document, "title"); value != "" {
		parts = append(parts, "title="+value)
	}
	if value := stringField(document, "snapshotText"); value != "" {
		parts = append(parts, "visibleText="+truncateText(compactWhitespace(value), maxSummaryTextLength))
	}
	references := stringSliceField(document, "interactiveRefs")
	if len(references) > maxInteractiveReferences {
		references = references[:maxInteractiveReferences]
	}
	if len(references) > 0 {
		parts = append(parts, "interactiveRefs="+strings.Join(references, ", "))
	}
	if booleanField(document, "hasMore") {
		parts = append(parts, "hasMore=true")
	}
	if len(parts) == 0 {
		return "Browser snapshot captured."
	}
	return strings.Join(parts, "; ")
}

func summarizeCollection(content string) string {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	switch typedValue := value.(type) {
	case []any:
		excerpts := []string{}
		for _, item := range typedValue {
			excerpt := summarizeJSONValue(item, []string{"speaker", "text", "content", "title", "fact", "score"})
			if excerpt != "" {
				excerpts = append(excerpts, excerpt)
			}
			if len(excerpts) >= 5 {
				break
			}
		}
		return fmt.Sprintf("Returned %d item(s). Top excerpts: %s", len(typedValue), strings.Join(excerpts, " | "))
	default:
		return summarizeJSONValue(value, []string{"messages", "hasMoreBefore", "historyCursor", "facts"})
	}
}

func summarizeSafeJSONFields(content string, fieldNames []string) string {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	summary := summarizeJSONValue(value, fieldNames)
	if summary == "" {
		return "Tool completed successfully."
	}
	return summary
}

func summarizeJSONValue(value any, fieldNames []string) string {
	document, isDocument := value.(map[string]any)
	if !isDocument {
		return truncateText(compactWhitespace(fmt.Sprintf("%v", value)), 500)
	}
	parts := []string{}
	for _, fieldName := range fieldNames {
		fieldValue, isFound := document[fieldName]
		if !isFound {
			continue
		}
		if isUnsafePromptField(fieldName) {
			continue
		}
		parts = append(parts, fieldName+"="+truncateText(compactWhitespace(fmt.Sprintf("%v", fieldValue)), 300))
	}
	return strings.Join(parts, "; ")
}

func progressAttachments(observation turnObservation) []ProgressAttachment {
	attachments := []ProgressAttachment{}
	for index, attachment := range observation.Attachments {
		attachments = append(attachments, ProgressAttachment{
			ObservationID:    observation.ObservationID,
			AttachmentIndex:  index,
			Filename:         attachment.Filename,
			ContentType:      attachment.ContentType,
			SizeBytes:        attachment.SizeBytes,
			Title:            attachment.Title,
			HasDevicePayload: strings.TrimSpace(attachment.DevicePath) != "",
		})
	}
	return attachments
}

func progressObservationSignature(observation ProgressObservation) string {
	return strings.Join([]string{observation.ToolName, observation.Status, observation.ShortSummary}, "\x00")
}

func stringField(document map[string]any, fieldName string) string {
	value, isString := document[fieldName].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func booleanField(document map[string]any, fieldName string) bool {
	value, isBool := document[fieldName].(bool)
	return isBool && value
}

func stringSliceField(document map[string]any, fieldName string) []string {
	values, isSlice := document[fieldName].([]any)
	if !isSlice {
		return nil
	}
	result := []string{}
	for _, value := range values {
		text, isString := value.(string)
		if isString && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func redactUnsafeText(value string) string {
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		lowerLine := strings.ToLower(line)
		if strings.Contains(lowerLine, "cookie") || strings.Contains(lowerLine, "authorization") || strings.Contains(lowerLine, "cdp") || strings.Contains(lowerLine, "profile") || strings.Contains(lowerLine, "devicepath") || strings.Contains(lowerLine, "localpath") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func isUnsafePromptField(fieldName string) bool {
	normalizedFieldName := strings.ToLower(fieldName)
	return strings.Contains(normalizedFieldName, "path") || strings.Contains(normalizedFieldName, "cookie") || strings.Contains(normalizedFieldName, "token") || strings.Contains(normalizedFieldName, "authorization") || strings.Contains(normalizedFieldName, "cdp") || strings.Contains(normalizedFieldName, "profile")
}
