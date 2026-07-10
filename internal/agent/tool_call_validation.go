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
	if !toolCallAvailableForAction(request.ToolSet, actionDocument.ToolName, actionDocument.ToolInput) {
		observation := agentTurnRunner.recordUnavailableToolRequest(taskRunID, len(state.Observations)+1, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "tool_unavailable "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if shouldRejectUnnecessarySiteApprovalRequest(request, actionDocument.ToolName, actionDocument.ToolInput) {
		observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "policy", actionDocument.ToolName, unnecessarySiteApprovalMessage(), FailurePolicyBlocked, FailureCodes.PolicyBlocked, "policy")
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.approval_request_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "approval_request_rejected", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if shouldRejectUnnecessaryAcknowledgementApproval(actionDocument.ToolName, actionDocument.ToolInput) {
		observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "policy", actionDocument.ToolName, unnecessaryAcknowledgementApprovalMessage(), FailurePolicyBlocked, FailureCodes.PolicyBlocked, "policy")
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.approval_request_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "approval_request_rejected", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if observation, isRejected := unrequestedPlatformMessageSendObservation(request, actionDocument, nextObservationID(len(state.Observations)+1)); isRejected {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.external_send_intent_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "external_send_intent_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if observation, isRejected := sitePublishPrerequisiteFailure(state.Observations, actionDocument, nextObservationID(len(state.Observations)+1)); isRejected {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.site_publish_prerequisite_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "site_publish_prerequisite_rejected", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	return toolCallActionOutcome{}
}

func (agentTurnRunner *AgentTurnRunner) rejectMalformedToolCall(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if validationError := validateBrowserToolInput(actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
		observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "continue", actionDocument.ToolName, validationError.Error(), FailureInvalidInput, FailureCodes.InvalidInput, "tool_input")
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if validationError := validateTerminalToolInput(actionDocument.ToolName, actionDocument.ToolInput, request.ToolSet); validationError != nil {
		failureCode := FailureCodes.InvalidInput
		if isTerminalToolNameError(validationError) {
			failureCode = FailureCodes.ToolNameInShell
		}
		observation := newFailureObservation(nextObservationID(len(state.Observations)+1), "continue", actionDocument.ToolName, validationError.Error(), FailureInvalidInput, failureCode, "tool_input")
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.tool_input_malformed", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	return toolCallActionOutcome{}
}

func (agentTurnRunner *AgentTurnRunner) rejectRepeatedToolCall(taskRunID string, stepID string, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if observation, isRepeatedRead := repeatedFileReadObservation(state.Observations, actionDocument, nextObservationID(len(state.Observations)+1)); isRepeatedRead {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.file_read_cache_hit", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "file_read_cache_hit", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if sentObservation, wasSent := previousSuccessfulExternalSend(state.Observations, actionDocument.ToolName, actionDocument.ToolInput); wasSent {
		observation := turnObservation{
			ObservationID: nextObservationID(len(state.Observations) + 1),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output:        ToolOutput{Content: "This task already sent to that recipient as " + sentObservation.ObservationID + ". Do not send to the same recipient again. Send to a different recipient or use that observation for completionEvidence and finish."},
			Failure:       &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This task already sent to that recipient."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.external_send_repeat_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "external_send_repeat_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if duplicateObservation, isDuplicate := successfulToolCalls[canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)]; isDuplicate && handlesDuplicateSuccessfulToolCall(actionDocument.ToolName, actionDocument.ToolInput) && !terminalRerunAfterWorkspaceMutation(actionDocument, state.Observations, duplicateObservation) {
		observation := turnObservation{
			ObservationID: nextObservationID(len(state.Observations) + 1),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output:        ToolOutput{Content: "This exact tool call already succeeded as " + duplicateObservation.ObservationID + ". Use that observation for completionEvidence instead of running it again."},
			Failure:       &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This exact tool call already succeeded."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.duplicate_tool_call_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "duplicate_tool_call "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	if duplicateFailure, isDuplicateFailure := previousFailedToolInput(state.Observations, actionDocument.ToolName, actionDocument.ToolInput); isDuplicateFailure {
		if len(requiredPreconditionsForObservation(duplicateFailure)) > 0 {
			observation := recoveryChoiceRejectedObservation(len(state.Observations)+1, duplicateFailure, "Retrying "+strings.TrimSpace(actionDocument.ToolName)+" requires evidence first: "+strings.Join(missingRecoveryPreconditions(duplicateFailure, state.Observations), ", "))
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.appendEvent(taskRunID, "agent.recovery_choice_rejected", marshalEventBody(observation))
			agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "recovery_choice_rejected "+actionDocument.ToolName, observation.ContentText())
			result, shouldStop := stopForNoProgress(stepID)
			return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
		}
		observation := repeatedFailedAttemptObservation(len(state.Observations)+1, duplicateFailure, firstNonEmptyString(state.Request.ActiveGoal.OriginalInstruction, state.Request.Prompt))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.failed_fingerprint_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "failed_fingerprint_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	return toolCallActionOutcome{}
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
	case FileWriteToolName, FileEditToolName, FilePatchToolName:
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

func specificToolDescription(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return `Open a web URL. Input: {"url":"https://www.google.com"}.`
	case "browser.snapshot":
		return `Read the current page. Returns url, title, snapshotText, and interactiveRefs such as @e1. Input: {}.`
	case "browser.screenshot":
		return `Capture the current page screenshot. Returns a temporary devicePath, not a local path. Input: {"ttlSeconds":86400}.`
	case "browser.click":
		return `Click an element by observe ref or selector. Input: {"target":"@e1"} or {"selector":"button[type=submit]"}.`
	case "browser.fill":
		return `Fill an input by observe ref or selector. Input: {"target":"@e1","text":"hello world"}.`
	case "browser.select":
		return `Select an option. Input: {"target":"@e1","value":"option"}.`
	case "browser.press":
		return `Press a key. Input: {"key":"Enter"}.`
	case "browser.wait":
		return `Wait for time or target. Input: {"milliseconds":1000} or {"target":"@e1"}.`
	case "task.add":
		return `Add a new Flow work item for the requester, or request new work for another person. Do not use this for editing, changing, completing, or updating an existing work item. Input: {"prompt":"기획안 전달","targetPersonHint":"lee"}.`
	case "task.list":
		return `List work items. Leave targetPersonHint EMPTY to list EVERYONE's tasks; set it to one person's name to list only that person. For "my tasks / what do I have", set targetPersonHint to the requester's own name. Weeks are relative integer offsets: weekFrom/weekTo where 0=this week (default), -1=last week; omit both for this week, set weekFrom for a range ending this week. Use before completing work when the matching task is uncertain. Input: {"targetPersonHint":"이샘플","weekFrom":-1}.`
	case "task.update":
		return `Update or complete an existing Flow work item. Use this for edits, status changes, "수정", "변경", and "완료". Input: {"query":"10분 회의","content":"15분 회의"} or {"query":"10분 회의"} to mark it complete. Optional status values include "예정", "진행", "완료", "일시정지", "기각", and "중단".`
	default:
		return ""
	}
}

func validateBrowserToolInput(toolName string, toolInput json.RawMessage) error {
	toolName, toolInput = effectiveActionToolNameAndInput(toolName, toolInput)
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
	return errorValue.toolName + " is a Blueclaw tool, not a shell command. Call it through the current action schema."
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

func toolDefinitionInputSchema(toolDefinition ToolDefinition) json.RawMessage {
	if len(toolDefinition.InputSchema) > 0 {
		return toolDefinition.InputSchema
	}
	return specificToolInputSchema(toolDefinition.Name)
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
	effectiveToolName, effectiveToolInput := effectiveActionToolNameAndInput(toolName, toolInput)
	return strings.TrimSpace(effectiveToolName) + "\x00" + canonicalToolInput(effectiveToolInput)
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
	effectiveToolName, _ := effectiveActionToolNameAndInput(actionDocument.ToolName, actionDocument.ToolInput)
	if strings.TrimSpace(effectiveToolName) != "terminal.run" {
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

func handlesDuplicateSuccessfulToolCall(toolName string, toolInput json.RawMessage) bool {
	effectiveToolName, _ := effectiveActionToolNameAndInput(toolName, toolInput)
	if strings.TrimSpace(effectiveToolName) == "terminal.run" {
		return true
	}
	return isOneShotCompletionEvidenceTool(effectiveToolName)
}

func previousSuccessfulExternalSend(observations []turnObservation, toolName string, toolInput json.RawMessage) (turnObservation, bool) {
	toolName, toolInput = effectiveActionToolNameAndInput(toolName, toolInput)
	if !isUnsafeRepeatSensitiveTool(toolName) {
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

func isUnsafeRepeatSensitiveTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "message.send", "mail.message.send", "google.gmail.send", "slack.message.send":
		return true
	default:
		return false
	}
}

func shouldRejectUnnecessarySiteApprovalRequest(request AgentTurnRequest, toolName string, toolInput json.RawMessage) bool {
	if strings.TrimSpace(toolName) != "ask.confirm" {
		return false
	}
	if !sitePublishTaskToolsAreAvailable(request.ToolSet) {
		return false
	}
	if !requestHasSelectedSiteArtifactSkill(request) {
		return false
	}
	approvalText := strings.ToLower(strings.TrimSpace(string(toolInput)))
	if containsAny(approvalText, []string{"rollback", "roll back", "unpublish", "delete", "remove", "take down", "삭제", "되돌", "내려", "중단"}) {
		return false
	}
	if requiredEvidenceContains(request.RequiredEvidenceTools, "site.publish") {
		return true
	}
	return containsAny(approvalText, []string{"deploy", "publish", "external", "website", "site", "배포", "웹사이트", "외부"})
}

func requestHasSelectedSiteArtifactSkill(request AgentTurnRequest) bool {
	selectedSkillNames := selectedSkillNameSet(request.SkillDecisions)
	for _, skillInstruction := range request.AvailableSkills {
		if selectedSkillNames[skillInstruction.Name] && skillSupportsSiteArtifact(skillInstruction) {
			return true
		}
	}
	return false
}

func sitePublishTaskToolsAreAvailable(toolSet *ToolSet) bool {
	if toolSet == nil {
		return false
	}
	return toolSet.IsRegistered("site.create") && toolSet.IsRegistered("site.publish") && toolSet.IsRegistered("terminal.run")
}

func requiredEvidenceContains(requiredEvidenceTools []string, expectedToolName string) bool {
	for _, toolName := range requiredEvidenceTools {
		if ToolNamesMatch(toolName, expectedToolName) {
			return true
		}
	}
	return false
}

func requiredEvidenceHasPrefix(requiredEvidenceTools []string, prefix string) bool {
	for _, toolName := range requiredEvidenceTools {
		if strings.HasPrefix(strings.TrimSpace(toolName), prefix) {
			return true
		}
	}
	return false
}

func unnecessarySiteApprovalMessage() string {
	return "Approval is not required for website create, build, or publish capability operations. Continue with the website capability operations directly. Ask approval only before rollback, unpublish, or delete."
}

func shouldRejectUnnecessaryAcknowledgementApproval(toolName string, toolInput json.RawMessage) bool {
	if strings.TrimSpace(toolName) != "ask.confirm" {
		return false
	}
	var inputFields struct {
		UserFacingMessage string `json:"userFacingMessage"`
		ReasonDetail      string `json:"reasonDetail"`
	}
	if errorValue := json.Unmarshal(toolInput, &inputFields); errorValue != nil {
		return false
	}
	combinedText := strings.ToLower(strings.TrimSpace(inputFields.UserFacingMessage) + " " + strings.TrimSpace(inputFields.ReasonDetail))
	sensitivemarkers := []string{
		"send", "전송", "보내", "dm",
		"delete", "삭제", "지우", "remove", "제거",
		"deploy", "배포", "publish", "게시",
		"external", "외부",
		"payment", "결제", "구매", "charge",
		"credential", "api key", "apikey", "토큰", "token", "secret", "비밀", "password",
		"grant", "권한", "unlock", "install", "설치",
		"schedule", "예약",
	}
	if containsAny(combinedText, sensitivemarkers) {
		return false
	}
	acknowledgementMarkers := []string{
		"기억", "remember", "memor",
		"맞나요", "맞아요", "맞죠",
		"이해했", "알겠", "확인차",
		"저장할까", "저장하면", "기록할까",
		"note this", "just confirming", "let me know if",
	}
	return containsAny(combinedText, acknowledgementMarkers)
}

func unnecessaryAcknowledgementApprovalMessage() string {
	return "Do not use ask.confirm to acknowledge information, confirm understanding, or before a non-destructive write. Perform the action directly through the relevant named tool, then finish."
}

func unrequestedPlatformMessageSendObservation(request AgentTurnRequest, actionDocument turnActionDocument, observationID string) (turnObservation, bool) {
	toolName, toolInput := effectiveActionToolNameAndInput(actionDocument.ToolName, actionDocument.ToolInput)
	if strings.TrimSpace(toolName) != "message.send" {
		return turnObservation{}, false
	}
	if requestRequiresPlatformMessageSend(request) {
		return turnObservation{}, false
	}
	if promptLooksLikePlatformMessageSendRequest(request.Prompt) || promptLooksLikePlatformMessageSendRequest(request.ActiveGoal.OriginalInstruction) {
		return turnObservation{}, false
	}
	deliveryType := platformMessageSendDeliveryType(toolInput)
	message := "message.send is only for explicit platform delivery requests. The latest user message does not ask to send a separate platform message; answer in the current conversation with finish.message instead."
	if deliveryType != "" {
		message += " Requested targetType was " + deliveryType + "."
	}
	return newFailureObservation(observationID, "policy", strings.TrimSpace(toolName), message, FailurePolicyBlocked, FailureCodes.PolicyBlocked, "policy"), true
}

func requestRequiresPlatformMessageSend(request AgentTurnRequest) bool {
	if requiredEvidenceContains(request.RequiredEvidenceTools, "message.send") {
		return true
	}
	for _, toolName := range outcomeContractRequiredToolNames(request.OutcomeContract) {
		if ToolNamesMatch(toolName, "message.send") {
			return true
		}
	}
	for _, toolName := range outcomeContractRequiredToolNames(request.ActiveGoal.OutcomeContract) {
		if ToolNamesMatch(toolName, "message.send") {
			return true
		}
	}
	return false
}

func promptLooksLikePlatformMessageSendRequest(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	if normalizedPrompt == "" {
		return false
	}
	return containsAny(normalizedPrompt, []string{
		"dm", "direct message", "send", "notify", "post", "forward",
		"보내", "전송", "디엠", "dm해", "전달", "공지", "올려",
	})
}

func platformMessageSendDeliveryType(toolInput json.RawMessage) string {
	var document struct {
		TargetType string `json:"targetType"`
	}
	if len(toolInput) == 0 || json.Unmarshal(toolInput, &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.TargetType)
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

func toolCallAvailableForAction(toolRegistry *ToolSet, toolName string, toolInput json.RawMessage) bool {
	if toolAvailableForAction(toolRegistry, toolName) {
		return true
	}
	if strings.TrimSpace(toolName) != CapabilityInvokeToolName || toolRegistry == nil || !toolRegistry.IsRegistered(CapabilityInvokeToolName) {
		return false
	}
	operationName, _ := effectiveActionToolNameAndInput(toolName, toolInput)
	return operationName != CapabilityInvokeToolName && toolRegistry.IsAllowed(operationName)
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
	return agentTurnRunner.saveToolObservation(context.Background(), taskRunID, observationID, trimmedToolName, effectiveObservationToolName(trimmedToolName, toolInput), toolInputKey, ToolFailureResult(FailurePolicyBlocked, FailureCodes.PolicyBlocked, "tool_availability", "tool is not allowed"), workspaceRootPath, minimumModifiedAt, 0)
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
