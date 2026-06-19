package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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
	ExecutionState  ExecutionState
	NextStepPlan    NextStepPlan
	ContextSummary  TaskContextSummary
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
	agentEffectContinue        agentEffectKind = "continue"
	agentEffectWaitForUser     agentEffectKind = "wait_for_user"
	agentEffectWaitForApproval agentEffectKind = "wait_for_approval"
	agentEffectFinish          agentEffectKind = "finish"
	agentEffectFail            agentEffectKind = "fail"
)

type agentEffect struct {
	Kind      agentEffectKind
	ModelCall *llm.StructuredResponseRequest
	ToolCall  *ToolInvocation
	UserWait  *agentPendingWait
	Finish    *agentFinish
	Failure   *agentFailure
}

type agentFinish struct {
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

func agentTaskStateForTurn(request AgentTurnRequest, options TurnOptions, taskRun task.TaskRun, events []task.TaskEvent) (agentTaskState, error) {
	if !request.IsRuntimeRestartResume {
		state := buildInitialAgentTaskState(request, options, taskRun.TaskRunID)
		state.Status = taskRun.Status
		return state, nil
	}
	return restoreAgentTaskState(request, options, taskRun, events)
}

func restoreAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRun task.TaskRun, events []task.TaskEvent) (agentTaskState, error) {
	if shouldCleanRestartRestoredTask(events) {
		return cleanRestartedAgentTaskState(request, options, taskRun, events), nil
	}
	state := buildInitialAgentTaskState(request, options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	state.Observations = observationsFromTaskEvents(events)
	state.Attachments = attachmentsFromObservations(state.Observations)
	state.ExecutionState = executionStateFromTaskEvents(events)
	state.NextStepPlan = nextStepPlanFromTaskEvents(events)
	state.ContextSummary = taskContextSummaryFromTaskEvents(events)
	state.ToolCallCount = successfulToolCallCount(state.Observations)
	state.IterationCount = len(state.Observations)
	return state, nil
}

func shouldCleanRestartRestoredTask(events []task.TaskEvent) bool {
	lastStallIndex := -1
	for index, event := range events {
		switch event.Name {
		case "agent.no_progress_loop_stopped", "agent.no_progress_loop_paused", "agent.limit_stop":
			lastStallIndex = index
		}
	}
	if lastStallIndex == -1 {
		return false
	}
	for index := lastStallIndex + 1; index < len(events); index++ {
		if events[index].Name == "task.steer.requested" {
			return true
		}
	}
	return false
}

func cleanRestartedAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRun task.TaskRun, events []task.TaskEvent) agentTaskState {
	state := buildInitialAgentTaskState(scrubRestoredGoalContext(request), options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	durableObservations := durableDeliveryObservations(events)
	state.Observations = append(durableObservations, regroundingObservation(len(durableObservations)+1))
	state.Attachments = attachmentsFromObservations(state.Observations)
	return state
}

func scrubRestoredGoalContext(request AgentTurnRequest) AgentTurnRequest {
	request.ActiveGoal.KnownContext = []string{"The prior attempt on this task stalled and its working notes were cleared. Ignore the earlier trajectory and earlier tool outputs; re-ground from the current workspace state via site.app.status and the source on disk before acting."}
	return request
}

func durableDeliveryObservations(events []task.TaskEvent) []turnObservation {
	durable := []turnObservation{}
	for _, observation := range observationsFromTaskEvents(events) {
		if observation.Failed() {
			continue
		}
		if isDurableDeliveryObservation(observation) {
			durable = append(durable, observation)
		}
	}
	return durable
}

func isDurableDeliveryObservation(observation turnObservation) bool {
	if len(observation.Attachments) > 0 {
		return true
	}
	switch strings.TrimSpace(observation.Tool) {
	case "site.app.publish", "site.app.promote":
		return true
	default:
		return false
	}
}

func regroundingObservation(index int) turnObservation {
	message := "The previous attempt on this task stalled without finishing, and its working notes were cleared to avoid repeating the same mistakes. Your file edits on disk are preserved. Re-ground before acting: resolve the current site with site.app.status (empty input resolves the conversation's site), read the current source from disk, and continue from there. Do not trust earlier tool outputs; verify the current state first."
	observation := newContentObservation(nextObservationID(index), "policy", "", marshalEventBody(map[string]string{
		"regrounded": "true",
		"directive":  message,
	}))
	observation.Summary = message
	return observation
}

func advanceAgentTask(state agentTaskState) agentTransition {
	completionState := buildCompletionState(state.Request, state.Requirements, state.Observations)
	switch completionState.RecommendedAction {
	case completionActionAttachExistingArtifacts:
		return agentTransition{
			State: state,
			Effect: agentEffect{
				Kind: agentEffectContinue,
				ToolCall: &ToolInvocation{
					ToolName: "file.attach",
					Input:    MarshalToolInput(map[string]any{"path": nextCompletionAttachmentPath(completionState)}),
				},
			},
		}
	case completionActionFinalizeWithEvidence:
		actionDocument := completionStateFinishDocument(completionState)
		return agentTransition{
			State: state,
			Effect: agentEffect{
				Kind: agentEffectFinish,
				Finish: &agentFinish{
					Reply:       finishActionMessage(actionDocument),
					Attachments: attachmentsFromAttachedEvidence(completionState.AttachedEvidence),
				},
			},
		}
	case completionActionBlockedInvalidArtifact:
		observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "policy", "", invalidCompletionArtifactObservationContent(completionState), FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
		state.Observations = append(state.Observations, observation)
		return agentTransition{State: state, Effect: agentEffect{Kind: agentEffectNone}}
	default:
		request := BuildAgentActionRequest(state)
		return agentTransition{State: state, Effect: agentEffect{Kind: agentEffectCallModel, ModelCall: &request}}
	}
}

func nextCompletionAttachmentPath(state CompletionState) string {
	paths := nextCompletionAttachmentPaths(state)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func nextCompletionAttachmentPaths(state CompletionState) []string {
	attachedPathByName := map[string]bool{}
	paths := []string{}
	for _, evidence := range state.AttachedEvidence {
		if strings.TrimSpace(evidence.DevicePath) != "" {
			attachedPathByName[strings.TrimSpace(evidence.DevicePath)] = true
		}
		if strings.TrimSpace(evidence.Filename) != "" {
			attachedPathByName[strings.TrimSpace(evidence.Filename)] = true
		}
	}
	for _, path := range state.AttachmentPaths {
		trimmedPath := strings.TrimSpace(path)
		if trimmedPath == "" {
			continue
		}
		if attachedPathByName[trimmedPath] || attachedPathByName[filepath.Base(trimmedPath)] {
			continue
		}
		paths = append(paths, trimmedPath)
	}
	return paths
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
	allowFail := shouldExposeFailAction(state)
	messages := (PromptAssembler{}).BuildTurnMessages(
		state.Request,
		state.Observations,
		buildAgentSystemInstruction(state.Request),
		buildAgentToolDescription(state.Request.ToolSet),
		state.ExecutionState,
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
			Document:           actionSchemaForToolSet(state.Request.ToolSet, allowQualityCriteria, blockedToolNames, hasFailureDebt, allowFail),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: state.Options.GenerationOptions,
	}
}

func shouldExposeFailAction(state agentTaskState) bool {
	failureDebt, hasFailureDebt := activeFailureDebt(state.Observations)
	if !hasFailureDebt {
		return true
	}
	return recoveryToolBudgetExhaustedForRequest(state.Observations, state.Request.ToolSet, state.Options.RecoveryBudget, failureDebt)
}

func actionSchemaForToolSet(toolSet *ToolSet, allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool, allowFailValues ...bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames, hasFailureDebt, allowFailValues...)
	}
	return toolSet.ActionSchema(allowQualityCriteria, blockedToolNames, hasFailureDebt, allowFailValues...)
}

func ParseAgentActionResponse(response llm.StructuredResponse) (agentAction, error) {
	content, errorValue := normalizeAgentActionResponseContent([]byte(response.Content))
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	var actionDocument turnActionDocument
	errorValue = json.Unmarshal(content, &actionDocument)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return normalizeParsedAction(actionDocument), nil
}

func normalizeAgentActionResponseContent(content []byte) ([]byte, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(content, &document); errorValue != nil {
		return nil, errorValue
	}
	if _, hasAction := document["action"]; hasAction {
		return content, nil
	}
	candidateAction, candidateCount := agentActionResponseCandidate(document)
	if candidateCount == 0 {
		return content, nil
	}
	if candidateCount > 1 {
		return nil, errors.New("action response contains multiple candidate action blocks")
	}
	return injectAgentActionResponseCandidate(document, candidateAction)
}

func agentActionResponseCandidate(document map[string]json.RawMessage) (string, int) {
	actionNames := []string{"finish", "continue", "fail", "request_tools", "set_quality_criteria"}
	candidateAction := ""
	candidateCount := 0
	for _, actionName := range actionNames {
		if _, isPresent := document[actionName]; !isPresent {
			continue
		}
		candidateAction = actionName
		candidateCount++
	}
	return candidateAction, candidateCount
}

func injectAgentActionResponseCandidate(document map[string]json.RawMessage, actionName string) ([]byte, error) {
	normalizedDocument := map[string]json.RawMessage{}
	for fieldName, fieldValue := range document {
		normalizedDocument[fieldName] = fieldValue
	}
	var nestedDocument map[string]json.RawMessage
	if json.Unmarshal(document[actionName], &nestedDocument) == nil {
		for fieldName, fieldValue := range nestedDocument {
			if _, isPresent := normalizedDocument[fieldName]; isPresent {
				continue
			}
			normalizedDocument[fieldName] = fieldValue
		}
	}
	actionValue, errorValue := json.Marshal(actionName)
	if errorValue != nil {
		return nil, errorValue
	}
	normalizedDocument["action"] = actionValue
	return json.Marshal(normalizedDocument)
}

func normalizeParsedAction(actionDocument turnActionDocument) turnActionDocument {
	actionDocument = normalizeParsedEvidence(actionDocument)
	action := strings.TrimSpace(actionDocument.Action)
	switch action {
	case "continue":
		actionDocument.Action = "continue"
	case "finish":
		actionDocument.Action = "finish"
	default:
		actionDocument.Action = action
	}
	return actionDocument
}

func normalizeParsedEvidence(actionDocument turnActionDocument) turnActionDocument {
	if len(actionDocument.CompletionEvidence) == 0 {
		actionDocument.CompletionEvidence = evidenceReferencesFromIDs(actionDocument.CompletionEvidenceIDs)
	}
	for index, item := range actionDocument.QualityReview {
		if len(item.Evidence) == 0 {
			item.Evidence = evidenceReferencesFromIDs(item.EvidenceIDs)
			actionDocument.QualityReview[index] = item
		}
	}
	return actionDocument
}

func evidenceReferencesFromIDs(values []string) []completionEvidenceReference {
	references := []completionEvidenceReference{}
	seenReferences := map[string]bool{}
	for _, value := range values {
		observationID := strings.TrimSpace(value)
		if observationID == "" || seenReferences[observationID] {
			continue
		}
		seenReferences[observationID] = true
		references = append(references, completionEvidenceReference{ObservationID: observationID})
	}
	return references
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
	case "continue":
		state.ToolCallCount++
	case "finish":
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
		Action:          "continue",
		Tool:            strings.TrimSpace(invocation.ToolName),
		Output:          result.Output,
		Failure:         result.Failure,
		Summary:         modelVisibleToolResultSummary(context.Background(), nil, invocation.ToolName, turnObservation{Tool: invocation.ToolName, Output: result.Output, Failure: result.Failure, Attachments: result.Attachments}),
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
		if isToolCallObservation(observation) && !observation.Failed() {
			count++
		}
	}
	return count
}

func isToolCallObservation(observation turnObservation) bool {
	action := strings.TrimSpace(observation.Action)
	return action == "continue"
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
	RecoveryPacket       *RecoveryPacket  `json:"recoveryPacket,omitempty"`
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
	action := legacyObservation.Action
	observation := turnObservation{
		ObservationID:        legacyObservation.ObservationID,
		Action:               action,
		Tool:                 legacyObservation.Tool,
		Output:               ToolOutput{Content: legacyObservation.Content},
		Summary:              legacyObservation.Summary,
		ToolInputKey:         legacyObservation.ToolInputKey,
		AttemptFingerprint:   legacyObservation.AttemptFingerprint,
		RecoveryAttemptKey:   legacyObservation.RecoveryAttemptKey,
		RecoveryStep:         legacyObservation.RecoveryStep,
		RecoveryAttemptSpent: legacyObservation.RecoveryAttemptSpent,
		RecoveryPacket:       legacyObservation.RecoveryPacket,
		Attachments:          append([]FileAttachment{}, legacyObservation.Attachments...),
		RecoveryActions:      append([]RecoveryAction{}, legacyObservation.RecoveryActions...),
	}
	if legacyObservation.IsError {
		observation.Failure = &ToolFailure{
			Kind:            FailureUnknown,
			Code:            CanonicalFailureCode(FailureCode(legacyObservation.ErrorCode)),
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
