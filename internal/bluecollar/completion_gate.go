package bluecollar

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

type completionEvidenceReference struct {
	ObservationID   string `json:"observationID"`
	ToolName        string `json:"toolName"`
	AttachmentIndex *int   `json:"attachmentIndex,omitempty"`
}

type qualityCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type qualityReviewItem struct {
	ID          string                        `json:"id"`
	Passed      bool                          `json:"passed"`
	EvidenceIDs []string                      `json:"evidenceIDs"`
	Evidence    []completionEvidenceReference `json:"evidence"`
	Notes       string                        `json:"notes,omitempty"`
}

type completionGateResult struct {
	IsSatisfied        bool
	Message            string
	EvidenceKind       string
	Attachments        []FileAttachment
	ValidityState      ValidityState
	SuggestedNextTools []string
	IsJudgeVerdict     bool
}

const (
	evidenceKindExpectedResult   = "expected_result_missing"
	evidenceKindRequiredTool     = "required_tool_missing"
	evidenceKindAttachment       = "attachment_missing"
	evidenceKindAttachmentValid  = "attachment_invalid"
	evidenceKindReference        = "evidence_reference_invalid"
	completionReplySchemaName    = "blueclaw_completion_reply"
	completionPersistenceTimeout = 5 * time.Second
)

type completionTransition struct {
	Observations  []turnObservation
	Attachments   []FileAttachment
	Result        AgentTurnResult
	IsCompleted   bool
	DidTransition bool
	Action        completionRecommendedAction
}

func (agentTurnRunner *AgentTurnRunner) applyCompletionState(ctx context.Context, taskRunID string, taskStepID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, lastModelMessage string) completionTransition {
	state := buildCompletionState(request, requirements, observations)
	agentState := agentTaskState{
		TaskRunID:       taskRunID,
		Request:         request,
		Observations:    append([]turnObservation{}, observations...),
		Attachments:     append([]FileAttachment{}, attachments...),
		QualityCriteria: append([]qualityCriterion{}, criteria...),
		Requirements:    append([]toolUseRequirement{}, requirements...),
		TurnStartedAt:   request.TurnStartedAt,
		ToolCallCount:   len(observations),
		IterationCount:  len(observations),
	}
	transition := advanceAgentTask(agentState)
	switch transition.Effect.Kind {
	case agentEffectContinue:
		if transition.Effect.ToolCall != nil && IsArtifactDeliveryTool(transition.Effect.ToolCall.ToolName) {
			return agentTurnRunner.attachCompletionArtifactsFromEffect(ctx, taskRunID, request, observations, attachments, state, *transition.Effect.ToolCall)
		}
	case agentEffectFinish:
		return agentTurnRunner.finalizeCompletionState(ctx, taskRunID, taskStepID, request, requirements, observations, attachments, criteria, state, lastModelMessage)
	case agentEffectNone:
		if len(transition.State.Observations) > len(observations) {
			return agentTurnRunner.blockInvalidCompletionArtifactsFromTransition(taskRunID, observations, attachments, state, transition)
		}
	default:
		return completionTransition{Observations: observations, Attachments: attachments}
	}
	return completionTransition{Observations: observations, Attachments: attachments}
}

func (agentTurnRunner *AgentTurnRunner) attachCompletionArtifacts(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, state CompletionState) completionTransition {
	files := []map[string]string{}
	for _, path := range nextCompletionAttachmentPaths(state) {
		files = append(files, map[string]string{"path": path})
	}
	return agentTurnRunner.attachCompletionArtifactsFromEffect(ctx, taskRunID, request, observations, attachments, state, ToolInvocation{
		ToolName: FileDeliverToolName,
		Input:    MarshalToolInput(map[string]any{"files": files}),
	})
}

func (agentTurnRunner *AgentTurnRunner) attachCompletionArtifactsFromEffect(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, state CompletionState, invocation ToolInvocation) completionTransition {
	agentTurnRunner.appendValidityReview(taskRunID, "pre_attach", state.ValidityState)
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, nextObservationIDForObservations(observations), invocation.ToolName, invocation.Input, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, "")
	if observation.Failed() {
		observation = withObservationContent(observation, completionAttachmentFailureContent(observation.ContentText(), state.AttachmentPaths))
		observation.RelatedPaths = appendUniqueStrings(state.AttachmentPaths)
	}
	observations = append(observations, observation)
	attachments = appendObservationAttachments(attachments, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_transition", marshalEventBody(map[string]any{
		"action":        completionActionAttachExistingArtifacts,
		"observationID": observation.ObservationID,
		"artifactCount": len(state.AttachmentPaths),
	}))
	return completionTransition{
		Observations:  observations,
		Attachments:   attachments,
		DidTransition: true,
		Action:        completionActionAttachExistingArtifacts,
	}
}

func (agentTurnRunner *AgentTurnRunner) blockInvalidCompletionArtifacts(taskRunID string, observations []turnObservation, attachments []FileAttachment, state CompletionState) completionTransition {
	observation := newFailureObservation(nextObservationIDForObservations(observations), "policy", "", invalidCompletionArtifactObservationContent(state), FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
	observation.PolicyCode = evidenceKindAttachmentValid
	observation.RelatedPaths = appendUniqueStrings(completionValidityPaths(state))
	observations = append(observations, observation)
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", state.ValidityState)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_required", marshalEventBody(observation))
	return completionTransition{
		Observations:  observations,
		Attachments:   attachments,
		DidTransition: true,
		Action:        completionActionBlockedInvalidArtifact,
	}
}

func (agentTurnRunner *AgentTurnRunner) blockInvalidCompletionArtifactsFromTransition(taskRunID string, observations []turnObservation, attachments []FileAttachment, state CompletionState, transition agentTransition) completionTransition {
	nextObservations := transition.State.Observations
	observation := nextObservations[len(nextObservations)-1]
	observation.PolicyCode = evidenceKindAttachmentValid
	observation.RelatedPaths = appendUniqueStrings(completionValidityPaths(state))
	nextObservations[len(nextObservations)-1] = observation
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", state.ValidityState)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_required", marshalEventBody(observation))
	return completionTransition{
		Observations:  nextObservations,
		Attachments:   attachments,
		DidTransition: true,
		Action:        completionActionBlockedInvalidArtifact,
	}
}

func invalidCompletionArtifactObservationContent(state CompletionState) string {
	lines := []string{validityFailureMessage(state.ValidityState)}
	for _, path := range completionValidityPaths(state) {
		if strings.TrimSpace(path) != "" {
			lines = append(lines, "path: "+strings.TrimSpace(path))
		}
	}
	return strings.Join(lines, "\n")
}

func completionAttachmentFailureContent(content string, paths []string) string {
	trimmedContent := strings.TrimSpace(content)
	if len(paths) == 0 {
		return trimmedContent
	}
	if trimmedContent == "" {
		trimmedContent = FileDeliverToolName + " failed"
	}
	return trimmedContent + "\nrequested paths: " + strings.Join(paths, "\n")
}

func (agentTurnRunner *AgentTurnRunner) finalizeCompletionState(ctx context.Context, taskRunID string, taskStepID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, state CompletionState, lastModelMessage string) completionTransition {
	if ctx.Err() != nil {
		return completionTransition{Observations: observations, Attachments: attachments}
	}
	modelWording := deliverableModelWording(lastModelMessage)
	if modelWording == "" {
		chatCompleter, isAvailable := llm.ResolveTextChatCompleter(agentTurnRunner.languageModel)
		if !isAvailable {
			return completionTransition{Observations: observations, Attachments: attachments}
		}
		var errorValue error
		modelWording, errorValue = generateCompletionReply(ctx, chatCompleter, request, requirements, observations)
		if errorValue != nil {
			agentTurnRunner.appendEvent(taskRunID, "agent.completion_reply_failed", marshalEventBody(map[string]string{"error": errorValue.Error()}))
			if ctx.Err() != nil || errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) {
				return completionTransition{Observations: observations, Attachments: attachments}
			}
			modelWording = elapsedClosingRawReply(request, true)
		}
	}
	actionDocument := completionStateFinishDocument(state, modelWording)
	completionGateResult := agentTurnRunner.validateCompletionGateWithJudge(ctx, taskRunID, request, requirements, observations, attachments, criteria, actionDocument)
	agentTurnRunner.appendValidityReview(taskRunID, "completion_state", completionGateResult.ValidityState)
	if !completionGateResult.IsSatisfied {
		if canDeliverBestEffortOnJudgeRejection(ctx, completionGateResult, modelWording) {
			agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_best_effort", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
			return agentTurnRunner.finalizeCompletionTransition(ctx, taskRunID, taskStepID, request, observations, attachments, completionGateResult, appendCompletionGateCaveat(modelWording, completionGateResult.Message))
		}
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_rejected", marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
		observation := newFailureObservation(nextObservationIDForObservations(observations), "policy", "", completionGateResult.Message, FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
		observation = withCompletionGateRecoveryPacket(observation, completionGateResult)
		observations = append(observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_required", marshalEventBody(observation))
		return completionTransition{Observations: observations, Attachments: attachments}
	}
	agentTurnRunner.appendQualityReview(taskRunID, criteria, actionDocument.QualityReview, observations)
	agentTurnRunner.appendEvent(taskRunID, "agent.completion_state_finalized", marshalEventBody(map[string]any{
		"attachmentCount": len(completionGateResult.Attachments),
		"evidenceCount":   len(state.EvidenceReferences),
		"evidence":        state.EvidenceReferences,
	}))
	return agentTurnRunner.finalizeCompletionTransition(ctx, taskRunID, taskStepID, request, observations, attachments, completionGateResult, finishActionMessage(actionDocument))
}

func (agentTurnRunner *AgentTurnRunner) finalizeCompletionTransition(ctx context.Context, taskRunID string, taskStepID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, completionGateResult completionGateResult, reply string) completionTransition {
	result := agentTurnRunner.completeTaskRunBestEffort(ctx, taskRunID, taskStepID, "completion_state "+string(completionActionFinalizeWithEvidence), request, observations, completionGateResult, reply)
	return completionTransition{
		Observations:  observations,
		Attachments:   appendUniqueAttachments(attachments, completionGateResult.Attachments),
		Result:        result,
		IsCompleted:   true,
		DidTransition: true,
		Action:        completionActionFinalizeWithEvidence,
	}
}

func canDeliverBestEffortOnJudgeRejection(ctx context.Context, completionGateResult completionGateResult, reply string) bool {
	return completionGateResult.IsJudgeVerdict && ctx.Err() != nil && strings.TrimSpace(reply) != ""
}

func appendCompletionGateCaveat(reply string, message string) string {
	trimmedMessage := strings.TrimSpace(message)
	if trimmedMessage == "" {
		return strings.TrimSpace(reply)
	}
	return strings.TrimSpace(reply) + " Note: " + trimmedMessage
}

func (agentTurnRunner *AgentTurnRunner) completeTaskRunBestEffort(ctx context.Context, taskRunID string, taskStepID string, stepAction string, request AgentTurnRequest, observations []turnObservation, completionGateResult completionGateResult, reply string) AgentTurnResult {
	detachedContext, cancelDetached := context.WithTimeout(context.WithoutCancel(ctx), completionPersistenceTimeout)
	defer cancelDetached()
	finalReply := agentTurnRunner.prepareFinishMessageForPlatform(detachedContext, request, reply)
	agentTurnRunner.saveStep(taskRunID, taskStepID, task.TaskStatusCompleted, stepAction, finalReply)
	completedTaskRun, completionError := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, finalReply)
	if completionError != nil {
		agentTurnRunner.appendEvent(taskRunID, "agent.completion_persist_failed", marshalEventBody(map[string]string{"error": completionError.Error()}))
	}
	return AgentTurnResult{
		TaskRun:         completedTaskRun,
		FinishMessage:   finalReply,
		Attachments:     completionGateResult.Attachments,
		RecoveryActions: recoveryActionsFromObservations(observations),
	}
}

func generateCompletionReply(ctx context.Context, chatCompleter llm.ChatCompleter, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation) (string, error) {
	response, errorValue := chatCompleter.GenerateChatCompletion(ctx, llm.ChatCompletionRequest{
		SchemaName: completionReplySchemaName,
		Messages: []llm.ChatCompletionMessage{{
			Role:    "user",
			Content: buildCompletionReplyPrompt(request, requirements, observations),
		}},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return llm.ChatCompletionText(response)
}

func buildCompletionReplyPrompt(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation) string {
	return strings.Join([]string{
		"Write the final user-facing reply for a request whose required result is complete.",
		responseLanguageInstruction(request.ResponseLanguage),
		"State only what the successful evidence proves. Do not mention tools, evidence identifiers, prompts, or runtime details.",
		"Original request:\n" + completionReplyOriginalRequest(request),
		"Successful evidence:\n" + buildLimitObservationSummary(completionPromptObservations(requirements, observations)),
	}, "\n\n")
}

func completionReplyOriginalRequest(request AgentTurnRequest) string {
	return firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt)
}

func completionStateFinishDocument(state CompletionState, message string) turnActionDocument {
	goalSatisfied := true
	return turnActionDocument{
		Action:             "finish",
		Message:            message,
		ReplyParts:         []AgentPart{{Type: AgentPartTypeText, Text: message}},
		CompletionSummary:  message,
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		CompletionEvidence: state.EvidenceReferences,
	}
}

func deliverableModelWording(message string) string {
	return strings.TrimSpace(message)
}

func appendObservationAttachments(attachments []FileAttachment, observation turnObservation) []FileAttachment {
	if observation.Failed() || len(observation.Attachments) == 0 {
		return attachments
	}
	nextAttachments := append([]FileAttachment{}, attachments...)
	if observation.Tool == "browser.screenshot" {
		nextAttachments = removeBrowserScreenshotAttachments(nextAttachments)
	}
	for _, attachment := range observation.Attachments {
		if strings.TrimSpace(attachment.DevicePath) == "" || hasAttachmentDevicePath(nextAttachments, attachment.DevicePath) {
			continue
		}
		nextAttachments = append(nextAttachments, attachment)
	}
	return nextAttachments
}

func removeBrowserScreenshotAttachments(attachments []FileAttachment) []FileAttachment {
	filteredAttachments := []FileAttachment{}
	for _, attachment := range attachments {
		if strings.HasPrefix(strings.TrimSpace(attachment.Filename), "browser-screenshot-") {
			continue
		}
		filteredAttachments = append(filteredAttachments, attachment)
	}
	return filteredAttachments
}

func hasAttachmentDevicePath(attachments []FileAttachment, devicePath string) bool {
	normalizedDevicePath := strings.TrimSpace(devicePath)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.DevicePath) == normalizedDevicePath {
			return true
		}
	}
	return false
}

func completionRequirementsHaveEvidence(toolSet *ToolSet, requirements []toolUseRequirement, observations []turnObservation) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		isSatisfied, _ := completionRequirementStatus(toolSet, requirement, observations)
		if !isSatisfied {
			return false
		}
	}
	return true
}

func validateCompletionGate(toolSet *ToolSet, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	_ = criteria
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "finish requires goalSatisfied=true"}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "finish requires goalStatus=satisfied"}
	}
	if result := validateFinishDoesNotHideUnresolvedWork(observations, actionDocument); !result.IsSatisfied {
		return result
	}
	if errorValue := validateObservedToolRequirements(toolSet, requirements, observations); errorValue != nil {
		return completionGateResult{Message: errorValue.Error(), EvidenceKind: evidenceKindRequiredTool}
	}
	attachments, errorValue := validateCompletionEvidence(toolSet, requirements, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error(), EvidenceKind: evidenceKindReference}
	}
	if sendCompletionEvidenceRequiredForTools(toolSet, requirements) && !hasSendCompletionEvidence(toolSet, observations, actionDocument.CompletionEvidence) {
		requiredSendToolNames := requiredSendToolNamesForRequirements(toolSet, requirements)
		return completionGateResult{
			Message:            sendCompletionEvidenceRequiredMessage(requiredSendToolNames),
			EvidenceKind:       evidenceKindRequiredTool,
			SuggestedNextTools: requiredSendToolNames,
		}
	}
	return completionGateResult{IsSatisfied: true, Attachments: attachments}
}

func validateFinishDoesNotHideUnresolvedWork(observations []turnObservation, actionDocument turnActionDocument) completionGateResult {
	_ = observations
	if actionDocument.HasRemainingWork {
		return completionGateResult{Message: "finish requires hasRemainingWork=false; recover the work or use fail"}
	}
	return completionGateResult{IsSatisfied: true}
}

func validateCompletionGateForRequest(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	return validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, defaultRecoveryBudget())
}

func validateCompletionGateForRequestWithExpectedResults(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, actionDocument turnActionDocument, recoveryBudget RecoveryBudget) completionGateResult {
	var result completionGateResult
	if len(request.OutcomeContract.ExpectedResults) == 0 {
		result = validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, recoveryBudget)
	} else {
		result = validateExpectedResultCompletionGate(request, observations, criteria, actionDocument, recoveryBudget)
	}
	if !result.IsSatisfied {
		return result
	}
	if contractResult := validateOutcomeContractRequirements(request.OutcomeContract, observations, result.Attachments); !contractResult.IsSatisfied {
		return contractResult
	}
	return validateExpectedResultDelivery(request, observations, result.Attachments, actionDocument)
}

func validateOutcomeContractRequirements(contract OutcomeContract, observations []turnObservation, attachments []FileAttachment) completionGateResult {
	contract = normalizeOutcomeContract(contract)
	for _, toolName := range contract.RequiredEvidenceTools {
		if !hasSuccessfulEvidenceToolObservation(observations, toolName) {
			return missingContractToolResult([]string{toolName})
		}
	}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		if !hasAnySuccessfulEvidenceToolObservation(observations, toolNames) {
			return missingContractToolResult(toolNames)
		}
	}
	if contractRequiresAttachment(contract) && len(attachments) == 0 {
		return completionGateResult{Message: "finish requires a delivered file attachment", EvidenceKind: evidenceKindAttachment, SuggestedNextTools: []string{FileDeliverToolName}}
	}
	if missingSuffix := missingRequiredAttachmentSuffix(attachments, contract.RequiredAttachmentSuffixes); missingSuffix != "" {
		return completionGateResult{Message: "required file attachment must include suffix " + missingSuffix, EvidenceKind: evidenceKindAttachmentValid, SuggestedNextTools: []string{FileDeliverToolName}}
	}
	return completionGateResult{IsSatisfied: true, Attachments: attachments}
}

func hasAnySuccessfulEvidenceToolObservation(observations []turnObservation, toolNames []string) bool {
	for _, toolName := range toolNames {
		if hasSuccessfulEvidenceToolObservation(observations, toolName) {
			return true
		}
	}
	return false
}

func hasSuccessfulEvidenceToolObservation(observations []turnObservation, toolName string) bool {
	return hasSuccessfulToolObservationForTurn(observations, toolName)
}

func missingContractToolResult(toolNames []string) completionGateResult {
	return completionGateResult{
		Message:            "finish requires successful evidence from one of these tools: " + strings.Join(toolNames, ", "),
		EvidenceKind:       evidenceKindRequiredTool,
		SuggestedNextTools: appendUniqueStrings(nil, toolNames...),
	}
}

func contractRequiresAttachment(contract OutcomeContract) bool {
	return strings.TrimSpace(contract.ArtifactRequirement) == ArtifactRequirementRequired || len(contract.RequiredAttachmentSuffixes) > 0
}

func validateExpectedResultCompletionGate(request AgentTurnRequest, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument, recoveryBudget RecoveryBudget) completionGateResult {
	_ = criteria
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "finish requires goalSatisfied=true"}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "finish requires goalStatus=satisfied"}
	}
	if result := validateFinishDoesNotHideUnresolvedWork(observations, actionDocument); !result.IsSatisfied {
		return result
	}
	attachments, errorValue := validateCompletionEvidence(request.ToolSet, nil, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error(), EvidenceKind: evidenceKindReference}
	}
	if externalSendCompletionEvidenceRequired(request) && !outcomeContractRequiresPublicLinkOnly(request.OutcomeContract) && !hasSendCompletionEvidence(request.ToolSet, observations, actionDocument.CompletionEvidence) {
		requiredSendToolNames := requiredSendToolNamesForRequest(request)
		return completionGateResult{
			Message:            sendCompletionEvidenceRequiredMessage(requiredSendToolNames),
			EvidenceKind:       evidenceKindRequiredTool,
			SuggestedNextTools: requiredSendToolNames,
		}
	}
	if expectedResultRequiresFileAttachment(request.OutcomeContract) && len(attachments) == 0 {
		return completionGateResult{
			Message:            "required file expected result must cite file.deliver completionEvidence",
			EvidenceKind:       evidenceKindAttachment,
			SuggestedNextTools: []string{FileDeliverToolName},
		}
	}
	if missingSuffix := missingRequiredAttachmentSuffix(attachments, request.OutcomeContract.RequiredAttachmentSuffixes); len(attachments) > 0 && missingSuffix != "" {
		return completionGateResult{
			Message:            "required file expected result must include attachment suffix " + missingSuffix,
			EvidenceKind:       evidenceKindAttachmentValid,
			SuggestedNextTools: []string{FileDeliverToolName},
		}
	}
	if expectedResultRequiresTool(request.OutcomeContract, AskInputToolName) && !hasSuccessfulToolObservationForTurn(observations, AskInputToolName) {
		return completionGateResult{
			Message:            "required interactive choice expected result must use ask.input",
			EvidenceKind:       evidenceKindRequiredTool,
			SuggestedNextTools: []string{AskInputToolName},
		}
	}
	if projectionResult := validateObservedResultProjection(request, observations, attachments, actionDocument); !projectionResult.IsSatisfied {
		return projectionResult
	}
	result := completionGateResult{IsSatisfied: true, Attachments: attachments}
	result.ValidityState = buildAttachmentValidityState(request.WorkspaceRootPath, result.Attachments)
	if !result.ValidityState.Passed {
		result.IsSatisfied = false
		result.Message = validityFailureMessage(result.ValidityState)
		result.EvidenceKind = evidenceKindAttachmentValid
		result.Attachments = nil
	}
	return result
}

func expectedResultRequiresFileAttachment(contract OutcomeContract) bool {
	if strings.TrimSpace(contract.ArtifactRequirement) == ArtifactRequirementRequired {
		return true
	}
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if result.Required && result.Type == ExpectedResultTypeFile {
			return true
		}
	}
	return false
}

func expectedResultRequiresTool(contract OutcomeContract, toolName string) bool {
	normalizedToolName := strings.TrimSpace(toolName)
	if normalizedToolName == "" {
		return false
	}
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if !result.Required {
			continue
		}
		for _, hint := range result.AcceptanceHints {
			if ToolNamesMatch(hint, normalizedToolName) {
				return true
			}
		}
	}
	return false
}

func externalSendCompletionEvidenceRequired(request AgentTurnRequest) bool {
	return contractRequiresSendTool(request.ToolSet, request.OutcomeContract) ||
		sendToolNamesContain(request.ToolSet, request.RequiredEvidenceTools)
}

func sendCompletionEvidenceRequiredForTools(toolSet *ToolSet, requirements []toolUseRequirement) bool {
	for _, requirement := range requirements {
		if isSendEvidenceTool(toolSet, requirement.ToolName) {
			return true
		}
	}
	return false
}

func sendToolNamesContain(toolSet *ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if isSendEvidenceTool(toolSet, toolName) {
			return true
		}
	}
	return false
}

func requiredSendToolNamesForRequirements(toolSet *ToolSet, requirements []toolUseRequirement) []string {
	toolNames := []string{}
	for _, requirement := range requirements {
		if isSendEvidenceTool(toolSet, requirement.ToolName) {
			toolNames = appendUniqueStrings(toolNames, requirement.ToolName)
		}
	}
	return toolNames
}

func requiredSendToolNamesForRequest(request AgentTurnRequest) []string {
	toolNames := sendEvidenceToolsFromValues(request.ToolSet, request.RequiredEvidenceTools)
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = sendEvidenceToolsFromValues(request.ToolSet, outcomeContractRequiredToolNames(request.OutcomeContract))
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = sendEvidenceToolsFromValues(request.ToolSet, request.OutcomeContract.SelectedEvidenceHints)
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = singleAvailableSendEvidenceTool(request.ToolSet)
	if len(toolNames) > 0 {
		return toolNames
	}
	return []string{"message.send"}
}

func sendCompletionEvidenceRequiredMessage(toolNames []string) string {
	if len(toolNames) == 0 {
		return "finish requires completionEvidence from a successful send tool observation; call a send tool to perform the actual send, then cite that observation"
	}
	return "finish requires completionEvidence from a successful send tool observation; call one of these tools to perform the actual send, then cite that observation: " + strings.Join(toolNames, ", ")
}

func hasSendCompletionEvidence(toolSet *ToolSet, observations []turnObservation, references []completionEvidenceReference) bool {
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if isFound && isSendEvidenceTool(toolSet, observation.Tool) {
			return true
		}
	}
	return false
}

func hasSuccessfulToolObservationForTurn(observations []turnObservation, toolName string) bool {
	normalizedToolName := strings.TrimSpace(toolName)
	if normalizedToolName == "" {
		return false
	}
	for _, observation := range observations {
		if ToolNamesMatch(observation.Tool, normalizedToolName) && !observation.Failed() {
			return true
		}
	}
	return false
}

func validateCompletionGateForRequestWithRecoveryBudget(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument, recoveryBudget RecoveryBudget) completionGateResult {
	requirements = requirementsWithFailureDebtWaiver(requirements, observations, actionDocument)
	result := validateCompletionGate(request.ToolSet, requirements, observations, criteria, actionDocument)
	if !result.IsSatisfied {
		return result
	}
	if externalSendCompletionEvidenceRequired(request) && !hasSendCompletionEvidence(request.ToolSet, observations, actionDocument.CompletionEvidence) {
		result.IsSatisfied = false
		result.SuggestedNextTools = requiredSendToolNamesForRequest(request)
		result.Message = sendCompletionEvidenceRequiredMessage(result.SuggestedNextTools)
		result.EvidenceKind = evidenceKindRequiredTool
		result.Attachments = nil
		return result
	}
	if projectionResult := validateObservedResultProjection(request, observations, result.Attachments, actionDocument); !projectionResult.IsSatisfied {
		return projectionResult
	}
	result.ValidityState = buildAttachmentValidityState(request.WorkspaceRootPath, result.Attachments)
	if !result.ValidityState.Passed {
		result.IsSatisfied = false
		result.Message = validityFailureMessage(result.ValidityState)
		result.EvidenceKind = evidenceKindAttachmentValid
		result.Attachments = nil
		return result
	}
	return result
}

func validateObservedResultProjection(request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, actionDocument turnActionDocument) completionGateResult {
	projection := buildObservedResultProjection(request, observations, attachments, actionDocument)
	if len(projection.MissingRequirements) == 0 {
		return completionGateResult{IsSatisfied: true, Attachments: attachments}
	}
	return completionGateResult{
		Message:            observedProjectionGateMessage(projection.MissingRequirements),
		EvidenceKind:       evidenceKindExpectedResult,
		SuggestedNextTools: observedProjectionSuggestedTools(projection.MissingRequirements),
	}
}

func observedProjectionGateMessage(requirements []ProjectionMissingRequirement) string {
	descriptions := []string{}
	for _, requirement := range requirements {
		descriptions = append(descriptions, strings.TrimSpace(requirement.Description))
	}
	return "finish is not backed by observed results: " + strings.Join(nonEmptyStrings(descriptions), "; ")
}

func observedProjectionSuggestedTools(requirements []ProjectionMissingRequirement) []string {
	toolNames := []string{}
	for _, requirement := range requirements {
		toolNames = appendUniqueStrings(toolNames, requirement.SuggestedNextTools...)
	}
	return toolNames
}

func requirementsWithFailureDebtWaiver(requirements []toolUseRequirement, observations []turnObservation, actionDocument turnActionDocument) []toolUseRequirement {
	if strings.TrimSpace(actionDocument.FailureResolution) != failureResolutionNoToolFallback {
		return requirements
	}
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return requirements
	}
	failedToolName := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	if failedToolName == "" {
		return requirements
	}
	filteredRequirements := []toolUseRequirement{}
	for _, requirement := range requirements {
		if canWaiveRequirementWithNoToolFallback(requirement, failedToolName) {
			continue
		}
		filteredRequirements = append(filteredRequirements, requirement)
	}
	return filteredRequirements
}

func canWaiveRequirementWithNoToolFallback(requirement toolUseRequirement, failedToolName string) bool {
	if requirement.RequiresAttachment || strings.TrimSpace(requirement.ToolName) != failedToolName {
		return false
	}
	return !requirement.RequiresSideEffectEvidence
}

func completionGateObservation(index int, result completionGateResult, priorObservations []turnObservation) turnObservation {
	message := strings.TrimSpace(result.Message)
	evidenceKind := strings.TrimSpace(result.EvidenceKind)
	if evidenceKind == "" {
		return newFailureObservation(nextObservationID(index), "policy", "", message, FailureInvalidInput, FailureCodes.InvalidInput, "completion_gate")
	}
	content := evidenceMissingGuidance(evidenceKind, message) + observedRealityStatement(priorObservations)
	observation := newFailureObservation(nextObservationID(index), "evidence_missing", "", message, FailureInvalidInput, FailureCodes.InvalidInput, evidenceKind)
	observation = withObservationContent(observation, content)
	observation.Summary = content
	observation.PolicyCode = evidenceKind
	observation.RelatedPaths = invalidValidityPaths(result.ValidityState)
	observation.Failure.Retryable = true
	observation.Failure.SafeRetry = true
	return observation
}

func observedRealityStatement(observations []turnObservation) string {
	successfulToolCount := 0
	for _, observation := range observations {
		if !observation.Failed() && strings.TrimSpace(observation.Tool) != "" {
			successfulToolCount++
		}
	}
	if successfulToolCount > 0 {
		return ""
	}
	return " Recorded reality: this task has ZERO successful tool observations, so nothing has been created or modified. Any completion claim is false. Your next action must be the required tool call, not finish."
}

func invalidValidityPaths(state ValidityState) []string {
	paths := []string{}
	for _, artifact := range state.InvalidArtifacts {
		paths = appendUniqueStrings(paths, artifact.RelativePath, artifact.Filename)
	}
	return paths
}

func withCompletionGateRecoveryPacket(observation turnObservation, result completionGateResult) turnObservation {
	if strings.TrimSpace(result.Message) == "" && len(result.SuggestedNextTools) == 0 {
		return observation
	}
	observation.RecoveryPacket = &RecoveryPacket{
		WhatFailed:       "Expected task result is not satisfied yet.",
		WhyLikely:        result.Message,
		FailureClass:     failureClassUnknown,
		RetryPolicy:      retryPolicyAfterPrecondition,
		AllowedTools:     appendUniqueStrings(result.SuggestedNextTools),
		EvidenceNeeded:   expectedResultRecoveryEvidence(result),
		MustDoNext:       []string{"Produce or inspect the missing expected result, then try finish again."},
		ForbiddenRepeats: nil,
	}
	return observation
}

func expectedResultRecoveryEvidence(result completionGateResult) []string {
	return []string{result.Message}
}

func completionGateEventName(observation turnObservation) string {
	if observation.Action == "evidence_missing" {
		return "agent.evidence_missing"
	}
	return "agent.completion_required"
}

func evidenceMissingGuidance(evidenceKind string, message string) string {
	switch evidenceKind {
	case "expected_result_missing":
		return "The Task expected result is not complete yet. Produce or inspect the missing result, then finish with exact typed delivery evidence. " + message
	case "required_tool_missing":
		return "The final reply needs successful tool evidence before completion. Use the required tool if it has not run, or cite an existing successful observation. " + message
	case "attachment_missing":
		return "The final reply needs an attached artifact before completion. Find or create the artifact, then use file.deliver before finish. " + message
	case "attachment_invalid":
		return "The final reply needs valid attachment evidence. Recheck the artifact path and required suffix, then attach a valid file. " + message
	case "evidence_reference_invalid":
		return "The final reply cited missing or failed evidence. Cite only existing successful observations, or run the missing tool first. " + message
	default:
		return message
	}
}

func validateCompletionEvidence(toolSet *ToolSet, requirements []toolUseRequirement, observations []turnObservation, references []completionEvidenceReference) ([]FileAttachment, error) {
	if len(requirements) == 0 {
		if errorValue := validateCompletionEvidenceReferences(toolSet, observations, references); errorValue != nil {
			return nil, errorValue
		}
		return collectReferenceDeliveryAttachments(observations, references), nil
	}
	attachments := collectReferenceDeliveryAttachments(observations, references)
	eligibleReferences := completionEvidenceEligibleReferences(toolSet, observations, references)
	for _, requirement := range requirements {
		if !requirement.RequiresAttachment {
			continue
		}
		matchingReferences := completionReferencesForRequirement(requirement, observations, eligibleReferences)
		if len(matchingReferences) == 0 {
			return nil, errors.New("completionEvidence must cite successful observation for " + requirementLabel(requirement))
		}
		requirementAttachments := collectReferenceAttachments(observations, matchingReferences)
		if len(requirementAttachments) == 0 {
			return nil, errors.New("completionEvidence for " + requirementLabel(requirement) + " must include an attachment")
		}
		if missingSuffix := missingRequiredAttachmentSuffix(requirementAttachments, requirement.AttachmentSuffixes); missingSuffix != "" {
			return nil, errors.New("completionEvidence for " + requirementLabel(requirement) + " must include attachment suffix " + missingSuffix)
		}
	}
	return attachments, nil
}

func completionEvidenceEligibleReferences(toolSet *ToolSet, observations []turnObservation, references []completionEvidenceReference) []completionEvidenceReference {
	eligibleReferences := make([]completionEvidenceReference, 0, len(references))
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if isFound && observationSatisfiesEvidenceCondition(toolSet, observation) {
			eligibleReferences = append(eligibleReferences, reference)
		}
	}
	return eligibleReferences
}

func validateObservedToolRequirements(toolSet *ToolSet, requirements []toolUseRequirement, observations []turnObservation) error {
	for _, requirement := range requirements {
		if requirement.RequiresAttachment {
			continue
		}
		isSatisfied, _ := completionRequirementStatus(toolSet, requirement, observations)
		if !isSatisfied {
			return errors.New("finish requires successful observation for " + requirementLabel(requirement))
		}
	}
	return nil
}

func missingRequiredAttachmentSuffix(attachments []FileAttachment, suffixes []string) string {
	missingSuffixes := missingRequiredAttachmentSuffixes(attachments, suffixes)
	if len(missingSuffixes) == 0 {
		return ""
	}
	return missingSuffixes[0]
}

func missingRequiredAttachmentSuffixes(attachments []FileAttachment, suffixes []string) []string {
	missingSuffixes := []string{}
	for _, suffix := range suffixes {
		if !attachmentsContainSuffix(attachments, suffix) {
			missingSuffixes = append(missingSuffixes, suffix)
		}
	}
	return missingSuffixes
}

func attachmentsContainSuffix(attachments []FileAttachment, suffix string) bool {
	for _, attachment := range attachments {
		if attachmentMatchesSuffix(attachment, suffix) {
			return true
		}
	}
	return false
}

func attachmentMatchesSuffix(attachment FileAttachment, suffix string) bool {
	return strings.HasSuffix(attachment.Filename, suffix) || strings.HasSuffix(attachment.DevicePath, suffix)
}

func validateCompletionEvidenceReferences(toolSet *ToolSet, observations []turnObservation, references []completionEvidenceReference) error {
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound || !observationSatisfiesEvidenceCondition(toolSet, observation) {
			return errors.New("completionEvidence references an unknown or failed observation")
		}
	}
	return nil
}

func completionReferencesForRequirement(requirement toolUseRequirement, observations []turnObservation, references []completionEvidenceReference) []completionEvidenceReference {
	matchingReferences := []completionEvidenceReference{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound {
			continue
		}
		if requirementMatchesObservation(requirement, observation) {
			matchingReferences = append(matchingReferences, reference)
		}
	}
	return matchingReferences
}

func matchingCompletionObservations(requirement toolUseRequirement, observations []turnObservation) []turnObservation {
	matchingObservations := []turnObservation{}
	for _, observation := range observations {
		if observation.Failed() || !requirementMatchesObservation(requirement, observation) {
			continue
		}
		if requirement.RequiresAttachment && len(observation.Attachments) == 0 {
			continue
		}
		matchingObservations = append(matchingObservations, observation)
	}
	return matchingObservations
}

func requirementMatchesObservation(requirement toolUseRequirement, observation turnObservation) bool {
	toolName := strings.TrimSpace(observation.Tool)
	if strings.TrimSpace(requirement.ToolName) == "" {
		return false
	}
	return ToolNamesMatch(toolName, requirement.ToolName)
}

func findSuccessfulObservation(observations []turnObservation, reference completionEvidenceReference) (turnObservation, bool) {
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.ObservationID) != strings.TrimSpace(reference.ObservationID) {
			continue
		}
		if strings.TrimSpace(reference.ToolName) != "" && !ToolNamesMatch(observation.Tool, reference.ToolName) {
			continue
		}
		return observation, true
	}
	return turnObservation{}, false
}

func collectReferenceAttachments(observations []turnObservation, references []completionEvidenceReference) []FileAttachment {
	attachments := []FileAttachment{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound {
			continue
		}
		attachments = appendUniqueAttachments(attachments, attachmentsForReference(observation, reference))
	}
	return attachments
}

func collectReferenceDeliveryAttachments(observations []turnObservation, references []completionEvidenceReference) []FileAttachment {
	attachments := []FileAttachment{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound || !toolProducesDeliveryAttachments(observation.Tool) {
			continue
		}
		attachments = appendUniqueAttachments(attachments, attachmentsForReference(observation, reference))
	}
	return attachments
}

func toolProducesDeliveryAttachments(toolName string) bool {
	if IsArtifactDeliveryTool(toolName) {
		return true
	}
	return strings.TrimSpace(toolName) == "browser.screenshot"
}

func attachmentsForReference(observation turnObservation, reference completionEvidenceReference) []FileAttachment {
	if reference.AttachmentIndex == nil {
		return observation.Attachments
	}
	index := *reference.AttachmentIndex
	if index < 0 || index >= len(observation.Attachments) {
		return nil
	}
	return []FileAttachment{observation.Attachments[index]}
}

func observationActionCounts(observations []turnObservation) map[string]int {
	counts := map[string]int{}
	for _, observation := range observations {
		action := strings.TrimSpace(observation.Action)
		if action == "" {
			action = "unknown"
		}
		counts[action]++
	}
	return counts
}

func observationToolCounts(observations []turnObservation) map[string]int {
	counts := map[string]int{}
	for _, observation := range observations {
		toolName := strings.TrimSpace(observation.Tool)
		if toolName == "" {
			continue
		}
		counts[toolName]++
	}
	return counts
}

func appendUniqueAttachments(attachments []FileAttachment, candidates []FileAttachment) []FileAttachment {
	nextAttachments := append([]FileAttachment{}, attachments...)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.DevicePath) == "" || hasAttachmentDevicePath(nextAttachments, candidate.DevicePath) {
			continue
		}
		nextAttachments = append(nextAttachments, candidate)
	}
	return nextAttachments
}

func requirementLabel(requirement toolUseRequirement) string {
	return strings.TrimSpace(requirement.ToolName)
}
