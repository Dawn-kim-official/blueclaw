package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/task"
)

func (agentTurnRunner *AgentTurnRunner) rejectUnavailableToolCall(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if !toolAvailableForAction(request.ToolSet, actionDocument.ToolName) {
		observation := agentTurnRunner.recordUnavailableToolRequest(taskRunID, len(state.Observations)+1, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "tool_unavailable "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if observation, isRejected := unrequestedPlatformMessageSendObservation(request, actionDocument, nextObservationIDForObservations(state.Observations)); isRejected {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.external_send_intent_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "external_send_intent_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if observation, isRejected := sitePublishPrerequisiteFailure(state.Observations, actionDocument, nextObservationIDForObservations(state.Observations)); isRejected {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.site_publish_prerequisite_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "site_publish_prerequisite_rejected", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{}
}

func (agentTurnRunner *AgentTurnRunner) rejectMalformedToolCall(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if validationError := validateBrowserToolInput(actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
		observation := newFailureObservation(nextObservationIDForObservations(state.Observations), "continue", actionDocument.ToolName, validationError.Error(), FailureInvalidInput, FailureCodes.InvalidInput, "tool_input")
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if validationError := validateTerminalToolInput(actionDocument.ToolName, actionDocument.ToolInput, request.ToolSet); validationError != nil {
		failureCode := FailureCodes.InvalidInput
		if isTerminalToolNameError(validationError) {
			failureCode = FailureCodes.ToolNameInShell
		}
		observation := newFailureObservation(nextObservationIDForObservations(state.Observations), "continue", actionDocument.ToolName, validationError.Error(), FailureInvalidInput, failureCode, "tool_input")
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{}
}

func (agentTurnRunner *AgentTurnRunner) rejectRepeatedToolCall(taskRunID string, stepID string, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if observation, isRepeatedRead := repeatedFileReadObservation(state.Observations, actionDocument, nextObservationIDForObservations(state.Observations)); isRepeatedRead {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.file_read_cache_hit", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "file_read_cache_hit", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if sentObservation, wasSent := previousSuccessfulExternalSend(state.Request.ToolSet, state.Observations, actionDocument.ToolName, actionDocument.ToolInput); wasSent {
		observation := turnObservation{
			ObservationID: nextObservationIDForObservations(state.Observations),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output:        ToolOutput{Content: "This task already sent to that recipient as " + sentObservation.ObservationID + ". Do not send to the same recipient again. Send to a different recipient or use that observation for completionEvidence and finish."},
			Failure:       &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This task already sent to that recipient."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.external_send_repeat_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "external_send_repeat_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if duplicateObservation, isDuplicate := repeatedSuccessfulToolObservation(state, actionDocument, successfulToolCalls); isDuplicate {
		observation := turnObservation{
			ObservationID: nextObservationIDForObservations(state.Observations),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output:        ToolOutput{Content: "This exact tool call already succeeded as " + duplicateObservation.ObservationID + ". Use that observation for completionEvidence instead of running it again."},
			Failure:       &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This exact tool call already succeeded."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.duplicate_tool_call_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "duplicate_tool_call "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if duplicateFailure, isDuplicateFailure := previousFailedToolInput(state.Observations, actionDocument.ToolName, actionDocument.ToolInput); isDuplicateFailure {
		if len(requiredPreconditionsForObservation(duplicateFailure)) > 0 {
			observation := recoveryChoiceRejectedObservation(len(state.Observations)+1, duplicateFailure, "Retrying "+strings.TrimSpace(actionDocument.ToolName)+" requires evidence first: "+strings.Join(missingRecoveryPreconditions(duplicateFailure, state.Observations), ", "))
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.appendEvent(taskRunID, "agent.recovery_choice_rejected", marshalEventBody(observation))
			agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "recovery_choice_rejected "+actionDocument.ToolName, observation.ContentText())
			result, shouldStop := stopForNoProgress(stepID)
			return noProgressToolCallActionOutcome(result, shouldStop)
		}
		observation := repeatedFailedAttemptObservation(len(state.Observations)+1, duplicateFailure, firstNonEmptyString(state.Request.ActiveGoal.OriginalInstruction, state.Request.Prompt))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.failed_fingerprint_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "failed_fingerprint_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{}
}

func repeatedSuccessfulToolObservation(state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation) (turnObservation, bool) {
	observation, isDuplicate := repeatedSuccessfulCompletionCandidate(state, actionDocument, successfulToolCalls)
	if !isDuplicate || !handlesDuplicateSuccessfulToolCall(state.Request.ToolSet, actionDocument.ToolName, actionDocument.ToolInput) {
		return turnObservation{}, false
	}
	return observation, true
}

func repeatedSuccessfulCompletionCandidate(state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation) (turnObservation, bool) {
	toolInputKey := canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
	observation, isDuplicate := successfulToolCalls[toolInputKey]
	if !isDuplicate {
		observation, isDuplicate = previousSuccessfulToolInputObservation(state.Observations, toolInputKey)
	}
	if !isDuplicate || terminalRerunAfterWorkspaceMutation(actionDocument, state.Observations, observation) {
		return turnObservation{}, false
	}
	return observation, true
}

func previousSuccessfulToolInputObservation(observations []turnObservation, toolInputKey string) (turnObservation, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action == "continue" && !observation.Failed() && strings.TrimSpace(observation.ToolInputKey) == toolInputKey {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func duplicateSuccessFinalizationRequirements(toolSet *ToolSet, requirements []toolUseRequirement, observations []turnObservation, actionDocument turnActionDocument) ([]toolUseRequirement, bool) {
	if completionRequirementsHaveEvidence(requirements, observations) {
		return requirements, true
	}
	strictRequirements := []toolUseRequirement{}
	for _, requirement := range requirements {
		if !requirement.RequiresAttachment && !requirement.RequiresSideEffectEvidence {
			continue
		}
		isSatisfied, _ := completionRequirementStatus(requirement, observations)
		if !isSatisfied {
			return nil, false
		}
		strictRequirements = append(strictRequirements, requirement)
	}
	_, isFound := toolSet.ToolDefinition(actionDocument.ToolName)
	return strictRequirements, isFound
}

func repeatedFileReadObservation(observations []turnObservation, actionDocument turnActionDocument, observationID string) (turnObservation, bool) {
	if strings.TrimSpace(actionDocument.ToolName) != "file.read" {
		return turnObservation{}, false
	}
	requestedRange, ok := fileReadRequestedRange(actionDocument.ToolInput)
	if !ok {
		return turnObservation{}, false
	}
	recoveryDirective := stalledReadRecoveryDirective(observations)
	for index, observation := range observations {
		fileContext, isFileRead := progressFileContextFromObservation(observation)
		if !isFileRead || fileContext.Path != requestedRange.Path {
			continue
		}
		if hasNewerFileMutationObservation(observations[index+1:], requestedRange.Path) {
			continue
		}
		for _, readRange := range fileContext.ReadRanges {
			coveredRange, ok := parseFileReadRange(readRange)
			if !ok {
				continue
			}
			if coveredRange.StartLine <= requestedRange.StartLine && coveredRange.EndLine >= requestedRange.EndLine {
				return cachedFileReadObservation(observationID, observation, "Already read "+requestedRange.Path+" lines "+readRange+" as "+observation.ObservationID+". Reuse the cached content below instead of spending another file.read call."+recoveryDirective), true
			}
			if fileReadRangesOverlap(coveredRange, requestedRange) {
				return cachedFileReadObservation(observationID, observation, "Already read overlapping lines "+readRange+" from "+requestedRange.Path+" as "+observation.ObservationID+". Reuse cached content and request only an uncovered range such as "+uncoveredFileReadHint(coveredRange, requestedRange)+" if more text is needed."+recoveryDirective), true
			}
		}
	}
	return turnObservation{}, false
}

func hasNewerFileMutationObservation(observations []turnObservation, path string) bool {
	for _, observation := range observations {
		if observation.Failed() || !isFileMutationTool(observation.Tool) {
			continue
		}
		if observationOutputPath(observation) == path {
			return true
		}
	}
	return false
}

func isFileMutationTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case FileWriteToolName, FileEditToolName:
		return true
	default:
		return false
	}
}

func observationOutputPath(observation turnObservation) string {
	payload := map[string]any{}
	if json.Unmarshal([]byte(observation.ContentText()), &payload) != nil {
		return ""
	}
	return strings.TrimSpace(stringField(payload, "path"))
}

func stalledReadRecoveryDirective(observations []turnObservation) string {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return ""
	}
	failedTool := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	if failedTool == "" {
		return ""
	}
	return " You already have the file content and an unresolved " + failedTool + " failure. Stop re-reading: edit the file with file.edit to fix the cause, then re-run " + failedTool + "."
}

func cachedFileReadObservation(observationID string, previousObservation turnObservation, message string) turnObservation {
	payload := map[string]any{}
	if json.Unmarshal([]byte(previousObservation.ContentText()), &payload) != nil {
		payload = map[string]any{}
	}
	payload["cacheStatus"] = "hit"
	payload["cachedObservationID"] = previousObservation.ObservationID
	payload["message"] = strings.TrimSpace(message)
	content := marshalEventBody(payload)
	observation := newContentObservation(observationID, "policy", "file.read", content)
	observation.Output.Data = json.RawMessage(content)
	observation.Summary = "file.read cache hit for " + firstNonEmptyString(stringField(payload, "path"), "previous range")
	return observation
}

func fileReadRangesOverlap(firstRange fileReadRange, secondRange fileReadRange) bool {
	return firstRange.StartLine <= secondRange.EndLine && secondRange.StartLine <= firstRange.EndLine
}

func uncoveredFileReadHint(coveredRange fileReadRange, requestedRange fileReadRange) string {
	if requestedRange.EndLine > coveredRange.EndLine {
		return strconv.Itoa(coveredRange.EndLine+1) + "-" + strconv.Itoa(requestedRange.EndLine)
	}
	if requestedRange.StartLine < coveredRange.StartLine {
		return strconv.Itoa(requestedRange.StartLine) + "-" + strconv.Itoa(coveredRange.StartLine-1)
	}
	return "a different range"
}

type fileReadRange struct {
	Path      string
	StartLine int
	EndLine   int
}

func fileReadRequestedRange(toolInput json.RawMessage) (fileReadRange, bool) {
	document := map[string]any{}
	if errorValue := json.Unmarshal(toolInput, &document); errorValue != nil {
		return fileReadRange{}, false
	}
	path := strings.TrimSpace(stringField(document, "path"))
	if path == "" {
		return fileReadRange{}, false
	}
	if intField(document, "startByte") > 0 {
		return fileReadRange{}, false
	}
	startLine := intField(document, "startLine")
	if startLine <= 0 {
		startLine = 1
	}
	lineCount := intField(document, "lineCount")
	if lineCount <= 0 {
		lineCount = 200
	}
	return fileReadRange{Path: path, StartLine: startLine, EndLine: startLine + lineCount - 1}, true
}

func parseFileReadRange(value string) (fileReadRange, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) == 1 {
		startLine, errorValue := strconv.Atoi(parts[0])
		if errorValue != nil || startLine <= 0 {
			return fileReadRange{}, false
		}
		return fileReadRange{StartLine: startLine, EndLine: startLine}, true
	}
	if len(parts) != 2 {
		return fileReadRange{}, false
	}
	startLine, startError := strconv.Atoi(parts[0])
	endLine, endError := strconv.Atoi(parts[1])
	if startError != nil || endError != nil || startLine <= 0 || endLine < startLine {
		return fileReadRange{}, false
	}
	return fileReadRange{StartLine: startLine, EndLine: endLine}, true
}

func validateBrowserToolInput(toolName string, toolInput json.RawMessage) error {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return validateRequiredToolInputFields(toolName, toolInput, "url")
	case "browser.fill":
		return validateBrowserTargetToolInput(toolName, toolInput, "text")
	case "browser.click":
		return validateBrowserTargetToolInput(toolName, toolInput)
	case "browser.select":
		return validateBrowserTargetToolInput(toolName, toolInput, "value")
	case "browser.press":
		return validateRequiredToolInputFields(toolName, toolInput, "key")
	case "browser.wait":
		return validateBrowserWaitInput(toolInput)
	default:
		return nil
	}
}

type terminalToolNameError struct {
	toolName string
}

func (errorValue terminalToolNameError) Error() string {
	return errorValue.toolName + " is a Blueclaw tool, not a shell command. Call it directly through the action schema."
}

func isTerminalToolNameError(errorValue error) bool {
	var typedError terminalToolNameError
	return errors.As(errorValue, &typedError)
}

func validateTerminalToolInput(toolName string, toolInput json.RawMessage, toolSets ...*ToolSet) error {
	if !isTerminalExecutionTool(toolName) {
		return nil
	}
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	command := strings.TrimSpace(stringValue(inputDocument["command"]))
	if command == "" {
		return nil
	}
	var toolSet *ToolSet
	if len(toolSets) > 0 {
		toolSet = toolSets[0]
	}
	if commandToolName := firstTerminalCommandToken(command); toolSet != nil && toolSet.IsRegistered(commandToolName) {
		return terminalToolNameError{toolName: commandToolName}
	}
	for _, toolAlias := range []string{FileDeliverToolName, "set_quality_criteria", "finish"} {
		if strings.Contains(command, toolAlias) {
			return errors.New(strings.TrimSpace(toolName) + " command cannot call Blueclaw action " + toolAlias + "; call that action directly instead")
		}
	}
	return nil
}

func firstTerminalCommandToken(command string) string {
	for _, token := range terminalCommandTokens(command) {
		token = strings.Trim(token, `"'`)
		if strings.TrimSpace(token) != "" {
			return token
		}
	}
	return ""
}

func terminalCommandTokens(command string) []string {
	replacer := strings.NewReplacer(
		"\n", " ",
		";", " ",
		"&&", " ",
		"||", " ",
		"|", " ",
		"(", " ",
		")", " ",
		"=", " ",
		"<", " ",
		">", " ",
	)
	return strings.Fields(replacer.Replace(command))
}

func validateBrowserTargetToolInput(toolName string, toolInput json.RawMessage, fieldNames ...string) error {
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	missingFieldNames := []string{}
	if firstNonEmptyString(stringValue(inputDocument["target"]), stringValue(inputDocument["ref"]), stringValue(inputDocument["selector"])) == "" {
		missingFieldNames = append(missingFieldNames, "target/ref/selector")
	}
	for _, fieldName := range fieldNames {
		if strings.TrimSpace(stringValue(inputDocument[fieldName])) == "" {
			missingFieldNames = append(missingFieldNames, fieldName)
		}
	}
	if len(missingFieldNames) > 0 {
		return errors.New("missing required tool input for " + strings.TrimSpace(toolName) + ": " + strings.Join(missingFieldNames, ", ") + validInputExampleSuffix(toolName))
	}
	return nil
}

func validateRequiredToolInputFields(toolName string, toolInput json.RawMessage, fieldNames ...string) error {
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	missingFieldNames := []string{}
	for _, fieldName := range fieldNames {
		if strings.TrimSpace(stringValue(inputDocument[fieldName])) == "" {
			missingFieldNames = append(missingFieldNames, fieldName)
		}
	}
	if len(missingFieldNames) > 0 {
		return errors.New("missing required tool input for " + strings.TrimSpace(toolName) + ": " + strings.Join(missingFieldNames, ", ") + validInputExampleSuffix(toolName))
	}
	return nil
}

func validateBrowserWaitInput(toolInput json.RawMessage) error {
	inputDocument, errorValue := parseToolInputDocument("browser.wait", toolInput)
	if errorValue != nil {
		return errorValue
	}
	if strings.TrimSpace(stringValue(inputDocument["target"])) != "" {
		return nil
	}
	if strings.TrimSpace(stringValue(inputDocument["ref"])) != "" {
		return nil
	}
	if strings.TrimSpace(stringValue(inputDocument["selector"])) != "" {
		return nil
	}
	if numberValue(inputDocument["milliseconds"]) > 0 {
		return nil
	}
	return errors.New("missing required tool input for browser.wait: target or milliseconds")
}

func validInputExampleSuffix(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return `. Valid input example: {"url":"https://www.google.com"}`
	case "browser.fill":
		return `. Valid input example: {"target":"@e1","text":"hello world"}`
	case "browser.click":
		return `. Valid input example: {"target":"@e1"}`
	case "browser.select":
		return `. Valid input example: {"target":"@e1","value":"option"}`
	case "browser.press":
		return `. Valid input example: {"key":"Enter"}`
	case "browser.wait":
		return `. Valid input example: {"target":"@e1"} or {"milliseconds":1000}`
	default:
		return ""
	}
}

func parseToolInputDocument(toolName string, toolInput json.RawMessage) (map[string]any, error) {
	inputDocument := map[string]any{}
	if len(toolInput) == 0 {
		return inputDocument, nil
	}
	if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
		return nil, errors.New("tool input for " + strings.TrimSpace(toolName) + " is not valid json: " + errorValue.Error())
	}
	return inputDocument, nil
}

func canonicalToolCallKey(toolName string, toolInput json.RawMessage) string {
	return strings.TrimSpace(toolName) + "\x00" + canonicalToolInput(toolInput)
}

func canonicalToolInput(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return "{}"
	}
	var document any
	if errorValue := json.Unmarshal(toolInput, &document); errorValue != nil {
		return strings.TrimSpace(string(toolInput))
	}
	content, errorValue := json.Marshal(document)
	if errorValue != nil {
		return strings.TrimSpace(string(toolInput))
	}
	return string(content)
}

// terminalRerunAfterWorkspaceMutation frees an identical terminal.run command
// from duplicate rejection once the workspace changed after the previous run —
// a revise-then-rebuild loop legitimately repeats the same build command.
func terminalRerunAfterWorkspaceMutation(actionDocument turnActionDocument, observations []turnObservation, duplicateObservation turnObservation) bool {
	if strings.TrimSpace(actionDocument.ToolName) != "terminal.run" {
		return false
	}
	seenDuplicateObservation := false
	for _, observation := range observations {
		if observation.ObservationID == duplicateObservation.ObservationID {
			seenDuplicateObservation = true
			continue
		}
		if !seenDuplicateObservation || observation.Failed() {
			continue
		}
		if isFileMutationTool(observation.Tool) {
			return true
		}
	}
	return false
}

func handlesDuplicateSuccessfulToolCall(toolSet *ToolSet, toolName string, toolInput json.RawMessage) bool {
	if strings.TrimSpace(toolName) == "terminal.run" {
		return true
	}
	return isOneShotCompletionEvidenceTool(toolSet, toolName)
}

func previousSuccessfulExternalSend(toolSet *ToolSet, observations []turnObservation, toolName string, toolInput json.RawMessage) (turnObservation, bool) {
	if !isSendEvidenceTool(toolSet, toolName) {
		return turnObservation{}, false
	}
	currentRecipient := sendRecipientKey(toolInput)
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "continue" || observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.Tool) != strings.TrimSpace(toolName) {
			continue
		}
		if currentRecipient == "" || currentRecipient == observationSendRecipientKey(observation) {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func sendRecipientKey(toolInput json.RawMessage) string {
	var document struct {
		TargetType     string `json:"targetType"`
		PersonHint     string `json:"personHint"`
		ChannelName    string `json:"channelName"`
		ConversationID string `json:"conversationID"`
	}
	if len(toolInput) == 0 || json.Unmarshal(toolInput, &document) != nil {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(strings.Join([]string{document.TargetType, document.PersonHint, document.ChannelName, document.ConversationID}, "|")))
	if strings.Trim(key, "|") == "" {
		return ""
	}
	return key
}

func observationSendRecipientKey(observation turnObservation) string {
	_, canonicalInput, found := strings.Cut(observation.ToolInputKey, "\x00")
	if !found {
		return ""
	}
	return sendRecipientKey(json.RawMessage(canonicalInput))
}

func requiredEvidenceContains(requiredEvidenceTools []string, expectedToolName string) bool {
	for _, toolName := range requiredEvidenceTools {
		if ToolNamesMatch(toolName, expectedToolName) {
			return true
		}
	}
	return false
}

func unrequestedPlatformMessageSendObservation(request AgentTurnRequest, actionDocument turnActionDocument, observationID string) (turnObservation, bool) {
	toolName := strings.TrimSpace(actionDocument.ToolName)
	if !isSendEvidenceTool(request.ToolSet, toolName) {
		return turnObservation{}, false
	}
	if requestRequiresExternalSendTool(request, toolName) {
		return turnObservation{}, false
	}
	message := toolName + " requires an exact external-send outcome contract. Answer in the current conversation with finish.message instead."
	return newFailureObservation(observationID, "policy", toolName, message, FailurePolicyBlocked, FailureCodes.PolicyBlocked, "policy"), true
}

func requestRequiresExternalSendTool(request AgentTurnRequest, toolName string) bool {
	if requiredEvidenceContains(request.RequiredEvidenceTools, toolName) {
		return true
	}
	for _, requiredToolName := range outcomeContractRequiredToolNames(request.OutcomeContract) {
		if ToolNamesMatch(requiredToolName, toolName) {
			return true
		}
	}
	for _, requiredToolName := range outcomeContractRequiredToolNames(request.ActiveGoal.OutcomeContract) {
		if ToolNamesMatch(requiredToolName, toolName) {
			return true
		}
	}
	return false
}

func isTerminalExecutionTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "terminal.run":
		return true
	default:
		return false
	}
}

func blockedToolNamesForPreconditions(toolRegistry *ToolSet, requirements []toolUseRequirement, observations []turnObservation) map[string]bool {
	return map[string]bool{}
}

func toolAvailableForAction(toolRegistry *ToolSet, toolName string) bool {
	if toolRegistry == nil {
		return false
	}
	return toolRegistry.IsAllowed(strings.TrimSpace(toolName))
}

func (agentTurnRunner *AgentTurnRunner) recordUnavailableToolRequest(taskRunID string, index int, toolName string, toolInput json.RawMessage, workspaceRootPath string, minimumModifiedAt time.Time) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		trimmedToolName = "unknown_tool"
	}
	observationID := nextObservationID(index)
	toolInputKey := canonicalToolCallKey(trimmedToolName, toolInput)
	agentTurnRunner.appendEvent(taskRunID, "tool."+trimmedToolName+".requested", marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	return agentTurnRunner.saveToolObservation(context.Background(), taskRunID, observationID, trimmedToolName, "", toolInput, effectiveObservationToolName(trimmedToolName, toolInput), toolInputKey, ToolFailureResult(FailurePolicyBlocked, FailureCodes.PolicyBlocked, "tool_availability", "tool is not allowed"), workspaceRootPath, minimumModifiedAt, 0)
}

func stringValue(value any) string {
	typedValue, isString := value.(string)
	if !isString {
		return ""
	}
	return typedValue
}

func numberValue(value any) float64 {
	switch typedValue := value.(type) {
	case float64:
		return typedValue
	case int:
		return float64(typedValue)
	default:
		return 0
	}
}
