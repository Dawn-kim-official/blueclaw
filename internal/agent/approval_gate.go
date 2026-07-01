package agent

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/task"
)

type approvalHeldCall struct {
	ToolName     string          `json:"toolName"`
	ToolInput    json.RawMessage `json:"toolInput"`
	Confirmation string          `json:"confirmation"`
}

type approvalExecutedCall struct {
	ToolName  string          `json:"toolName"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
}

func isApprovalRequiredObservation(observation turnObservation) bool {
	if !observation.Failed() {
		return false
	}
	normalizedText := approvalObservationText(observation)
	if strings.Contains(normalizedText, "approval_required") {
		return true
	}
	return strings.EqualFold(observation.FailureStage(), "authorization") && strings.Contains(normalizedText, "requires approval")
}

func approvalObservationText(observation turnObservation) string {
	return strings.ToLower(strings.Join([]string{
		observation.FailureCode(),
		observation.FailureStage(),
		observation.FailureSummary(),
		observation.ContentText(),
		string(observation.Output.Data),
	}, " "))
}

func (agentTurnRunner *AgentTurnRunner) requestHeldCallApproval(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) toolCallActionOutcome {
	confirmation := heldCallConfirmationWording(actionDocument)
	heldCall := approvalHeldCall{
		ToolName:     strings.TrimSpace(actionDocument.ToolName),
		ToolInput:    copyJSONRawMessage(actionDocument.ToolInput),
		Confirmation: confirmation,
	}
	agentTurnRunner.appendEvent(taskRunID, "approval.pending_call", marshalEventBody(heldCall))
	pausedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingApproval, confirmation)
	if errorValue != nil {
		result, _ := agentTurnRunner.failTurn(taskRunID, request, errorValue.Error(), state.Observations, state.Attachments, state.ExecutionState)
		return toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true}
	}
	agentTurnRunner.appendEvent(taskRunID, "confirmation.requested", marshalEventBody(map[string]string{
		"userFacingMessage": confirmation,
		"message":           confirmation,
		"reasonCode":        "external_send",
		"reasonDetail":      "runtime approval gate for " + heldCall.ToolName,
		"responseLanguage":  request.ResponseLanguage,
	}))
	agentTurnRunner.appendEvent(taskRunID, "ask.requested", marshalEventBody(map[string]any{
		"kind":             "confirm",
		"message":          confirmation,
		"reasonCode":       "external_send",
		"reasonDetail":     "runtime approval gate for " + heldCall.ToolName,
		"responseLanguage": request.ResponseLanguage,
	}))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusWaitingApproval, "approval "+heldCall.ToolName, confirmation)
	return toolCallActionOutcome{
		Result:       AgentTurnResult{TaskRun: pausedTaskRun, UserNotice: confirmation, Attachments: state.Attachments},
		ShouldReturn: true,
		WasHandled:   true,
	}
}

func (agentTurnRunner *AgentTurnRunner) executeApprovedHeldCall(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation) (AgentTurnRequest, AgentTurnResult, bool) {
	taskEvents := agentTurnRunner.taskRunService.ListTaskEvent(taskRunID)
	heldCall, isFound := pendingApprovalHeldCall(taskEvents)
	if !isFound {
		return request, AgentTurnResult{}, false
	}
	request = requestWithHeldCallTool(request, heldCall.ToolName)
	state.Request = request
	actionDocument := turnActionDocument{
		Action:    "continue",
		ToolName:  heldCall.ToolName,
		ToolInput: copyJSONRawMessage(heldCall.ToolInput),
	}
	stepID := taskRunID + ":approval-continuation"
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusRunning, "approval "+heldCall.ToolName, heldCall.Confirmation)
	state.ToolCallCount++
	observationID := nextApprovalExecutionObservationID(taskEvents)
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, observationID, heldCall.ToolName, heldCall.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, request.WorkKinds, "")
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	agentTurnRunner.appendEvent(taskRunID, "approval.executed", marshalEventBody(approvalExecutedCall{ToolName: heldCall.ToolName, ToolInput: copyJSONRawMessage(heldCall.ToolInput)}))
	if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
		agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "approval "+heldCall.ToolName, observation.ContentText())
		return request, pausedResult, true
	}
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "approval "+heldCall.ToolName, observation.ContentText())
	return request, AgentTurnResult{}, false
}

func nextApprovalExecutionObservationID(taskEvents []task.TaskEvent) string {
	count := 0
	for _, taskEvent := range taskEvents {
		if strings.HasPrefix(taskEvent.Name, "tool.") && strings.HasSuffix(taskEvent.Name, ".result") {
			count++
		}
	}
	return nextObservationID(count + 1)
}

func requestWithHeldCallTool(request AgentTurnRequest, toolName string) AgentTurnRequest {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" || toolAvailableForAction(request.ToolSet, trimmedToolName) {
		return request
	}
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, trimmedToolName)
	request, _ = applyToolRequest(request, requestToolsArguments{ToolNames: []string{trimmedToolName}})
	return request
}

func pendingApprovalHeldCall(taskEvents []task.TaskEvent) (approvalHeldCall, bool) {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "approval.pending_call" {
			continue
		}
		heldCall, isValid := approvalHeldCallFromEvent(taskEvent)
		if !isValid || approvalHeldCallExecutedAfter(taskEvents[index+1:], heldCall.ToolName) {
			continue
		}
		return heldCall, true
	}
	return approvalHeldCall{}, false
}

func approvalHeldCallFromEvent(taskEvent task.TaskEvent) (approvalHeldCall, bool) {
	var heldCall approvalHeldCall
	if errorValue := json.Unmarshal([]byte(taskEvent.Body), &heldCall); errorValue != nil {
		return approvalHeldCall{}, false
	}
	heldCall.ToolName = strings.TrimSpace(heldCall.ToolName)
	heldCall.Confirmation = strings.TrimSpace(heldCall.Confirmation)
	if heldCall.ToolName == "" {
		return approvalHeldCall{}, false
	}
	heldCall.ToolInput = copyJSONRawMessage(heldCall.ToolInput)
	return heldCall, true
}

func approvalHeldCallExecutedAfter(taskEvents []task.TaskEvent, toolName string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "approval.executed" {
			continue
		}
		var executedCall approvalExecutedCall
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &executedCall); errorValue != nil {
			continue
		}
		if strings.TrimSpace(executedCall.ToolName) == strings.TrimSpace(toolName) {
			return true
		}
	}
	return false
}

// The approval question is user-facing wording, so it is the model's own message
// from the same turn that issued the approval-gated tool call (LLM-first, no extra
// model call). When the model left no deliverable message the wording is empty.
func heldCallConfirmationWording(actionDocument turnActionDocument) string {
	return deliverableModelWording(actionDocument.Message)
}

func copyJSONRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage{}, value...)
}
