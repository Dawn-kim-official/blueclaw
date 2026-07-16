package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"blueclaw/internal/llm"
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
	return observation.Failed() && observation.Failure.RequiresApproval
}

func (agentTurnRunner *AgentTurnRunner) requestHeldCallApproval(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) toolCallActionOutcome {
	confirmation, errorValue := agentTurnRunner.heldCallConfirmationWording(ctx, request, actionDocument)
	if errorValue != nil {
		result, _ := agentTurnRunner.failTurn(taskRunID, request, errorValue.Error(), state.Observations, state.Attachments, state.ExecutionState)
		return toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true}
	}
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
		"kind":             "ask_confirm",
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
	executionToolSet := toolSetWithApprovedHeldCall(request.ToolSet, heldCall.ToolName)
	observation := agentTurnRunner.invokeTool(ctx, executionToolSet, taskRunID, observationID, heldCall.ToolName, heldCall.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, "")
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	agentTurnRunner.appendEvent(taskRunID, "approval.executed", marshalEventBody(approvalExecutedCall{ToolName: heldCall.ToolName, ToolInput: copyJSONRawMessage(heldCall.ToolInput)}))
	if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
		agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "approval "+heldCall.ToolName, observation.ContentText())
		return request, pausedResult, true
	}
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "approval "+heldCall.ToolName, observation.ContentText())
	return request, AgentTurnResult{}, false
}

func toolSetWithApprovedHeldCall(toolSet *ToolSet, toolName string) *ToolSet {
	trimmedToolName := strings.TrimSpace(toolName)
	if toolSet == nil || !toolSet.IsRegistered(trimmedToolName) {
		return toolSet
	}
	allowedToolNames := append(toolSet.ListToolNames(), trimmedToolName)
	return toolSet.WithAllowedToolNames(appendUniqueStrings(allowedToolNames))
}

func nextApprovalExecutionObservationID(taskEvents []task.TaskEvent) string {
	highestObservationIndex := 0
	for _, taskEvent := range taskEvents {
		if strings.HasPrefix(taskEvent.Name, "tool.") && strings.HasSuffix(taskEvent.Name, ".result") {
			var observation struct {
				ObservationID string `json:"observationID"`
			}
			if json.Unmarshal([]byte(taskEvent.Body), &observation) != nil {
				continue
			}
			observationIndex, isValid := observationIndexFromID(observation.ObservationID)
			if isValid && observationIndex > highestObservationIndex {
				highestObservationIndex = observationIndex
			}
		}
	}
	return nextObservationID(highestObservationIndex + 1)
}

func observationIndexFromID(observationID string) (int, bool) {
	trimmedObservationID := strings.TrimSpace(observationID)
	if !strings.HasPrefix(trimmedObservationID, "obs-") {
		return 0, false
	}
	observationIndex, errorValue := strconv.Atoi(strings.TrimPrefix(trimmedObservationID, "obs-"))
	return observationIndex, errorValue == nil
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

func toolCallRequiresRuntimeApproval(toolSet *ToolSet, actionDocument turnActionDocument) bool {
	trimmedToolName := strings.TrimSpace(actionDocument.ToolName)
	if trimmedToolName == "" || trimmedToolName == CapabilityInvokeToolName {
		return false
	}
	definition, isFound := toolSet.ToolDefinition(trimmedToolName)
	if isFound && definition.RequiresApproval {
		return true
	}
	if trimmedToolName != TerminalRunToolName {
		return false
	}
	var input struct {
		ApprovalRequired bool `json:"approvalRequired"`
	}
	return json.Unmarshal(actionDocument.ToolInput, &input) == nil && input.ApprovalRequired
}

type approvalQuestionContextDocument struct {
	ResponseLanguage string            `json:"responseLanguage,omitempty"`
	OriginalRequest  string            `json:"originalRequest,omitempty"`
	ModelDraft       string            `json:"modelDraft,omitempty"`
	Operation        string            `json:"operation,omitempty"`
	ActionDetails    map[string]string `json:"actionDetails,omitempty"`
}

type approvalQuestionResponseDocument struct {
	Question string `json:"question"`
}

type approvalQuestionActionInput struct {
	TargetType     string   `json:"targetType"`
	PersonHint     string   `json:"personHint"`
	ChannelName    string   `json:"channelName"`
	Message        string   `json:"message"`
	Body           string   `json:"body"`
	Subject        string   `json:"subject"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	Reason         string   `json:"reason"`
	ApprovalReason string   `json:"approvalReason"`
	Path           string   `json:"path"`
	DevicePath     string   `json:"devicePath"`
	TargetPath     string   `json:"targetPath"`
	Slug           string   `json:"slug"`
	SiteID         string   `json:"siteID"`
	EventID        string   `json:"eventID"`
	To             []string `json:"to"`
	People         []string `json:"people"`
}

func (agentTurnRunner *AgentTurnRunner) heldCallConfirmationWording(ctx context.Context, request AgentTurnRequest, actionDocument turnActionDocument) (string, error) {
	modelDraft := deliverableModelWording(actionDocument.Message)
	return agentTurnRunner.generateHeldCallConfirmationWording(ctx, request, actionDocument, modelDraft)
}

func (agentTurnRunner *AgentTurnRunner) generateHeldCallConfirmationWording(ctx context.Context, request AgentTurnRequest, actionDocument turnActionDocument, modelDraft string) (string, error) {
	if agentTurnRunner.languageModel == nil {
		return "", errors.New("language model provider is not configured")
	}
	contextDocument := approvalQuestionContext(request, actionDocument, modelDraft)
	contextDocumentBytes, errorValue := json.Marshal(contextDocument)
	if errorValue != nil {
		return "", errorValue
	}
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: strings.Join([]string{
				"Write exactly one concise user-facing approval question for Blueclaw.",
				"The question asks whether to perform the pending action.",
				"Use the original request, model draft, and action details to phrase the target, content, file, event, or site naturally.",
				"Include consequential details when present so the user can approve a concrete action.",
				"Do not mention internal tool names, operation identifiers, JSON, schemas, approval gates, runtime, or implementation details.",
				"Do not answer the question, report status, or explain the policy.",
			}, "\n")},
			{Role: "system", Content: responseLanguageInstruction(request.ResponseLanguage)},
			{Role: "user", Content: string(contextDocumentBytes)},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_approval_question",
			Document:           `{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	var responseDocument approvalQuestionResponseDocument
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &responseDocument); errorValue != nil {
		return "", errorValue
	}
	question := strings.TrimSpace(responseDocument.Question)
	if question == "" {
		return "", errors.New("approval question is empty")
	}
	return question, nil
}

func approvalQuestionContext(request AgentTurnRequest, actionDocument turnActionDocument, modelDraft string) approvalQuestionContextDocument {
	operation, input := effectiveActionToolNameAndInput(actionDocument.ToolName, actionDocument.ToolInput)
	return approvalQuestionContextDocument{
		ResponseLanguage: strings.TrimSpace(request.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(request.Prompt),
		ModelDraft:       strings.TrimSpace(modelDraft),
		Operation:        strings.TrimSpace(operation),
		ActionDetails:    approvalQuestionActionDetails(input),
	}
}

func approvalQuestionActionDetails(input json.RawMessage) map[string]string {
	if len(input) == 0 {
		return nil
	}
	var document approvalQuestionActionInput
	if json.Unmarshal(input, &document) != nil {
		return nil
	}
	details := map[string]string{}
	approvalQuestionSetDetail(details, "target", firstNonEmptyString(document.PersonHint, document.ChannelName, strings.Join(trimNonEmptyConfirmationStrings(document.To), ", "), strings.Join(trimNonEmptyConfirmationStrings(document.People), ", ")))
	approvalQuestionSetDetail(details, "deliveryTargetType", document.TargetType)
	approvalQuestionSetDetail(details, "content", firstNonEmptyString(document.Message, document.Subject, document.Body, document.Title, document.Summary, document.ApprovalReason, document.Reason))
	approvalQuestionSetDetail(details, "message", document.Message)
	approvalQuestionSetDetail(details, "subject", document.Subject)
	approvalQuestionSetDetail(details, "body", document.Body)
	approvalQuestionSetDetail(details, "title", document.Title)
	approvalQuestionSetDetail(details, "summary", document.Summary)
	approvalQuestionSetDetail(details, "reason", document.Reason)
	approvalQuestionSetDetail(details, "approvalReason", document.ApprovalReason)
	approvalQuestionSetDetail(details, "slug", document.Slug)
	approvalQuestionSetDetail(details, "siteID", document.SiteID)
	approvalQuestionSetDetail(details, "eventID", document.EventID)
	filePath := firstNonEmptyString(document.Path, document.DevicePath, document.TargetPath)
	approvalQuestionSetDetail(details, "path", filePath)
	if strings.TrimSpace(filePath) != "" {
		approvalQuestionSetDetail(details, "fileName", filepath.Base(filePath))
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

func approvalQuestionSetDetail(details map[string]string, key string, value string) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return
	}
	details[key] = trimmedValue
}

func copyJSONRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage{}, value...)
}
