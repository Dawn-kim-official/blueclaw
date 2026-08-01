package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

const defaultAgentActionMaxTokens = 4096
const terminalStructuredMaxTokens = 1600
const maximumAgentActionCorrectionCount = 2

type agentAction = turnActionDocument

type agentTaskState struct {
	TaskRunID                          string
	Status                             task.TaskStatus
	Request                            AgentTurnRequest
	Options                            TurnOptions
	Observations                       []turnObservation
	QualityCriteria                    []qualityCriterion
	Attachments                        []FileAttachment
	ExecutionState                     ExecutionState
	ContextSummary                     TaskContextSummary
	IterationCount                     int
	ToolCallCount                      int
	TurnStartedAt                      time.Time
	PendingWait                        *agentPendingWait
	Requirements                       []toolUseRequirement
	LastModelMessage                   string
	CompletionIntentToolName           string
	ShouldRestrictNextActionToTerminal bool
	DidNudgePlan                       bool
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

func agentTaskStateForTurn(request AgentTurnRequest, options TurnOptions, taskRun task.TaskRun, events []task.TaskEvent, isPausedTaskResume bool) (agentTaskState, error) {
	if !request.IsRuntimeRestartResume && !request.IsApprovalContinuation && !isPausedTaskResume {
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
	if userResumeClearsInheritedFailureDebt(request, state.Observations) {
		state.Observations = observationsWithoutFailures(state.Observations)
	}
	state.Attachments = attachmentsFromObservations(state.Observations)
	state.ExecutionState = executionStateFromTaskEvents(events)
	state.ContextSummary = taskContextSummaryFromTaskEvents(events)
	state.ToolCallCount = successfulToolCallCount(state.Observations)
	state.IterationCount = len(state.Observations)
	return state, nil
}

func userResumeClearsInheritedFailureDebt(request AgentTurnRequest, observations []turnObservation) bool {
	if !request.IsRuntimeRestartResume || request.IsApprovalContinuation {
		return false
	}
	_, hasFailureDebt := activeFailureDebt(observations)
	return hasFailureDebt
}

func observationsWithoutFailures(observations []turnObservation) []turnObservation {
	retained := make([]turnObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		retained = append(retained, observation)
	}
	return retained
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
	state.Observations = append(durableObservations, regroundingObservation(len(durableObservations)+1, producedSourcePaths(events)))
	state.Attachments = attachmentsFromObservations(state.Observations)
	return state
}

func scrubRestoredGoalContext(request AgentTurnRequest) AgentTurnRequest {
	request.ActiveGoal.KnownContext = []string{"The prior attempt on this task stalled and its working notes were cleared. Ignore the earlier trajectory and earlier tool outputs; re-ground from the current workspace state: read the deliverable source and any build or review output already saved on disk, and continue improving that same source in place rather than recreating it from scratch. For a website task, resolve the current site with site.list."}
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

// producedSourcePaths recovers the workspace paths the model was writing or
// editing before the restart, taken from the small result of each successful
// file.write/file.edit (path only, never the file body, so no stale content is
// carried forward). Surfacing them structurally lets the restart continue the
// exact same source in place instead of guessing what it was building.
func producedSourcePaths(events []task.TaskEvent) []string {
	const maxSourcePaths = 8
	seen := map[string]bool{}
	paths := []string{}
	addPath := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] || len(paths) >= maxSourcePaths {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, observation := range observationsFromTaskEvents(events) {
		if observation.Failed() {
			continue
		}
		switch strings.TrimSpace(observation.Tool) {
		case "file.write":
			var result struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(observation.Output.Content), &result) == nil {
				addPath(result.Path)
			}
		case "file.edit":
			var result struct {
				EditedFiles []string `json:"editedFiles"`
			}
			if json.Unmarshal([]byte(observation.Output.Content), &result) == nil {
				for _, path := range result.EditedFiles {
					addPath(path)
				}
			}
		}
	}
	return paths
}

func isDurableDeliveryObservation(observation turnObservation) bool {
	if len(observation.Attachments) > 0 {
		return true
	}
	switch strings.TrimSpace(observation.Tool) {
	case "site.serve":
		return true
	default:
		return false
	}
}

func regroundingObservation(index int, sourcePaths []string) turnObservation {
	message := "The previous attempt on this task stalled without finishing, and its working notes were cleared to avoid repeating the same mistakes. Your file edits on disk are preserved. Re-ground before acting: read the deliverable source you were producing on disk and any build or review output saved beside it, then continue that same workflow — improve the existing source in place rather than recreating it from scratch. If a build, review, or quality score already exists on disk, treat it as your target and iterate toward it. For a website task, resolve the current site with site.list. Do not trust earlier tool outputs; verify the current state first."
	if len(sourcePaths) > 0 {
		message += " The source files you were producing are: " + strings.Join(sourcePaths, ", ") + ". Read these first and keep improving them in place."
	}
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
					ToolName: FileDeliverToolName,
					Input:    completionArtifactDeliveryInput(state, completionState),
				},
			},
		}
	case completionActionFinalizeWithEvidence:
		actionDocument := completionStateFinishDocument(completionState, "")
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
		observation := newFailureObservation(nextObservationIDForObservations(state.Observations), "policy", "", invalidCompletionArtifactObservationContent(completionState), FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
		observation.PolicyCode = evidenceKindAttachmentValid
		observation.RelatedPaths = appendUniqueStrings(completionValidityPaths(completionState))
		state.Observations = append(state.Observations, observation)
		return agentTransition{State: state, Effect: agentEffect{Kind: agentEffectNone}}
	default:
		request := BuildAgentActionRequest(state)
		return agentTransition{State: state, Effect: agentEffect{Kind: agentEffectCallModel, ModelCall: &request}}
	}
}

func completionArtifactDeliveryInput(state agentTaskState, completionState CompletionState) json.RawMessage {
	return MarshalToolInput(map[string]any{"path": nextCompletionAttachmentPath(completionState)})
}

func operationInputSelectsDeliveryFile(requiredInput map[string]any) bool {
	if path, isString := requiredInput["path"].(string); isString && strings.TrimSpace(path) != "" {
		return true
	}
	files, isArray := requiredInput["files"].([]any)
	if !isArray {
		return false
	}
	for _, fileValue := range files {
		file, isObject := fileValue.(map[string]any)
		if !isObject {
			continue
		}
		if path, isString := file["path"].(string); isString && strings.TrimSpace(path) != "" {
			return true
		}
	}
	return false
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
	return buildAgentActionRequest(state, true)
}

func buildAgentActionRequest(state agentTaskState, includeToolDescription bool) llm.StructuredResponseRequest {
	allowQualityCriteria := len(state.QualityCriteria) == 0
	requirements := state.Requirements
	if requirements == nil {
		requirements = deriveToolUseRequirements(state.Request)
	}
	modelToolSet := modelCallableToolSet(state.Request.ToolSet, state.Request.RestrictActionToTerminalOnly)
	blockedToolNames := blockedToolNamesForPreconditions(modelToolSet, requirements, state.Observations)
	failureFacts := buildFailureReportFacts(state.Observations, state.Options.RecoveryBudget)
	hasFailureDebt := len(failureFacts.Attempts) > 0
	allowFail := shouldExposeFailAction(state)
	allowFinish := shouldExposeFinishAction(state, requirements)
	toolDescription := ""
	if includeToolDescription {
		toolDescription = buildAgentToolDescription(modelToolSet)
	}
	messages := (PromptAssembler{}).BuildTurnMessages(
		state.Request,
		state.Observations,
		buildAgentSystemInstruction(state.Request),
		toolDescription,
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
			Document:           actionSchemaForToolSet(modelToolSet, allowQualityCriteria, blockedToolNames, hasFailureDebt, allowFail, allowFinish),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: agentActionGenerationOptions(state.Options.GenerationOptions),
	}
}

func agentActionGenerationOptions(options llm.GenerationOptions) llm.GenerationOptions {
	if options.MaxTokens != nil {
		return options
	}
	maxTokens := defaultAgentActionMaxTokens
	options.MaxTokens = &maxTokens
	return options
}

func terminalStructuredGenerationOptions(options llm.GenerationOptions) llm.GenerationOptions {
	if options.MaxTokens != nil {
		return options
	}
	maxTokens := terminalStructuredMaxTokens
	options.MaxTokens = &maxTokens
	return options
}

func modelCallableToolSet(toolSet *ToolSet, restrictToTerminalActionsOnly bool) *ToolSet {
	if restrictToTerminalActionsOnly {
		return nil
	}
	return toolSet
}

func shouldExposeFailAction(state agentTaskState) bool {
	if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
		return !evaluateRecoveryAllowance(state.Observations, state.Options.RecoveryBudget).CanRecover
	}
	if _, shouldContinue := recoverableWorkflowFailResult(state.Request, state.Observations); shouldContinue {
		return false
	}
	return true
}

func shouldExposeFinishAction(state agentTaskState, requirements []toolUseRequirement) bool {
	if finishWasRejectedWithoutAnyToolEvidence(state.Observations) {
		return false
	}
	if _, hasFailureDebt := activeFailureDebt(state.Observations); !hasFailureDebt {
		return true
	}
	if !evaluateRecoveryAllowance(state.Observations, state.Options.RecoveryBudget).CanRecover {
		return true
	}
	return len(requirements) == 0 || completionRequirementsHaveEvidence(state.Request.ToolSet, requirements, state.Observations)
}

func finishWasRejectedWithoutAnyToolEvidence(observations []turnObservation) bool {
	if len(observations) == 0 {
		return false
	}
	latestObservation := observations[len(observations)-1]
	if latestObservation.Action != "evidence_missing" {
		return false
	}
	switch latestObservation.PolicyCode {
	case "attachment_missing", "attachment_invalid", "required_tool_missing":
		return true
	}
	for _, observation := range observations {
		if !observation.Failed() && strings.TrimSpace(observation.Tool) != "" {
			return false
		}
	}
	return true
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
		return normalizeAgentActionResponseScalarContent(content)
	}
	candidateAction, candidateCount := agentActionResponseCandidate(document)
	if candidateCount == 0 {
		return normalizeAgentActionResponseScalarContent(content)
	}
	if candidateCount > 1 {
		return nil, errors.New("action response contains multiple candidate action blocks")
	}
	injectedContent, errorValue := injectAgentActionResponseCandidate(document, candidateAction)
	if errorValue != nil {
		return nil, errorValue
	}
	return normalizeAgentActionResponseScalarContent(injectedContent)
}

func agentActionResponseCandidate(document map[string]json.RawMessage) (string, int) {
	actionNames := []string{"finish", "continue", "fail", "set_quality_criteria"}
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

func normalizeAgentActionResponseScalarContent(content []byte) ([]byte, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(content, &document); errorValue != nil {
		return nil, errorValue
	}
	didChange := normalizeJSONStringBooleanField(document, "goalSatisfied")
	for _, fieldName := range []string{"completionEvidenceIDs", "qualityCriteria"} {
		if normalizeJSONStringToArrayField(document, fieldName) {
			didChange = true
		}
	}
	if !didChange {
		return content, nil
	}
	return json.Marshal(document)
}

func normalizeJSONStringToArrayField(document map[string]json.RawMessage, fieldName string) bool {
	fieldValue, isPresent := document[fieldName]
	if !isPresent {
		return false
	}
	var stringValue string
	if json.Unmarshal(fieldValue, &stringValue) != nil {
		return false
	}
	arrayValue := []string{}
	for _, item := range strings.Split(stringValue, ",") {
		if trimmedItem := strings.TrimSpace(item); trimmedItem != "" {
			arrayValue = append(arrayValue, trimmedItem)
		}
	}
	marshaledValue, errorValue := json.Marshal(arrayValue)
	if errorValue != nil {
		return false
	}
	document[fieldName] = marshaledValue
	return true
}

func normalizeJSONStringBooleanField(document map[string]json.RawMessage, fieldName string) bool {
	fieldValue, isPresent := document[fieldName]
	if !isPresent {
		return false
	}
	var stringValue string
	if errorValue := json.Unmarshal(fieldValue, &stringValue); errorValue != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(stringValue)) {
	case "true":
		document[fieldName] = json.RawMessage("true")
		return true
	case "false":
		document[fieldName] = json.RawMessage("false")
		return true
	default:
		return false
	}
}

func normalizeParsedAction(actionDocument turnActionDocument) turnActionDocument {
	actionDocument = normalizeParsedEvidence(actionDocument)
	action := strings.TrimSpace(actionDocument.Action)
	switch action {
	case "continue":
		actionDocument.Action = "continue"
		actionDocument.ToolName = strings.TrimSpace(actionDocument.ToolName)
	case "finish":
		actionDocument.Action = "finish"
		actionDocument.ReplyParts = normalizeFinishReplyParts(actionDocument.ReplyParts)
	default:
		actionDocument.Action = action
	}
	return actionDocument
}

func normalizeFinishReplyParts(parts []AgentPart) []AgentPart {
	normalizedParts := []AgentPart{}
	for _, part := range parts {
		part.Type = strings.TrimSpace(part.Type)
		part.Text = strings.TrimSpace(part.Text)
		if part.Type == "" && part.Text != "" {
			part.Type = AgentPartTypeText
		}
		if part.Type != AgentPartTypeText || part.Text == "" {
			continue
		}
		part.Image = nil
		part.File = nil
		normalizedParts = append(normalizedParts, part)
	}
	return normalizedParts
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
	if chatCompleter, isAvailable := llm.ResolveTextChatCompleter(languageModel); isAvailable {
		chatRequestSource := buildAgentActionRequest(state, false)
		if chatRequest, isRepresentable := buildAgentActionChatCompletionRequest(chatRequestSource); isRepresentable {
			return decideAgentActionWithChat(ctx, chatCompleter, chatRequest, state)
		}
	}
	structuredRequest := BuildAgentActionRequest(state)
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, structuredRequest)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return ParseAgentActionResponse(structuredResponse)
}

func decideAgentActionWithChat(ctx context.Context, chatCompleter llm.ChatCompleter, request llm.ChatCompletionRequest, state agentTaskState) (agentAction, error) {
	currentRequest := request
	for correctionCount := 0; ; correctionCount++ {
		response, errorValue := chatCompleter.GenerateChatCompletion(ctx, currentRequest)
		if errorValue == nil {
			return parseNativeAgentActionResponse(response, currentRequest.Tools)
		}
		if errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) || ctx.Err() != nil {
			return turnActionDocument{}, errorValue
		}
		if correctionCount >= maximumAgentActionCorrectionCount {
			return turnActionDocument{}, errorValue
		}
		correction, isCorrectable := llm.StructuredOutputCorrectionFromError(errorValue)
		if !isCorrectable {
			return turnActionDocument{}, errorValue
		}
		retryRequest, canRetry := retryAgentActionChatCompletionRequest(currentRequest, correction, state)
		if !canRetry {
			return turnActionDocument{}, errorValue
		}
		currentRequest = retryRequest
	}
}

func retryAgentActionChatCompletionRequest(request llm.ChatCompletionRequest, correction llm.StructuredOutputCorrection, state agentTaskState) (llm.ChatCompletionRequest, bool) {
	retryRequest := request
	retryRequest.Messages = append([]llm.ChatCompletionMessage{}, request.Messages...)
	retryRequest.Messages = append(retryRequest.Messages, llm.ChatCompletionMessage{
		Role:    "system",
		Content: agentActionCorrectionMessage(correction),
	})
	toolName := strings.TrimSpace(correction.Diagnostic.ToolName)
	if toolName == "" {
		if correction.Diagnostic.Category != llm.StructuredOutputDiagnosticFinishReason ||
			correction.Diagnostic.FinishReason != llm.StructuredOutputDiagnosticFinishStop {
			return retryRequest, true
		}
		toolName = firstPendingActionToolName(state)
		if toolName == "" && agentActionCompletionIsReady(state) {
			toolName = "finish"
		}
		if toolName == "" {
			return retryRequest, true
		}
	}
	return restrictAgentActionChatCompletionRequest(retryRequest, toolName)
}

func restrictAgentActionChatCompletionRequest(request llm.ChatCompletionRequest, toolName string) (llm.ChatCompletionRequest, bool) {
	for _, tool := range request.Tools {
		if tool.Function.Name != toolName {
			continue
		}
		request.Tools = []llm.ChatCompletionTool{tool}
		request.ToolChoice = json.RawMessage(`"required"`)
		request.ParallelToolCalls = false
		return request, true
	}
	return llm.ChatCompletionRequest{}, false
}

func firstPendingActionToolName(state agentTaskState) string {
	if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
		return ""
	}
	return firstPendingRequiredToolName(
		state.Request.ContractToolWorkingSet.RequiredNextTools,
		state.Observations,
	)
}

func agentActionCompletionIsReady(state agentTaskState) bool {
	if agentActionCompletionIsBlocked(state) {
		return false
	}
	requirements := state.Requirements
	if requirements == nil {
		requirements = deriveToolUseRequirements(state.Request)
	}
	completionState := buildCompletionState(state.Request, requirements, state.Observations)
	if completionState.RecommendedAction != completionActionFinalizeWithEvidence {
		return false
	}
	action := completionStateFinishDocument(completionState, "completion wording pending")
	gateResult := validateAgentActionCompletionGate(state, requirements, action)
	if !gateResult.IsSatisfied {
		return false
	}
	return validateOutcomeContractRequirements(
		state.Request.OutcomeContract,
		state.Observations,
		gateResult.Attachments,
	).IsSatisfied
}

func validateAgentActionCompletionGate(state agentTaskState, requirements []toolUseRequirement, action turnActionDocument) completionGateResult {
	if len(state.Request.OutcomeContract.ExpectedResults) > 0 {
		return validateExpectedResultCompletionGate(
			state.Request,
			state.Observations,
			state.QualityCriteria,
			action,
			state.Options.RecoveryBudget,
		)
	}
	return validateCompletionGateForRequestWithRecoveryBudget(
		state.Request,
		requirements,
		state.Observations,
		state.QualityCriteria,
		action,
		state.Options.RecoveryBudget,
	)
}

func agentActionCompletionIsBlocked(state agentTaskState) bool {
	if state.PendingWait != nil {
		return true
	}
	if hasPendingObservedSuggestedNextTool(state.Observations) {
		return true
	}
	_, hasFailureDebt := activeFailureDebt(state.Observations)
	return hasFailureDebt
}

func agentActionCorrectionMessage(correction llm.StructuredOutputCorrection) string {
	diagnostic := correction.Diagnostic
	messageParts := []string{
		"The previous native action response was invalid.",
		"Return exactly one valid tool call.",
		"Diagnostic category: " + string(diagnostic.Category) + ".",
	}
	if diagnostic.ToolName != "" {
		messageParts = append(messageParts, "Expected tool: "+diagnostic.ToolName+".")
	}
	if diagnostic.FinishReason != "" {
		messageParts = append(messageParts, "Observed finish reason: "+string(diagnostic.FinishReason)+".")
	}
	for _, issue := range diagnostic.ValidationIssues {
		messageParts = append(messageParts, "Validation issue: "+issue.FieldPath+" ("+string(issue.Code)+").")
	}
	return strings.Join(messageParts, " ")
}

func buildAgentActionChatCompletionRequest(structuredRequest llm.StructuredResponseRequest) (llm.ChatCompletionRequest, bool) {
	messages := make([]llm.ChatCompletionMessage, 0, len(structuredRequest.Messages))
	for _, message := range structuredRequest.Messages {
		messages = append(messages, llm.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
			Parts:   append([]llm.MessagePart{}, message.Parts...),
		})
	}
	tools, errorValue := nativeAgentActionTools(structuredRequest.StructuredOutputSchema.Document)
	if errorValue != nil || len(tools) == 0 {
		return llm.ChatCompletionRequest{}, false
	}
	return llm.ChatCompletionRequest{
		SchemaName:        agentActionSchemaName,
		Messages:          messages,
		Tools:             tools,
		ToolChoice:        json.RawMessage(`"required"`),
		ParallelToolCalls: false,
		GenerationOptions: structuredRequest.GenerationOptions,
	}, true
}

func nativeAgentActionTools(schemaDocument string) ([]llm.ChatCompletionTool, error) {
	var schema struct {
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
		return nil, errorValue
	}
	tools := make([]llm.ChatCompletionTool, 0, len(schema.OneOf))
	toolNames := map[string]bool{}
	for _, variant := range schema.OneOf {
		tool, errorValue := nativeAgentActionTool(variant)
		if errorValue != nil {
			return nil, errorValue
		}
		if toolNames[tool.Function.Name] {
			return nil, fmt.Errorf("native agent action tool %q is duplicated", tool.Function.Name)
		}
		toolNames[tool.Function.Name] = true
		tools = append(tools, tool)
	}
	return tools, nil
}

func nativeAgentActionTool(variant json.RawMessage) (llm.ChatCompletionTool, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(variant, &document); errorValue != nil {
		return llm.ChatCompletionTool{}, errorValue
	}
	var properties map[string]json.RawMessage
	if errorValue := json.Unmarshal(document["properties"], &properties); errorValue != nil {
		return llm.ChatCompletionTool{}, errors.New("native agent action variant has no properties")
	}
	actionName, errorValue := singleSchemaEnumValue(properties["action"])
	if errorValue != nil {
		return llm.ChatCompletionTool{}, errorValue
	}
	toolName := actionName
	parameters := variant
	switch {
	case actionName == "continue":
		toolName, errorValue = singleSchemaEnumValue(properties["toolName"])
		parameters = properties["toolInput"]
	case isNativeTerminalAction(actionName):
		parameters, errorValue = nativeTerminalActionParameters(document)
	default:
		return llm.ChatCompletionTool{}, fmt.Errorf("native agent action %q is unsupported", actionName)
	}
	if errorValue != nil {
		return llm.ChatCompletionTool{}, errorValue
	}
	if len(parameters) == 0 {
		return llm.ChatCompletionTool{}, errors.New("native agent action parameters are empty")
	}
	var description string
	_ = json.Unmarshal(document["description"], &description)
	return llm.ChatCompletionTool{Type: "function", Function: llm.ChatCompletionFunction{
		Name: toolName, Description: description, Parameters: parameters,
	}}, nil
}

func singleSchemaEnumValue(document json.RawMessage) (string, error) {
	var schema struct {
		Enum []string `json:"enum"`
	}
	if json.Unmarshal(document, &schema) != nil || len(schema.Enum) != 1 || strings.TrimSpace(schema.Enum[0]) == "" {
		return "", errors.New("native agent action discriminator must contain one value")
	}
	return schema.Enum[0], nil
}

func nativeTerminalActionParameters(document map[string]json.RawMessage) (json.RawMessage, error) {
	var properties map[string]json.RawMessage
	if errorValue := json.Unmarshal(document["properties"], &properties); errorValue != nil {
		return nil, errorValue
	}
	delete(properties, "action")
	propertiesDocument, errorValue := json.Marshal(properties)
	if errorValue != nil {
		return nil, errorValue
	}
	document["properties"] = propertiesDocument
	var requiredFields []string
	_ = json.Unmarshal(document["required"], &requiredFields)
	retainedFields := make([]string, 0, len(requiredFields))
	for _, fieldName := range requiredFields {
		if fieldName != "action" {
			retainedFields = append(retainedFields, fieldName)
		}
	}
	requiredDocument, errorValue := json.Marshal(retainedFields)
	if errorValue != nil {
		return nil, errorValue
	}
	document["required"] = requiredDocument
	return json.Marshal(document)
}

func parseNativeAgentActionResponse(response llm.ChatCompletionResponse, tools []llm.ChatCompletionTool) (agentAction, error) {
	if response.FinishReason != "tool_calls" {
		return turnActionDocument{}, fmt.Errorf("native agent action chat finish reason is %q", response.FinishReason)
	}
	if response.Message.Role != "assistant" {
		return turnActionDocument{}, errors.New("native agent action chat message must be assistant")
	}
	if len(response.Message.ToolCalls) == 0 {
		return turnActionDocument{}, errors.New("native agent action chat expected at least one tool call")
	}
	toolCall := response.Message.ToolCalls[0]
	if strings.TrimSpace(toolCall.ID) == "" {
		return turnActionDocument{}, errors.New("native agent action chat tool call ID is empty")
	}
	if toolCall.Type != "function" || !containsNativeAgentTool(tools, toolCall.Function.Name) {
		return turnActionDocument{}, fmt.Errorf("native agent action chat returned unknown tool %q", toolCall.Function.Name)
	}
	input := json.RawMessage(toolCall.Function.Arguments)
	var inputDocument map[string]json.RawMessage
	if json.Unmarshal(input, &inputDocument) != nil || inputDocument == nil {
		return turnActionDocument{}, fmt.Errorf("native agent action tool %q arguments must be an object", toolCall.Function.Name)
	}
	if !isNativeTerminalAction(toolCall.Function.Name) {
		return turnActionDocument{Action: "continue", ToolName: toolCall.Function.Name, ToolInput: input}, nil
	}
	inputDocument["action"], _ = json.Marshal(toolCall.Function.Name)
	normalizedInput, errorValue := json.Marshal(inputDocument)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return ParseAgentActionResponse(llm.StructuredResponse{Content: string(normalizedInput)})
}

func containsNativeAgentTool(tools []llm.ChatCompletionTool, toolName string) bool {
	for _, tool := range tools {
		if tool.Function.Name == toolName {
			return true
		}
	}
	return false
}

func isNativeTerminalAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "finish", "fail", "set_quality_criteria":
		return true
	default:
		return false
	}
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
		ObservationID:   nextObservationIDForObservations(state.Observations),
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
	}
	state.Observations = append(state.Observations, observation)
	state.Attachments = attachmentsFromObservations(state.Observations)
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
		if errorValue == nil && strings.TrimSpace(observation.ObservationID) != "" && !isApprovalRequiredObservation(observation) {
			observations = append(observations, observation)
		}
	}
	return observations
}

// The last successful file.deliver declares what the person receives. Earlier
// deliveries are superseded rather than accumulated, so an agent that delivered
// the wrong file corrects itself by delivering the right set. Without this a
// single mistaken delivery is unrecoverable, and the turn thrashes against an
// instruction to remove an attachment that no tool can carry out.
func attachmentsFromObservations(observations []turnObservation) []FileAttachment {
	lastDeliveryIndex := -1
	for index, observation := range observations {
		if !observation.Failed() && observation.Tool == FileDeliverToolName && len(observation.Attachments) > 0 {
			lastDeliveryIndex = index
		}
	}
	attachments := []FileAttachment{}
	for index, observation := range observations {
		if observation.Tool == FileDeliverToolName && index != lastDeliveryIndex {
			continue
		}
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
