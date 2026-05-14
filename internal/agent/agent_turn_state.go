package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

type agentAction = turnActionDocument

type agentTaskState struct {
	TaskRunID       string
	Status          task.TaskStatus
	Request         AgentTurnRequest
	Options         TurnOptions
	Observations    []turnObservation
	QualityCriteria []qualityCriterion
	Attachments     []FileAttachment
	IterationCount  int
	ToolCallCount   int
	TurnStartedAt   time.Time
	PendingWait     *agentPendingWait
	Requirements    []toolUseRequirement
}

type agentPendingWait struct {
	Kind    agentPendingWaitKind
	Message string
	Reason  string
}

type agentPendingWaitKind string

const (
	agentPendingWaitUserInput agentPendingWaitKind = "user_input"
	agentPendingWaitApproval  agentPendingWaitKind = "approval"
)

type agentUserReply struct {
	Text string
}

type agentEffectKind string

const (
	agentEffectNone            agentEffectKind = "none"
	agentEffectCallModel       agentEffectKind = "call_model"
	agentEffectCallTool        agentEffectKind = "call_tool"
	agentEffectWaitForUser     agentEffectKind = "wait_for_user"
	agentEffectWaitForApproval agentEffectKind = "wait_for_approval"
	agentEffectFinalReply      agentEffectKind = "final_reply"
	agentEffectFail            agentEffectKind = "fail"
)

type agentEffect struct {
	Kind       agentEffectKind
	ModelCall  *llm.StructuredResponseRequest
	ToolCall   *ToolInvocation
	UserWait   *agentPendingWait
	FinalReply *agentFinalReply
	Failure    *agentFailure
}

type agentFinalReply struct {
	Reply       string
	Attachments []FileAttachment
}

type agentFailure struct {
	Reason string
}

type agentEvent struct {
	Name string
	Body string
}

type agentTransition struct {
	State  agentTaskState
	Effect agentEffect
	Events []agentEvent
}

func buildInitialAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRunID string) agentTaskState {
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}
	return agentTaskState{
		TaskRunID:      taskRunID,
		Status:         task.TaskStatusRunning,
		Request:        request,
		Options:        normalizeTurnOptions(options),
		TurnStartedAt:  request.TurnStartedAt,
		Requirements:   deriveToolUseRequirements(request),
		Observations:   []turnObservation{},
		Attachments:    []FileAttachment{},
		ToolCallCount:  0,
		IterationCount: 0,
	}
}

func restoreAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRun task.TaskRun, events []task.TaskEvent) (agentTaskState, error) {
	state := buildInitialAgentTaskState(request, options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	state.Observations = observationsFromTaskEvents(events)
	state.Attachments = attachmentsFromObservations(state.Observations)
	state.ToolCallCount = successfulToolCallCount(state.Observations)
	state.IterationCount = len(state.Observations)
	return state, nil
}

func advanceAgentTask(state agentTaskState) agentTransition {
	completionState := buildCompletionState(state.Request, state.Requirements, state.Observations)
	switch completionState.RecommendedAction {
	case completionActionAttachExistingArtifacts:
		return agentTransition{
			State: state,
			Effect: agentEffect{
				Kind: agentEffectCallTool,
				ToolCall: &ToolInvocation{
					ToolName: "file.attach",
					Input:    MarshalToolInput(map[string]any{"paths": completionState.AttachmentPaths}),
				},
			},
		}
	case completionActionFinalizeWithEvidence:
		actionDocument := completionStateFinalReplyDocument(completionState)
		return agentTransition{
			State: state,
			Effect: agentEffect{
				Kind: agentEffectFinalReply,
				FinalReply: &agentFinalReply{
					Reply:       strings.TrimSpace(actionDocument.FinalReply),
					Attachments: attachmentsFromAttachedEvidence(completionState.AttachedEvidence),
				},
			},
		}
	case completionActionBlockedInvalidArtifact:
		observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "policy", "", invalidCompletionArtifactObservationContent(completionState), FailureInvalidInput, NewFailureCode(FailureCodeParts{Domain: "artifact", Reason: "validity_failed"}), "completion_state")
		state.Observations = append(state.Observations, observation)
		return agentTransition{State: state, Effect: agentEffect{Kind: agentEffectNone}}
	default:
		request := BuildAgentActionRequest(state)
		return agentTransition{State: state, Effect: agentEffect{Kind: agentEffectCallModel, ModelCall: &request}}
	}
}

func BuildAgentActionRequest(state agentTaskState) llm.StructuredResponseRequest {
	allowQualityCriteria := len(state.QualityCriteria) == 0
	requirements := state.Requirements
	if requirements == nil {
		requirements = deriveToolUseRequirements(state.Request)
	}
	blockedToolNames := blockedToolNamesForPreconditions(state.Request.ToolSet, requirements, state.Observations)
	failureFacts := buildFailureReportFacts(state.Observations, state.Options.RecoveryBudget)
	hasFailureDebt := len(failureFacts.Attempts) > 0
	messages := (PromptAssembler{}).BuildTurnMessages(
		state.Request,
		state.Observations,
		buildAgentSystemInstruction(state.Request),
		buildAgentToolDescription(state.Request.ToolSet),
	)
	if hasFailureDebt {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: failureDebtActionContractMessage(failureFacts),
		})
	}
	return llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_agent_turn_action",
			Document:           actionSchemaForToolSet(state.Request.ToolSet, allowQualityCriteria, blockedToolNames, hasFailureDebt),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: state.Options.GenerationOptions,
	}
}

func actionSchemaForToolSet(toolSet *ToolSet, allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames, hasFailureDebt)
	}
	return toolSet.ActionSchema(allowQualityCriteria, blockedToolNames, hasFailureDebt)
}

func ParseAgentActionResponse(response llm.StructuredResponse) (agentAction, error) {
	var actionDocument turnActionDocument
	errorValue := json.Unmarshal([]byte(response.Content), &actionDocument)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	if strings.TrimSpace(actionDocument.Action) == "" && strings.TrimSpace(actionDocument.Reply) != "" {
		actionDocument.Action = "final_reply"
		actionDocument.FinalReply = actionDocument.Reply
	}
	return actionDocument, nil
}

func DecideAgentAction(ctx context.Context, languageModel llm.LanguageModelProvider, state agentTaskState) (agentAction, error) {
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, BuildAgentActionRequest(state))
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return ParseAgentActionResponse(structuredResponse)
}

func applyAgentAction(state agentTaskState, action agentAction) (agentTaskState, error) {
	switch strings.TrimSpace(action.Action) {
	case "set_quality_criteria":
		state.QualityCriteria = normalizeQualityCriteria(action.QualityCriteria)
	case "call_tool":
		state.ToolCallCount++
	case "final_reply":
		state.Status = task.TaskStatusCompleted
	case "fail":
		state.Status = task.TaskStatusFailed
	}
	return state, nil
}

func applyToolResult(state agentTaskState, invocation ToolInvocation, result ToolResult) agentTaskState {
	result = normalizeToolFailureResult(invocation.ToolName, result)
	toolInputKey := canonicalToolCallKey(invocation.ToolName, invocation.Input)
	observation := turnObservation{
		ObservationID:   nextObservationID(len(state.Observations) + 1),
		Action:          "call_tool",
		Tool:            strings.TrimSpace(invocation.ToolName),
		Output:          result.Output,
		Failure:         result.Failure,
		Summary:         buildToolResultSummary(invocation.ToolName, result.ContentText(), result.Failed(), result.Attachments, "", result),
		ToolInputKey:    toolInputKey,
		RecoveryActions: append([]RecoveryAction{}, result.RecoveryActions...),
	}
	if observation.Failed() {
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.FailureCode())
	}
	if !result.Failed() {
		observation.Attachments = append([]FileAttachment{}, result.Attachments...)
		state.Attachments = appendObservationAttachments(state.Attachments, observation)
	}
	state.Observations = append(state.Observations, observation)
	return state
}

func applyUserReply(state agentTaskState, reply agentUserReply) (agentTaskState, error) {
	if state.PendingWait == nil {
		return state, nil
	}
	state.PendingWait = nil
	state.Status = task.TaskStatusRunning
	state.Request.VisibleContext.Messages = append(state.Request.VisibleContext.Messages, VisibleContextMessage{
		Speaker: state.Request.RequesterName,
		Text:    strings.TrimSpace(reply.Text),
	})
	return state, nil
}

func qualityCriteriaForActionRequest(allowQualityCriteria bool) []qualityCriterion {
	if allowQualityCriteria {
		return nil
	}
	return []qualityCriterion{{ID: "existing", Description: "existing criteria"}}
}

func observationsFromTaskEvents(events []task.TaskEvent) []turnObservation {
	observations := []turnObservation{}
	for _, event := range events {
		if !strings.HasPrefix(event.Name, "tool.") || !strings.HasSuffix(event.Name, ".result") {
			continue
		}
		observation, errorValue := decodeTurnObservation([]byte(event.Body))
		if errorValue == nil && strings.TrimSpace(observation.ObservationID) != "" {
			observations = append(observations, observation)
		}
	}
	return observations
}

func attachmentsFromObservations(observations []turnObservation) []FileAttachment {
	attachments := []FileAttachment{}
	for _, observation := range observations {
		attachments = appendObservationAttachments(attachments, observation)
	}
	return attachments
}

func successfulToolCallCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		if observation.Action == "call_tool" && !observation.Failed() {
			count++
		}
	}
	return count
}

type legacyTurnObservation struct {
	ObservationID        string           `json:"observationID"`
	Action               string           `json:"action"`
	Tool                 string           `json:"tool,omitempty"`
	Content              string           `json:"content"`
	Summary              string           `json:"summary,omitempty"`
	IsError              bool             `json:"isError"`
	Message              string           `json:"message,omitempty"`
	ErrorCode            string           `json:"errorCode,omitempty"`
	FailureStage         string           `json:"failureStage,omitempty"`
	Retryable            bool             `json:"retryable,omitempty"`
	SafeRetry            bool             `json:"safeRetry,omitempty"`
	ToolInputKey         string           `json:"toolInputKey,omitempty"`
	AttemptFingerprint   string           `json:"attemptFingerprint,omitempty"`
	RecoveryAttemptKey   string           `json:"recoveryAttemptKey,omitempty"`
	RecoveryStep         string           `json:"recoveryStep,omitempty"`
	RecoveryAttemptSpent bool             `json:"recoveryAttemptSpent,omitempty"`
	Attachments          []FileAttachment `json:"attachments,omitempty"`
	RecoveryActions      []RecoveryAction `json:"recoveryActions,omitempty"`
}

func decodeTurnObservation(document []byte) (turnObservation, error) {
	var observation turnObservation
	if errorValue := json.Unmarshal(document, &observation); errorValue != nil {
		return turnObservation{}, errorValue
	}
	if observation.Output.Content != "" || len(observation.Output.Data) > 0 || observation.Failure != nil {
		return observation, nil
	}
	var legacyObservation legacyTurnObservation
	if errorValue := json.Unmarshal(document, &legacyObservation); errorValue != nil {
		return turnObservation{}, errorValue
	}
	return legacyObservation.toTurnObservation(), nil
}

func (legacyObservation legacyTurnObservation) toTurnObservation() turnObservation {
	observation := turnObservation{
		ObservationID:        legacyObservation.ObservationID,
		Action:               legacyObservation.Action,
		Tool:                 legacyObservation.Tool,
		Output:               ToolOutput{Content: legacyObservation.Content},
		Summary:              legacyObservation.Summary,
		ToolInputKey:         legacyObservation.ToolInputKey,
		AttemptFingerprint:   legacyObservation.AttemptFingerprint,
		RecoveryAttemptKey:   legacyObservation.RecoveryAttemptKey,
		RecoveryStep:         legacyObservation.RecoveryStep,
		RecoveryAttemptSpent: legacyObservation.RecoveryAttemptSpent,
		Attachments:          append([]FileAttachment{}, legacyObservation.Attachments...),
		RecoveryActions:      append([]RecoveryAction{}, legacyObservation.RecoveryActions...),
	}
	if legacyObservation.IsError {
		observation.Failure = &ToolFailure{
			Kind:            FailureUnknown,
			Code:            normalizeFailureCode(FailureCodeLiteral(legacyObservation.ErrorCode)),
			Stage:           strings.TrimSpace(legacyObservation.FailureStage),
			UserSafeSummary: firstNonEmptyString(strings.TrimSpace(legacyObservation.Message), strings.TrimSpace(legacyObservation.Content)),
			Retryable:       legacyObservation.Retryable,
			SafeRetry:       legacyObservation.SafeRetry,
		}
	}
	return observation
}

func attachmentsFromAttachedEvidence(evidence []CompletionAttachedEvidence) []FileAttachment {
	attachments := []FileAttachment{}
	for _, item := range evidence {
		attachments = append(attachments, FileAttachment{
			DevicePath:  item.DevicePath,
			Filename:    item.Filename,
			ContentType: item.ContentType,
			SizeBytes:   item.SizeBytes,
			Title:       item.Title,
		})
	}
	return attachments
}
