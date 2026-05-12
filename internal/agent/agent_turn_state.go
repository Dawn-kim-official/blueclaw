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
		observation := turnObservation{
			ObservationID: nextObservationID(len(state.Observations) + 1),
			Action:        "policy",
			Content:       invalidCompletionArtifactObservationContent(completionState),
			IsError:       true,
		}
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
	return llm.StructuredResponseRequest{
		Messages: (PromptAssembler{}).BuildTurnMessages(
			state.Request,
			state.Observations,
			buildAgentSystemInstruction(state.Request),
			buildAgentToolDescription(state.Request.ToolSet),
		),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_agent_turn_action",
			Document:           actionSchemaForToolSet(state.Request.ToolSet, allowQualityCriteria, blockedToolNames),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: state.Options.GenerationOptions,
	}
}

func actionSchemaForToolSet(toolSet *ToolSet, allowQualityCriteria bool, blockedToolNames map[string]bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames)
	}
	return toolSet.ActionSchema(allowQualityCriteria, blockedToolNames)
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
		Content:         result.Content,
		Summary:         buildToolResultSummary(invocation.ToolName, result.Content, result.IsError, result.Attachments, "", result),
		IsError:         result.IsError,
		Message:         result.Message,
		ErrorCode:       result.ErrorCode,
		FailureStage:    result.FailureStage,
		Retryable:       result.Retryable,
		SafeRetry:       result.SafeRetry,
		ToolInputKey:    toolInputKey,
		RecoveryActions: append([]RecoveryAction{}, result.RecoveryActions...),
	}
	if observation.IsError {
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.ErrorCode)
	}
	if !result.IsError {
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
		var observation turnObservation
		if json.Unmarshal([]byte(event.Body), &observation) == nil && strings.TrimSpace(observation.ObservationID) != "" {
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
		if observation.Action == "call_tool" && !observation.IsError {
			count++
		}
	}
	return count
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
