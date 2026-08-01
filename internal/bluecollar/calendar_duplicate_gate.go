package bluecollar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
)

const calendarAddOperation = "calendar.add"

type calendarAddInput struct {
	Title    string   `json:"title"`
	StartISO string   `json:"startISO"`
	EndISO   string   `json:"endISO"`
	People   []string `json:"people"`
}

type calendarListedEvent struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	StartISO string `json:"startISO"`
	EndISO   string `json:"endISO"`
}

type calendarDuplicateCandidatePayload struct {
	Status     string                `json:"status"`
	Candidates []calendarListedEvent `json:"candidates"`
}

type calendarDuplicateJudgment struct {
	IsDuplicate bool   `json:"isDuplicate"`
	Reason      string `json:"reason"`
}

func (agentTurnRunner *AgentTurnRunner) resolveCalendarDuplicate(ctx context.Context, taskRunID string, observationID string, request AgentTurnRequest, actionDocument turnActionDocument, observation turnObservation) turnObservation {
	if effectiveObservationToolName(actionDocument.ToolName, actionDocument.ToolInput) != calendarAddOperation {
		return observation
	}
	candidates, isDuplicateCandidate := parseCalendarDuplicateCandidates(observation)
	if !isDuplicateCandidate {
		return observation
	}
	addInput, hasInput := decodeCalendarAddInput(actionDocument.ToolInput)
	if !hasInput {
		return observation
	}
	if agentTurnRunner.judgeCalendarDuplicate(ctx, addInput, candidates) {
		existingEvent := candidates[0]
		message := calendarDuplicateSkippedMessage(existingEvent, request.ResponseLanguage)
		toolInputKey := canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
		toolDefinition, _ := request.ToolSet.ToolDefinition(actionDocument.ToolName)
		return agentTurnRunner.saveToolObservation(ctx, taskRunID, observationID, actionDocument.ToolName, toolDefinition.ID, actionDocument.ToolInput, calendarAddOperation, toolInputKey, ToolSuccess(message), request.WorkspaceRootPath, request.TurnStartedAt, 0)
	}
	retryContext := WithToolConflictResolution(ctx, ToolConflictResolutionAllowDuplicate)
	return agentTurnRunner.invokeTool(retryContext, request.ToolSet, taskRunID, observationID, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message)
}

func parseCalendarDuplicateCandidates(observation turnObservation) ([]calendarListedEvent, bool) {
	rawResult := observation.Output.Data
	if len(rawResult) == 0 {
		rawResult = json.RawMessage(strings.TrimSpace(observation.Output.Content))
	}
	if len(rawResult) == 0 {
		return nil, false
	}
	var payload calendarDuplicateCandidatePayload
	if json.Unmarshal(rawResult, &payload) != nil {
		return nil, false
	}
	if payload.Status != "duplicate_candidate" || len(payload.Candidates) == 0 {
		return nil, false
	}
	return payload.Candidates, true
}

func decodeCalendarAddInput(toolInput json.RawMessage) (calendarAddInput, bool) {
	var wrapper struct {
		Input calendarAddInput `json:"input"`
	}
	if json.Unmarshal(toolInput, &wrapper) != nil {
		return calendarAddInput{}, false
	}
	if strings.TrimSpace(wrapper.Input.StartISO) == "" {
		return calendarAddInput{}, false
	}
	return wrapper.Input, true
}

func (agentTurnRunner *AgentTurnRunner) judgeCalendarDuplicate(ctx context.Context, addInput calendarAddInput, existingEvents []calendarListedEvent) bool {
	if agentTurnRunner.languageModel == nil {
		return false
	}
	response, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: calendarDuplicateJudgeMessages(addInput, existingEvents),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_calendar_duplicate_judge",
			Document:           calendarDuplicateJudgeSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return false
	}
	var judgment calendarDuplicateJudgment
	if json.Unmarshal([]byte(response.Content), &judgment) != nil {
		return false
	}
	return judgment.IsDuplicate
}

func calendarDuplicateSkippedMessage(existingEvent calendarListedEvent, responseLanguage string) string {
	title := strings.TrimSpace(existingEvent.Title)
	eventID := strings.TrimSpace(existingEvent.ID)
	if strings.HasPrefix(strings.ToLower(ResolveResponseLanguage(responseLanguage)), "en") {
		return fmt.Sprintf("'%s' is already on the calendar at this time, so nothing was added. The existing event (id=%s) is unchanged.", title, eventID)
	}
	return fmt.Sprintf("이미 같은 시각에 '%s' 일정이 등록돼 있어 새로 추가하지 않았습니다. 기존 일정(id=%s)을 그대로 유지합니다.", title, eventID)
}

func calendarDuplicateJudgeMessages(addInput calendarAddInput, existingEvents []calendarListedEvent) []llm.Message {
	existingLines := []string{}
	for _, event := range existingEvents {
		existingLines = append(existingLines, fmt.Sprintf("- id=%s | %s | %s ~ %s", strings.TrimSpace(event.ID), strings.TrimSpace(event.Title), event.StartISO, event.EndISO))
	}
	userContent := strings.Join([]string{
		"Decide whether the new event below is the same event as one of the existing events.",
		"Every candidate already matches on time and attendees. If they refer to the same gathering, treat them as duplicates (isDuplicate=true) even when the titles are worded differently. If it is clearly a different gathering, answer false.",
		"",
		fmt.Sprintf("New event: %s | %s ~ %s", strings.TrimSpace(addInput.Title), addInput.StartISO, addInput.EndISO),
		"Existing events:",
		strings.Join(existingLines, "\n"),
	}, "\n")
	return []llm.Message{
		{Role: "system", Content: "You are a calendar duplicate judge. Output only the structured judgment."},
		{Role: "user", Content: userContent},
	}
}

func calendarDuplicateJudgeSchema() string {
	return `{"type":"object","properties":{"isDuplicate":{"type":"boolean","description":"true if the new event is the same as one of the existing events"},"reason":{"type":"string"}},"required":["isDuplicate"]}`
}
