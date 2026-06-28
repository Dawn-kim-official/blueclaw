package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type ObservedFact struct {
	ObjectType    string `json:"objectType"`
	Effect        string `json:"effect"`
	Visibility    string `json:"visibility,omitempty"`
	Durability    string `json:"durability,omitempty"`
	ObservationID string `json:"observationID,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
	ID            string `json:"id,omitempty"`
	Path          string `json:"path,omitempty"`
	URL           string `json:"url,omitempty"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	Summary       string `json:"summary,omitempty"`
}

type ProjectionMissingRequirement struct {
	Description        string   `json:"description"`
	ObjectType         string   `json:"objectType"`
	Effect             string   `json:"effect"`
	SuggestedNextTools []string `json:"suggestedNextTools,omitempty"`
}

type ProjectionRecoverableAction struct {
	ToolName string `json:"toolName"`
	Reason   string `json:"reason"`
}

type ObservedResultProjection struct {
	ObservedFacts       []ObservedFact                 `json:"observedFacts,omitempty"`
	MissingRequirements []ProjectionMissingRequirement `json:"missingRequirements,omitempty"`
	RecoverableActions  []ProjectionRecoverableAction  `json:"recoverableActions,omitempty"`
}

func buildObservedResultProjection(request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, actionDocument turnActionDocument) ObservedResultProjection {
	facts := observedFactsFromObservations(observations)
	facts = appendObservedAttachmentFacts(facts, attachments)
	facts = deduplicateObservedFacts(facts)
	return ObservedResultProjection{
		ObservedFacts:       facts,
		MissingRequirements: missingRequirementsForFinishClaims(request, facts, actionDocument),
		RecoverableActions:  recoverableActionsForProjection(observations),
	}
}

func observedFactsFromObservations(observations []turnObservation) []ObservedFact {
	facts := []ObservedFact{}
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		facts = append(facts, factsFromObservation(observation)...)
	}
	return facts
}

func factsFromObservation(observation turnObservation) []ObservedFact {
	if IsArtifactDeliveryTool(observation.Tool) {
		return fileAttachmentFacts(observation)
	}
	if facts := terminalCapabilityFacts(observation); len(facts) > 0 {
		return facts
	}
	switch strings.TrimSpace(observation.Tool) {
	case "file.promote":
		return filePathObservationFacts(observation, "file", "made_durable")
	case "file.write":
		return appendWorkspaceModifiedFact(filePathObservationFacts(observation, "file", "created"))
	case "file.edit", "file.patch":
		return appendWorkspaceModifiedFact(filePathObservationFacts(observation, "file", "updated"))
	case "calendar.event.add":
		return toolObjectFact(observation, "calendar_event", "scheduled")
	case "calendar.event.update":
		return toolObjectFact(observation, "calendar_event", "updated")
	case "calendar.event.delete":
		return toolObjectFact(observation, "calendar_event", "deleted")
	case "flow.task.add":
		return toolObjectFact(observation, "flow_task", "created")
	case "flow.task.update":
		return toolObjectFact(observation, "flow_task", "updated")
	case "flow.task.delete":
		return toolObjectFact(observation, "flow_task", "deleted")
	case "schedule.create":
		return toolObjectFact(observation, "schedule", "created")
	case "schedule.update":
		return toolObjectFact(observation, "schedule", "updated")
	case "schedule.cancel":
		return toolObjectFact(observation, "schedule", "deleted")
	case "site.app.publish":
		return siteObservationFacts(observation, "published")
	case "site.app.create":
		return append(siteObservationFacts(observation, "created"), siteWorkspaceModifiedFacts(observation)...)
	case "site.app.build":
		return siteObservationFacts(observation, "built")
	case "site.app.status":
		return siteStatusFacts(observation)
	case "ask.input", "ask.choice", "ask.confirm":
		return toolObjectFact(observation, "user_input", "requested")
	default:
		if isSendEvidenceTool(observation.Tool) {
			return toolObjectFact(observation, "message", "sent")
		}
		return nil
	}
}

func fileAttachmentFacts(observation turnObservation) []ObservedFact {
	facts := []ObservedFact{}
	for _, attachment := range observation.Attachments {
		facts = append(facts, ObservedFact{
			ObjectType:    "file",
			Effect:        "attached",
			Visibility:    "conversation",
			Durability:    "external",
			ObservationID: observation.ObservationID,
			ToolName:      observation.Tool,
			Filename:      strings.TrimSpace(attachment.Filename),
			ContentType:   strings.TrimSpace(attachment.ContentType),
		})
	}
	return facts
}

func filePathObservationFacts(observation turnObservation, objectType string, effect string) []ObservedFact {
	document := observationOutputDocument(observation)
	path := firstNonEmptyString(stringValue(document["path"]), stringValue(document["devicePath"]), stringValue(document["targetPath"]))
	if path == "" {
		inputDocument := toolInputDocument(observation)
		path = firstNonEmptyString(stringValue(inputDocument["path"]), stringValue(inputDocument["targetPath"]))
	}
	if path == "" {
		return nil
	}
	return []ObservedFact{{
		ObjectType:    objectType,
		Effect:        effect,
		Visibility:    fileVisibility(path),
		Durability:    fileDurability(path),
		ObservationID: observation.ObservationID,
		ToolName:      observation.Tool,
		Path:          path,
		Filename:      filepath.Base(filepath.ToSlash(path)),
		Summary:       truncateProjectionSummary(observation.Summary),
	}}
}

func appendWorkspaceModifiedFact(facts []ObservedFact) []ObservedFact {
	nextFacts := append([]ObservedFact{}, facts...)
	for _, fact := range facts {
		nextFacts = append(nextFacts, ObservedFact{
			ObjectType:    "workspace",
			Effect:        "modified",
			Visibility:    fact.Visibility,
			Durability:    fact.Durability,
			ObservationID: fact.ObservationID,
			ToolName:      fact.ToolName,
			Path:          fact.Path,
			Filename:      fact.Filename,
			Summary:       fact.Summary,
		})
	}
	return nextFacts
}

func toolObjectFact(observation turnObservation, objectType string, effect string) []ObservedFact {
	document := observationOutputDocument(observation)
	return []ObservedFact{{
		ObjectType:    objectType,
		Effect:        effect,
		Visibility:    effectVisibility(effect),
		Durability:    effectDurability(effect),
		ObservationID: observation.ObservationID,
		ToolName:      observation.Tool,
		ID:            firstNonEmptyString(stringValue(document["id"]), stringValue(document["eventID"]), stringValue(document["taskID"]), stringValue(document["taskScheduleID"])),
		URL:           firstNonEmptyString(stringValue(document["url"]), stringValue(document["publicURL"])),
		Summary:       truncateProjectionSummary(firstNonEmptyString(stringValue(document["title"]), stringValue(document["summary"]), observation.Summary)),
	}}
}

func siteObservationFacts(observation turnObservation, effect string) []ObservedFact {
	document := observationOutputDocument(observation)
	url := firstNonEmptyString(stringValue(document["url"]), stringValue(document["publicURL"]), stringValue(document["publishedURL"]), stringValue(document["previewURL"]))
	return []ObservedFact{{
		ObjectType:    "website",
		Effect:        effect,
		Visibility:    effectVisibility(effect),
		Durability:    effectDurability(effect),
		ObservationID: observation.ObservationID,
		ToolName:      observation.Tool,
		ID:            firstNonEmptyString(stringValue(document["siteID"]), stringValue(document["id"])),
		URL:           url,
		Summary:       truncateProjectionSummary(firstNonEmptyString(stringValue(document["title"]), observation.Summary)),
	}}
}

func siteStatusFacts(observation turnObservation) []ObservedFact {
	facts := siteObservationFacts(observation, "read")
	document := observationOutputDocument(observation)
	statusText := strings.ToLower(firstNonEmptyString(stringValue(document["status"]), stringValue(document["publishStatus"])))
	if siteStatusIsPublished(statusText) {
		facts = append(facts, siteObservationFacts(observation, "published")...)
	}
	return facts
}

func siteWorkspaceModifiedFacts(observation turnObservation) []ObservedFact {
	document := observationOutputDocument(observation)
	path := firstNonEmptyString(stringValue(document["appWorkspacePath"]), stringValue(document["sourceWorkspacePath"]), stringValue(document["workspacePath"]))
	if path == "" {
		return nil
	}
	return []ObservedFact{{
		ObjectType:    "workspace",
		Effect:        "modified",
		Visibility:    fileVisibility(path),
		Durability:    fileDurability(path),
		ObservationID: observation.ObservationID,
		ToolName:      observation.Tool,
		Path:          path,
		Filename:      filepath.Base(filepath.ToSlash(path)),
		Summary:       truncateProjectionSummary(observation.Summary),
	}}
}

func siteStatusIsPublished(statusText string) bool {
	normalizedStatus := strings.ToLower(strings.TrimSpace(statusText))
	if normalizedStatus == "" || strings.Contains(normalizedStatus, "unpublish") {
		return false
	}
	return normalizedStatus == "published" ||
		normalizedStatus == "public" ||
		normalizedStatus == "live" ||
		strings.Contains(normalizedStatus, "status:published") ||
		strings.Contains(normalizedStatus, "status=published")
}

func appendObservedAttachmentFacts(facts []ObservedFact, attachments []FileAttachment) []ObservedFact {
	for _, attachment := range attachments {
		facts = append(facts, ObservedFact{
			ObjectType:  "file",
			Effect:      "attached",
			Visibility:  "conversation",
			Durability:  "external",
			Filename:    strings.TrimSpace(attachment.Filename),
			ContentType: strings.TrimSpace(attachment.ContentType),
		})
	}
	return facts
}

func missingRequirementsForFinishClaims(request AgentTurnRequest, facts []ObservedFact, actionDocument turnActionDocument) []ProjectionMissingRequirement {
	message := finishActionMessage(actionDocument)
	if strings.TrimSpace(actionDocument.Action) != "finish" {
		return nil
	}
	requirements := requiredProjectionRequirementsFromContract(request)
	if strings.TrimSpace(message) != "" {
		requirements = append(requirements, claimedRequirementsFromFinishMessage(request, message)...)
	}
	missingRequirements := []ProjectionMissingRequirement{}
	for _, requirement := range requirements {
		if projectionHasObservedFact(facts, requirement.ObjectType, requirement.Effect) {
			continue
		}
		missingRequirements = append(missingRequirements, requirement)
	}
	return missingRequirements
}

func requiredProjectionRequirementsFromContract(request AgentTurnRequest) []ProjectionMissingRequirement {
	requirements := []ProjectionMissingRequirement{}
	for _, effect := range normalizeOutcomeEffects(request.OutcomeContract.RequiredEffects) {
		description := effect.Description
		if description == "" {
			description = "finish is missing required observed effect " + effect.ObjectType + "/" + effect.Effect
		}
		requirements = append(requirements, projectionRequirement(request, description, effect.ObjectType, effect.Effect, effect.SuggestedNextTools))
	}
	return deduplicateProjectionRequirements(requirements)
}

func claimedRequirementsFromFinishMessage(request AgentTurnRequest, message string) []ProjectionMissingRequirement {
	requirements := []ProjectionMissingRequirement{}
	if finishClaimsCalendarEventScheduled(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims calendar event scheduling but no successful calendar event creation was observed", "calendar_event", "scheduled", []string{"calendar.event.add"}))
	}
	if finishClaimsCalendarEventUpdated(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims calendar event update but no successful calendar event update was observed", "calendar_event", "updated", []string{"calendar.event.update"}))
	}
	if finishClaimsCalendarEventDeleted(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims calendar event deletion but no successful calendar event deletion was observed", "calendar_event", "deleted", []string{"calendar.event.delete"}))
	}
	if finishClaimsFlowTaskCreated(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims flow task creation but no successful flow task creation was observed", "flow_task", "created", []string{"flow.task.add"}))
	}
	if finishClaimsFlowTaskUpdated(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims flow task update but no successful flow task update was observed", "flow_task", "updated", []string{"flow.task.update"}))
	}
	if finishClaimsMessageSent(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims external message delivery but no successful send observation was observed", "message", "sent", requiredSendToolNamesForRequest(request)))
	}
	if finishClaimsWebsitePublished(message) {
		requirements = append(requirements, projectionRequirement(request, "finish claims website publication but no successful site publish observation was observed", "website", "published", []string{"site.app.publish"}))
	}
	return deduplicateProjectionRequirements(requirements)
}

func projectionRequirement(request AgentTurnRequest, description string, objectType string, effect string, suggestedTools []string) ProjectionMissingRequirement {
	return ProjectionMissingRequirement{
		Description:        description,
		ObjectType:         objectType,
		Effect:             effect,
		SuggestedNextTools: projectionSuggestedToolNames(request, suggestedTools),
	}
}

func projectionSuggestedToolNames(request AgentTurnRequest, suggestedTools []string) []string {
	if request.ToolSet == nil {
		return appendUniqueStrings(nil, suggestedTools...)
	}
	registeredTools := registeredToolNamesOnly(request.ToolSet, suggestedTools)
	if len(registeredTools) > 0 {
		return registeredTools
	}
	return appendUniqueStrings(nil, suggestedTools...)
}

func finishClaimsCalendarEventScheduled(message string) bool {
	return containsAnyNormalized(message, "일정", "캘린더", "calendar", "event", "회의", "미팅", "약속") &&
		containsAnyNormalized(message, "등록", "추가", "예약", "잡았", "잡혔", "만들", "생성", "scheduled", "added", "created", "booked")
}

func finishClaimsCalendarEventUpdated(message string) bool {
	return containsAnyNormalized(message, "일정", "캘린더", "calendar", "event", "회의", "미팅", "약속") &&
		containsAnyNormalized(message, "수정", "변경", "업데이트", "바꿨", "updated", "changed", "rescheduled")
}

func finishClaimsCalendarEventDeleted(message string) bool {
	return containsAnyNormalized(message, "일정", "캘린더", "calendar", "event", "회의", "미팅", "약속") &&
		containsAnyNormalized(message, "삭제", "취소", "cancelled", "canceled", "deleted", "removed")
}

func finishClaimsFlowTaskCreated(message string) bool {
	return containsAnyNormalized(message, "업무", "할 일", "할일", "태스크", "task", "todo") &&
		containsAnyNormalized(message, "등록", "추가", "만들", "생성", "created", "added")
}

func finishClaimsFlowTaskUpdated(message string) bool {
	return containsAnyNormalized(message, "업무", "할 일", "할일", "태스크", "task", "todo") &&
		containsAnyNormalized(message, "수정", "변경", "완료 처리", "완료로", "업데이트", "updated", "changed", "completed")
}

func finishClaimsMessageSent(message string) bool {
	return containsAnyNormalized(message, "메시지", "메일", "이메일", "email", "message", "dm", "slack", "mattermost") &&
		containsAnyNormalized(message, "보냈", "전송", "발송", "sent", "delivered", "emailed")
}

func finishClaimsWebsitePublished(message string) bool {
	return containsAnyNormalized(message, "사이트", "웹사이트", "페이지", "웹", "site", "website", "page", "url", "http") &&
		containsAnyNormalized(message, "배포", "공개", "게시", "published", "deployed", "live")
}

func projectionHasObservedFact(facts []ObservedFact, objectType string, effect string) bool {
	for _, fact := range facts {
		if fact.ObjectType == objectType && fact.Effect == effect {
			return true
		}
	}
	return false
}

func recoverableActionsForProjection(observations []turnObservation) []ProjectionRecoverableAction {
	actions := []ProjectionRecoverableAction{}
	for _, observation := range observations {
		if !observation.Failed() || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		if observation.RecoveryPacket == nil && !observation.SafeRetry() {
			continue
		}
		actions = append(actions, ProjectionRecoverableAction{
			ToolName: observation.Tool,
			Reason:   firstNonEmptyString(observation.FailureSummary(), observation.ContentText()),
		})
	}
	return actions
}

func observationOutputDocument(observation turnObservation) map[string]any {
	document := map[string]any{}
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(observation.ContentText())), &document); errorValue != nil {
		return document
	}
	return document
}

func toolInputDocument(observation turnObservation) map[string]any {
	document := map[string]any{}
	_, payload, hasPayload := strings.Cut(strings.TrimSpace(observation.ToolInputKey), "\x00")
	if !hasPayload {
		return document
	}
	if errorValue := json.Unmarshal([]byte(payload), &document); errorValue != nil {
		return map[string]any{}
	}
	return document
}

func fileVisibility(path string) string {
	normalizedPath := filepath.ToSlash(strings.TrimSpace(path))
	switch {
	case strings.Contains(normalizedPath, "/shared/public/") || strings.HasPrefix(normalizedPath, "/workspace/shared/public/"):
		return "public"
	case strings.Contains(normalizedPath, "/circles/") || strings.HasPrefix(normalizedPath, "/workspace/circles/"):
		return "circle"
	case strings.Contains(normalizedPath, "/artifacts/") || strings.HasPrefix(normalizedPath, "artifacts/"):
		return "task_artifact"
	default:
		return "private"
	}
}

func fileDurability(path string) string {
	if isDurableArtifactDevicePath(path) {
		return "durable"
	}
	return "draft"
}

func effectVisibility(effect string) string {
	switch effect {
	case "sent", "published", "scheduled", "updated", "deleted":
		return "external"
	default:
		return "workspace"
	}
}

func effectDurability(effect string) string {
	switch effect {
	case "sent", "published", "scheduled", "updated", "deleted", "created":
		return "durable"
	default:
		return ""
	}
}

func containsAnyNormalized(text string, fragments ...string) bool {
	normalizedText := strings.ToLower(strings.TrimSpace(text))
	for _, fragment := range fragments {
		if strings.Contains(normalizedText, strings.ToLower(strings.TrimSpace(fragment))) {
			return true
		}
	}
	return false
}

func deduplicateObservedFacts(facts []ObservedFact) []ObservedFact {
	seenFacts := map[string]bool{}
	deduplicatedFacts := []ObservedFact{}
	for _, fact := range facts {
		key := strings.Join([]string{fact.ObjectType, fact.Effect, fact.ObservationID, fact.ToolName, fact.ID, fact.Path, fact.URL, fact.Filename}, "\x00")
		if seenFacts[key] {
			continue
		}
		seenFacts[key] = true
		deduplicatedFacts = append(deduplicatedFacts, fact)
	}
	return deduplicatedFacts
}

func deduplicateProjectionRequirements(requirements []ProjectionMissingRequirement) []ProjectionMissingRequirement {
	seenRequirements := map[string]bool{}
	deduplicatedRequirements := []ProjectionMissingRequirement{}
	for _, requirement := range requirements {
		key := requirement.ObjectType + "\x00" + requirement.Effect
		if seenRequirements[key] {
			continue
		}
		seenRequirements[key] = true
		deduplicatedRequirements = append(deduplicatedRequirements, requirement)
	}
	return deduplicatedRequirements
}

func truncateProjectionSummary(summary string) string {
	trimmedSummary := compactWhitespace(strings.TrimSpace(summary))
	if len(trimmedSummary) <= 180 {
		return trimmedSummary
	}
	return trimmedSummary[:180] + "..."
}
