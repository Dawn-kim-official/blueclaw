package agent

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
		message := fmt.Sprintf("이미 같은 시각에 '%s' 일정이 등록돼 있어 새로 추가하지 않았습니다. 기존 일정(id=%s)을 그대로 유지합니다.", strings.TrimSpace(existingEvent.Title), strings.TrimSpace(existingEvent.ID))
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

func calendarDuplicateJudgeMessages(addInput calendarAddInput, existingEvents []calendarListedEvent) []llm.Message {
	existingLines := []string{}
	for _, event := range existingEvents {
		existingLines = append(existingLines, fmt.Sprintf("- id=%s | %s | %s ~ %s", strings.TrimSpace(event.ID), strings.TrimSpace(event.Title), event.StartISO, event.EndISO))
	}
	userContent := strings.Join([]string{
		"새로 추가하려는 일정이 아래 기존 일정 중 하나와 동일한 일정인지 판단하세요.",
		"이 후보들은 이미 같은 시각·같은 참석자로 확인된 일정입니다. 같은 모임을 가리키면 제목 표현이 조금 달라도 동일(isDuplicate=true)로 봅니다. 명백히 다른 모임이면 다른 일정(false)입니다.",
		"",
		fmt.Sprintf("새 일정: %s | %s ~ %s", strings.TrimSpace(addInput.Title), addInput.StartISO, addInput.EndISO),
		"기존 일정:",
		strings.Join(existingLines, "\n"),
	}, "\n")
	return []llm.Message{
		{Role: "system", Content: "당신은 캘린더 중복 판단기입니다. 오직 구조화된 판단만 출력합니다."},
		{Role: "user", Content: userContent},
	}
}

func calendarDuplicateJudgeSchema() string {
	return `{"type":"object","properties":{"isDuplicate":{"type":"boolean","description":"true if the new event is the same as one of the existing events"},"reason":{"type":"string"}},"required":["isDuplicate"]}`
}
