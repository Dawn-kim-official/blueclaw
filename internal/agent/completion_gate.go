package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	IsSatisfied          bool
	Message              string
	Attachments          []FileAttachment
	ValidityState        ValidityState
	ResultVerification   ResultVerification
	ContractVerification ContractSatisfactionVerification
	SuggestedNextTools   []string
}

type completionTransition struct {
	Observations  []turnObservation
	Attachments   []FileAttachment
	Result        AgentTurnResult
	IsCompleted   bool
	DidTransition bool
	Action        completionRecommendedAction
}

func (agentTurnRunner *AgentTurnRunner) applyCompletionState(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment) completionTransition {
	state := buildCompletionState(request, requirements, observations)
	agentState := agentTaskState{
		TaskRunID:      taskRunID,
		Request:        request,
		Observations:   append([]turnObservation{}, observations...),
		Attachments:    append([]FileAttachment{}, attachments...),
		Requirements:   append([]toolUseRequirement{}, requirements...),
		TurnStartedAt:  request.TurnStartedAt,
		ToolCallCount:  len(observations),
		IterationCount: len(observations),
	}
	transition := advanceAgentTask(agentState)
	switch transition.Effect.Kind {
	case agentEffectContinue:
		if transition.Effect.ToolCall != nil && IsArtifactDeliveryTool(transition.Effect.ToolCall.ToolName) {
			return agentTurnRunner.attachCompletionArtifactsFromEffect(ctx, taskRunID, request, observations, attachments, state, *transition.Effect.ToolCall)
		}
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
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, nextObservationID(len(observations)+1), invocation.ToolName, invocation.Input, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, "")
	if observation.Failed() {
		observation = withObservationContent(observation, completionAttachmentFailureContent(observation.ContentText(), state.AttachmentPaths))
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
	observation := newFailureObservation(nextObservationID(len(observations)+1), "policy", "", invalidCompletionArtifactObservationContent(state), FailureInvalidInput, FailureCodes.InvalidInput, "completion_state")
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

// Runtime-composed finish and approval wording is the model's own most-recent
// message (LLM-first), reused from the turn that already produced it so no extra
// model call is spent. When the model left no deliverable message the wording is
// empty rather than a deterministic template.
func deliverableModelWording(message string) string {
	message = strings.TrimSpace(message)
	if message == "" || ValidateUserNoticeDelivery(message) != nil {
		return ""
	}
	return message
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

func completionRequirementsHaveEvidence(requirements []toolUseRequirement, observations []turnObservation) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		isSatisfied, _ := completionRequirementStatus(requirement, observations)
		if !isSatisfied {
			return false
		}
	}
	return true
}

func validateCompletionGate(requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "finish requires goalSatisfied=true"}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "finish requires goalStatus=satisfied"}
	}
	if result := validateFinishTaskState(observations, criteria, actionDocument); !result.IsSatisfied {
		return result
	}
	if errorValue := validateObservedToolRequirements(requirements, observations); errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	attachments, errorValue := validateCompletionEvidence(requirements, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	if sendCompletionEvidenceRequiredForTools(requirements) && !hasSendCompletionEvidence(observations, actionDocument.CompletionEvidence) {
		requiredSendToolNames := requiredSendToolNamesForRequirements(requirements)
		return completionGateResult{
			Message:            sendCompletionEvidenceRequiredMessage(requiredSendToolNames),
			SuggestedNextTools: requiredSendToolNames,
		}
	}
	finishMessage := finishActionMessage(actionDocument)
	requiresAttachmentEvidence := len(attachments) > 0
	if errorValue := ValidateFinishMessageDelivery(finishMessage, attachments, requiresAttachmentEvidence); errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	return completionGateResult{IsSatisfied: true, Attachments: attachments}
}

func validateFinishTaskState(observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	finishMessage := finishActionMessage(actionDocument)
	if finishMessageRepeatsSentCheckpoint(finishMessage, observations) {
		return completionGateResult{Message: "finish.message repeats a sent checkpoint; write a fresh completed-result reply"}
	}
	if _, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt && strings.TrimSpace(actionDocument.FailureResolution) != failureResolutionNoToolFallback {
		return completionGateResult{Message: "finish cannot be satisfied while FailureDebt is active; recover the failure or use fail"}
	}
	if strings.TrimSpace(actionDocument.RemainingWork) != "" {
		return completionGateResult{Message: "finish requires remainingWork to be empty"}
	}
	if errorValue := validateQualityReview(criteria, actionDocument.QualityReview, observations); errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	return completionGateResult{IsSatisfied: true}
}

func finishMessageRepeatsSentCheckpoint(message string, observations []turnObservation) bool {
	normalizedMessage := normalizeCheckpointMessage(message)
	if normalizedMessage == "" {
		return false
	}
	for _, observation := range observations {
		if observation.Action != "checkpoint" || !checkpointObservationWasSent(observation) {
			continue
		}
		if normalizeCheckpointMessage(checkpointObservationMessage(observation)) == normalizedMessage {
			return true
		}
	}
	return false
}

func checkpointObservationWasSent(observation turnObservation) bool {
	var document struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(observation.ContentText()), &document) == nil && strings.TrimSpace(document.Status) == "sent"
}

func validateCompletionGateForRequest(request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	return validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, defaultRecoveryBudget())
}

func (agentTurnRunner *AgentTurnRunner) validateCompletionGateForRequestWithExpectedResults(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []FileAttachment, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	if len(request.OutcomeContract.ExpectedResults) == 0 {
		result := validateCompletionGateForRequestWithRecoveryBudget(request, requirements, observations, criteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
		if !result.IsSatisfied {
			return result
		}
		return agentTurnRunner.verifyCompletionContract(ctx, taskRunID, request, observations, result.Attachments, actionDocument, result)
	}
	result := validateExpectedResultCompletionGate(request, observations, criteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
	if !result.IsSatisfied {
		return result
	}
	verification, errorValue := verifyExpectedResults(ctx, agentTurnRunner.languageModel, request, observations, attachments, actionDocument)
	if errorValue != nil {
		result.IsSatisfied = false
		result.Message = "expected result verification unavailable: " + errorValue.Error()
		agentTurnRunner.appendEvent(taskRunID, "agent.expected_result_verification_unavailable", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return result
	}
	result.ResultVerification = verification
	agentTurnRunner.appendEvent(taskRunID, "agent.expected_result_verification", marshalEventBody(verification))
	missingResults := blockingExpectedResultItems(request.OutcomeContract, verification, observations)
	if len(missingResults) == 0 {
		return agentTurnRunner.verifyCompletionContract(ctx, taskRunID, request, observations, result.Attachments, actionDocument, result)
	}
	result.IsSatisfied = false
	result.Message = expectedResultGateMessage(missingResults)
	result.SuggestedNextTools = suggestedNextToolsForResultVerification(missingResults)
	return result
}

func (agentTurnRunner *AgentTurnRunner) verifyCompletionContract(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, actionDocument turnActionDocument, result completionGateResult) completionGateResult {
	if !OutcomeContractHasRequirements(request.OutcomeContract) {
		return result
	}
	verification, errorValue := verifyContractSatisfaction(ctx, agentTurnRunner.languageModel, request, observations, attachments, actionDocument)
	if errorValue != nil {
		result.IsSatisfied = false
		result.Message = "contract verification unavailable: " + errorValue.Error()
		agentTurnRunner.appendEvent(taskRunID, "agent.contract_verification_unavailable", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return result
	}
	result.ContractVerification = verification
	agentTurnRunner.appendEvent(taskRunID, "agent.contract_satisfaction_verification", marshalEventBody(verification))
	if verification.Satisfied {
		return result
	}
	result.IsSatisfied = false
	result.Message = contractVerificationGateMessage(verification)
	result.SuggestedNextTools = verification.SuggestedNextTools
	result.Attachments = nil
	return result
}

func contractVerificationGateMessage(verification ContractSatisfactionVerification) string {
	description := firstNonEmptyString(verification.MissingDescription, verification.Reason, "requested outcome is not backed by observed evidence")
	return "finish does not satisfy task contract: " + strings.TrimSpace(description)
}

func validateExpectedResultCompletionGate(request AgentTurnRequest, observations []turnObservation, criteria []qualityCriterion, actionDocument turnActionDocument, recoveryBudget RecoveryBudget) completionGateResult {
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "finish requires goalSatisfied=true"}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "finish requires goalStatus=satisfied"}
	}
	if result := validateFinishTaskState(observations, criteria, actionDocument); !result.IsSatisfied {
		return result
	}
	attachments, errorValue := validateCompletionEvidence(nil, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	if externalSendCompletionEvidenceRequired(request) && !outcomeContractRequiresPublicLinkOnly(request.OutcomeContract) && !hasSendCompletionEvidence(observations, actionDocument.CompletionEvidence) {
		requiredSendToolNames := requiredSendToolNamesForRequest(request)
		return completionGateResult{
			Message:            sendCompletionEvidenceRequiredMessage(requiredSendToolNames),
			SuggestedNextTools: requiredSendToolNames,
		}
	}
	if expectedResultRequiresFileAttachment(request.OutcomeContract) && len(attachments) == 0 {
		return completionGateResult{
			Message: "required file expected result must cite file.deliver completionEvidence",
		}
	}
	if missingSuffix := missingRequiredAttachmentSuffix(attachments, request.OutcomeContract.RequiredAttachmentSuffixes); len(attachments) > 0 && missingSuffix != "" {
		return completionGateResult{
			Message: "required file expected result must include attachment suffix " + missingSuffix,
		}
	}
	if expectedResultRequiresTool(request.OutcomeContract, AskInputToolName) && !hasSuccessfulToolObservationForTurn(observations, AskInputToolName) {
		return completionGateResult{
			Message:            "required interactive choice expected result must use ask.input",
			SuggestedNextTools: []string{AskInputToolName},
		}
	}
	finishMessage := finishActionMessage(actionDocument)
	requiresAttachmentEvidence := len(attachments) > 0
	if errorValue := ValidateFinishMessageDelivery(finishMessage, attachments, requiresAttachmentEvidence); errorValue != nil {
		return completionGateResult{Message: errorValue.Error()}
	}
	if projectionResult := validateObservedResultProjection(request, observations, attachments, actionDocument); !projectionResult.IsSatisfied {
		return projectionResult
	}
	result := completionGateResult{IsSatisfied: true, Attachments: attachments}
	result.ValidityState = buildAttachmentValidityState(request.WorkspaceRootPath, result.Attachments)
	if !result.ValidityState.Passed {
		result.IsSatisfied = false
		result.Message = validityFailureMessage(result.ValidityState)
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
	return contractRequiresSendTool(request.OutcomeContract) ||
		sendToolNamesContain(request.RequiredEvidenceTools)
}

func sendCompletionEvidenceRequiredForTools(requirements []toolUseRequirement) bool {
	for _, requirement := range requirements {
		if isSendEvidenceTool(requirement.ToolName) {
			return true
		}
	}
	return false
}

func sendToolNamesContain(toolNames []string) bool {
	for _, toolName := range toolNames {
		if isSendEvidenceTool(toolName) {
			return true
		}
	}
	return false
}

func requiredSendToolNamesForRequirements(requirements []toolUseRequirement) []string {
	toolNames := []string{}
	for _, requirement := range requirements {
		if isSendEvidenceTool(requirement.ToolName) {
			toolNames = appendUniqueStrings(toolNames, requirement.ToolName)
		}
	}
	return toolNames
}

func requiredSendToolNamesForRequest(request AgentTurnRequest) []string {
	toolNames := sendEvidenceToolsFromValues(request.RequiredEvidenceTools)
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = sendEvidenceToolsFromValues(outcomeContractRequiredToolNames(request.OutcomeContract))
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = sendEvidenceToolsFromValues(request.OutcomeContract.SelectedEvidenceHints)
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

func hasSendCompletionEvidence(observations []turnObservation, references []completionEvidenceReference) bool {
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if isFound && (isSendEvidenceTool(observation.Tool) || terminalObservationInvokedSendTool(observation)) {
			return true
		}
	}
	return false
}

func terminalObservationInvokedSendTool(observation turnObservation) bool {
	toolName, _, isFound := terminalCapabilityResponse(observation)
	return isFound && isSendEvidenceTool(toolName)
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
	result := validateCompletionGate(requirements, observations, criteria, actionDocument)
	if !result.IsSatisfied {
		return result
	}
	if externalSendCompletionEvidenceRequired(request) && !hasSendCompletionEvidence(observations, actionDocument.CompletionEvidence) {
		result.IsSatisfied = false
		result.SuggestedNextTools = requiredSendToolNamesForRequest(request)
		result.Message = sendCompletionEvidenceRequiredMessage(result.SuggestedNextTools)
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

func expectedResultGateMessage(results []ResultVerificationItem) string {
	parts := []string{}
	for _, result := range results {
		description := firstNonEmptyString(result.MissingDescription, result.Reason, result.ID)
		parts = append(parts, strings.TrimSpace(result.ID)+": "+strings.TrimSpace(description))
	}
	return "finish is missing required expected result: " + strings.Join(parts, "; ")
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

func completionGateObservation(index int, message string) turnObservation {
	content := "The final reply is not yet backed by the required successful observations. Produce, inspect, or cite the missing result before finishing. " + strings.TrimSpace(message)
	observation := newFailureObservation(nextObservationID(index), "evidence_missing", "", message, FailureInvalidInput, FailureCodes.InvalidInput, "completion_gate")
	observation = withObservationContent(observation, content)
	observation.Summary = content
	observation.Failure.Retryable = true
	observation.Failure.SafeRetry = true
	return observation
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
	if len(result.ResultVerification.Results) == 0 {
		return []string{result.Message}
	}
	values := []string{}
	for _, item := range result.ResultVerification.Results {
		if item.Status == "missing" || item.Status == "uncertain" {
			values = appendUniqueStrings(values, firstNonEmptyString(item.MissingDescription, item.Reason, item.ID))
		}
	}
	return values
}

func completionGateEventName(observation turnObservation) string {
	if observation.Action == "evidence_missing" {
		return "agent.evidence_missing"
	}
	return "agent.completion_required"
}

func validateCompletionEvidence(requirements []toolUseRequirement, observations []turnObservation, references []completionEvidenceReference) ([]FileAttachment, error) {
	if len(requirements) == 0 {
		if errorValue := validateCompletionEvidenceReferences(observations, references); errorValue != nil {
			return nil, errorValue
		}
		return collectReferenceDeliveryAttachments(observations, references), nil
	}
	attachments := collectReferenceDeliveryAttachments(observations, references)
	for _, requirement := range requirements {
		if !requirement.RequiresAttachment && !requirement.RequiresSideEffectEvidence {
			continue
		}
		matchingReferences := completionReferencesForRequirement(requirement, observations, references)
		if len(matchingReferences) == 0 {
			return nil, errors.New("completionEvidence must cite successful observation for " + requirementLabel(requirement))
		}
		if !requirement.RequiresAttachment {
			continue
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

func validateObservedToolRequirements(requirements []toolUseRequirement, observations []turnObservation) error {
	for _, requirement := range requirements {
		if requirement.RequiresAttachment {
			continue
		}
		isSatisfied, _ := completionRequirementStatus(requirement, observations)
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

func validateCompletionEvidenceReferences(observations []turnObservation, references []completionEvidenceReference) error {
	for _, reference := range references {
		if _, isFound := findSuccessfulObservation(observations, reference); !isFound {
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
	if strings.TrimSpace(requirement.ToolName) != "" {
		if terminalObservationMatchesCapabilityTool(observation, requirement.ToolName) {
			return true
		}
		return ToolNamesMatch(toolName, requirement.ToolName)
	}
	if strings.TrimSpace(requirement.ToolPrefix) != "" {
		return strings.HasPrefix(toolName, strings.TrimSpace(requirement.ToolPrefix))
	}
	return false
}

func findSuccessfulObservation(observations []turnObservation, reference completionEvidenceReference) (turnObservation, bool) {
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.ObservationID) != strings.TrimSpace(reference.ObservationID) {
			continue
		}
		if strings.TrimSpace(reference.ToolName) != "" && !ToolNamesMatch(observation.Tool, reference.ToolName) && !terminalObservationMatchesCapabilityTool(observation, reference.ToolName) {
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
	if strings.TrimSpace(requirement.ToolName) != "" {
		return strings.TrimSpace(requirement.ToolName)
	}
	return strings.TrimSpace(requirement.ToolPrefix)
}
