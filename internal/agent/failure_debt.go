package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const (
	recoveryStepCorrectedRetry = "corrected_retry"
	recoveryStepAlternateRoute = "alternate_route"
	recoveryStepAdjacentTool   = "adjacent_tool"
	recoveryStepPrecondition   = "precondition"
	recoveryStepInspection     = "inspection"
	recoveryStepRejectedRepeat = "rejected_repeat"

	failureResolutionRecoveredWithSuccess = "recovered_with_success"
	failureResolutionNoToolFallback       = "no_tool_fallback"
	failureResolutionFailureReport        = "failure_report"
)

type FailureDebt struct {
	LatestFailure turnObservation `json:"latestFailure"`
}

type RecoveryPacket struct {
	WhatFailed          string               `json:"whatFailed"`
	WhyLikely           string               `json:"whyLikely,omitempty"`
	MustDoNext          []string             `json:"mustDoNext,omitempty"`
	AllowedTools        []string             `json:"allowedTools,omitempty"`
	ForbiddenRepeats    []string             `json:"forbiddenRepeats,omitempty"`
	EvidenceNeeded      []string             `json:"evidenceNeeded,omitempty"`
	FailureClass        string               `json:"failureClass,omitempty"`
	RetryPolicy         string               `json:"retryPolicy,omitempty"`
	AffectedResources   []AffectedResource   `json:"affectedResources,omitempty"`
	DiagnosticArtifacts []DiagnosticArtifact `json:"diagnosticArtifacts,omitempty"`
}

type attemptLedgerEntry struct {
	ObservationID      string `json:"observationID"`
	ToolName           string `json:"toolName"`
	ToolInputKey       string `json:"toolInputKey,omitempty"`
	AttemptFingerprint string `json:"attemptFingerprint,omitempty"`
	FailureStage       string `json:"failureStage,omitempty"`
	ErrorCode          string `json:"errorCode,omitempty"`
	RecoveryStep       string `json:"recoveryStep,omitempty"`
	Status             string `json:"status"`
}

type failureReportFacts struct {
	Attempts    []failureReportAttempt `json:"attempts"`
	BudgetState string                 `json:"budgetState"`
}

type failureReportAttempt struct {
	ToolName     string `json:"toolName"`
	InputSummary string `json:"inputSummary"`
	ErrorCode    string `json:"errorCode"`
	FailureStage string `json:"failureStage"`
	Message      string `json:"message"`
}

func defaultRecoveryBudget() RecoveryBudget {
	return RecoveryBudget{
		CorrectedRetry: 1,
		AlternateRoute: 1,
		AdjacentTool:   2,
		NoToolFallback: 1,
	}
}

func normalizeRecoveryBudget(budget RecoveryBudget) RecoveryBudget {
	if budget.CorrectedRetry < 0 {
		budget.CorrectedRetry = 0
	}
	if budget.AlternateRoute < 0 {
		budget.AlternateRoute = 0
	}
	if budget.AdjacentTool < 0 {
		budget.AdjacentTool = 0
	}
	if budget.NoToolFallback < 0 {
		budget.NoToolFallback = 0
	}
	return budget
}

func recoveryBudgetIsUnset(budget RecoveryBudget) bool {
	return budget.CorrectedRetry == 0 && budget.AlternateRoute == 0 && budget.AdjacentTool == 0 && budget.NoToolFallback == 0
}

func recoveryToolBudgetTotal(budget RecoveryBudget) int {
	budget = normalizeRecoveryBudget(budget)
	return budget.CorrectedRetry + budget.AlternateRoute + budget.AdjacentTool
}

func activeFailureDebt(observations []turnObservation) (FailureDebt, bool) {
	var activeDebt FailureDebt
	for _, observation := range observations {
		if observation.Action != "continue" {
			continue
		}
		if observation.Failed() && strings.TrimSpace(observation.ToolInputKey) != "" {
			if failureObservationDoesNotCreateDebt(observation) {
				continue
			}
			activeDebt = FailureDebt{LatestFailure: observation}
			continue
		}
		if !observation.Failed() && strings.TrimSpace(activeDebt.LatestFailure.ObservationID) != "" {
			if successfulObservationIsInspection(observation) {
				continue
			}
			activeDebt = FailureDebt{}
		}
	}
	return activeDebt, strings.TrimSpace(activeDebt.LatestFailure.ObservationID) != ""
}

func failureObservationDoesNotCreateDebt(observation turnObservation) bool {
	if strings.TrimSpace(observation.Tool) != "file.read" {
		return false
	}
	if observation.FailureCode() != FailureCodes.NotFound.String() {
		return false
	}
	return optionalSiteControlFileToolInputKey(observation.ToolInputKey)
}

func optionalSiteControlFileToolInputKey(toolInputKey string) bool {
	_, inputDocument, isFound := strings.Cut(toolInputKey, "\x00")
	if !isFound {
		return false
	}
	input := map[string]any{}
	if json.Unmarshal([]byte(inputDocument), &input) != nil {
		return false
	}
	normalizedPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(stringValue(input["path"]))))
	for _, suffix := range []string{
		".internkim/site.json",
		".internkim/idea.md",
		".internkim/artifact-brief.md",
		".internkim/review-log.json",
	} {
		if normalizedPath == suffix || strings.HasSuffix(normalizedPath, "/"+suffix) {
			return true
		}
	}
	return false
}

func successfulObservationIsInspection(observation turnObservation) bool {
	if strings.TrimSpace(observation.RecoveryStep) == recoveryStepInspection {
		return true
	}
	return isInspectionRecoveryTool(observation.Tool)
}

func attemptFingerprint(toolInputKey string, errorCode string) string {
	normalizedToolInputKey := strings.TrimSpace(toolInputKey)
	normalizedErrorCode := strings.TrimSpace(errorCode)
	if normalizedErrorCode == "" {
		normalizedErrorCode = FailureCodes.OperationFailed.String()
	}
	if normalizedToolInputKey == "" {
		return normalizedErrorCode
	}
	return normalizedToolInputKey + "\x00" + normalizedErrorCode
}

func previousFailedToolInput(observations []turnObservation, toolName string, toolInput json.RawMessage) (turnObservation, bool) {
	expectedKey := canonicalToolCallKey(toolName, toolInput)
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "continue" {
			continue
		}
		if !observation.Failed() {
			return turnObservation{}, false
		}
		if strings.TrimSpace(observation.ToolInputKey) == expectedKey {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func classifyRecoveryStep(failureDebt FailureDebt, toolName string) string {
	failedToolName := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	recoveryToolName := strings.TrimSpace(toolName)
	if failedToolName == recoveryToolName {
		return recoveryStepCorrectedRetry
	}
	if isAlternateRouteToolPair(failedToolName, recoveryToolName) {
		return recoveryStepAlternateRoute
	}
	if toolCanSatisfyRecoveryPrecondition(failureDebt.LatestFailure, recoveryToolName) {
		return recoveryStepPrecondition
	}
	if isInspectionRecoveryTool(recoveryToolName) {
		return recoveryStepInspection
	}
	return recoveryStepAdjacentTool
}

func toolCanSatisfyRecoveryPrecondition(failedObservation turnObservation, toolName string) bool {
	for _, precondition := range requiredPreconditionsForObservation(failedObservation) {
		switch strings.TrimSpace(precondition) {
		case "source_changed":
			if toolName == "file.write" || toolName == "file.edit" {
				return true
			}
		}
	}
	return false
}

func isInspectionRecoveryTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "file.read", "site.list", "conversation.history":
		return true
	default:
		return false
	}
}

func isAlternateRouteToolPair(firstToolName string, secondToolName string) bool {
	if isMemorySearchWebSearchRoute(firstToolName, secondToolName) {
		return true
	}
	firstRoute := recoveryRouteGroup(firstToolName)
	secondRoute := recoveryRouteGroup(secondToolName)
	return firstRoute != "" && firstRoute == secondRoute
}

func isMemorySearchWebSearchRoute(firstToolName string, secondToolName string) bool {
	return strings.TrimSpace(firstToolName) == "memory.search" && strings.TrimSpace(secondToolName) == "web.search"
}

func recoveryRouteGroup(toolName string) string {
	trimmedToolName := strings.TrimSpace(toolName)
	switch {
	case strings.HasPrefix(trimmedToolName, "browser.") || strings.HasPrefix(trimmedToolName, "browser_handoff.") || strings.HasPrefix(trimmedToolName, "web."):
		return "web"
	case strings.HasPrefix(trimmedToolName, "terminal."):
		return "terminal"
	default:
		return ""
	}
}

func recoveryBudgetAllowsStep(observations []turnObservation, budget RecoveryBudget, recoveryStep string) bool {
	budget = normalizeRecoveryBudget(budget)
	switch recoveryStep {
	case recoveryStepCorrectedRetry:
		return recoveryStepUseCount(observations, recoveryStepCorrectedRetry) < budget.CorrectedRetry
	case recoveryStepAlternateRoute:
		return recoveryStepUseCount(observations, recoveryStepAlternateRoute) < budget.AlternateRoute
	case recoveryStepAdjacentTool:
		return recoveryStepUseCount(observations, recoveryStepAdjacentTool) < budget.AdjacentTool
	case recoveryStepPrecondition:
		return true
	case recoveryStepInspection:
		return true
	default:
		return false
	}
}

func recoveryStepUseCount(observations []turnObservation, recoveryStep string) int {
	count := 0
	for _, observation := range observations {
		if observation.RecoveryAttemptSpent && strings.TrimSpace(observation.RecoveryStep) == recoveryStep {
			count++
		}
	}
	return count
}

func maxToolCallCountWithRecovery(options TurnOptions, observations []turnObservation) int {
	if _, hasFailureDebt := activeFailureDebt(observations); !hasFailureDebt {
		return options.MaxToolCallCount
	}
	return options.MaxToolCallCount + recoveryToolBudgetTotal(options.RecoveryBudget)
}

func repeatedFailedAttemptObservation(index int, failedObservation turnObservation, originalInstruction string) turnObservation {
	content := "This exact tool/input/error fingerprint already failed. Do not repeat it. Change the input, use another route or adjacent tool, answer without tools using failureResolution=no_tool_fallback if enough context exists, or fail after recovery budget is exhausted."
	observation := recoveryGuidanceObservation(index, failedObservation, originalInstruction)
	observation.Action = "policy"
	observation = withObservationContent(observation, content+" "+observation.ContentText())
	observation.Summary = observation.ContentText()
	observation.RecoveryStep = recoveryStepRejectedRepeat
	observation.RecoveryAttemptSpent = true
	return observation
}

func recoveryBudgetExhaustedObservation(index int, failedObservation turnObservation, recoveryStep string, originalInstruction string) turnObservation {
	content := "The recovery budget for " + strings.TrimSpace(recoveryStep) + " is exhausted. Choose another recovery step, answer without tools using failureResolution=no_tool_fallback if enough context exists, or return fail if no recovery tool budget remains."
	observation := recoveryGuidanceObservation(index, failedObservation, originalInstruction)
	observation.Action = "policy"
	observation = withObservationContent(observation, content+" "+observation.ContentText())
	observation.Summary = observation.ContentText()
	observation.RecoveryStep = strings.TrimSpace(recoveryStep)
	observation.RecoveryAttemptSpent = true
	observation.PolicyCode = "recovery_budget_exhausted"
	return observation
}

func activeFailureDebtEventBody(observations []turnObservation, budget RecoveryBudget) map[string]any {
	failureDebt, _ := activeFailureDebt(observations)
	return map[string]any{
		"failureDebt":        failureDebt,
		"failureReportFacts": buildFailureReportFacts(observations, budget),
		"attemptLedger":      attemptLedger(observations),
		"recoveryBudget":     normalizeRecoveryBudget(budget),
	}
}

func attemptLedger(observations []turnObservation) []attemptLedgerEntry {
	entries := []attemptLedgerEntry{}
	for _, observation := range observations {
		if observation.Action != "continue" || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		status := "success"
		if observation.Failed() {
			status = "error"
		}
		entries = append(entries, attemptLedgerEntry{
			ObservationID:      observation.ObservationID,
			ToolName:           strings.TrimSpace(observation.Tool),
			ToolInputKey:       strings.TrimSpace(observation.ToolInputKey),
			AttemptFingerprint: strings.TrimSpace(observation.AttemptFingerprint),
			FailureStage:       observation.FailureStage(),
			ErrorCode:          observation.FailureCode(),
			RecoveryStep:       strings.TrimSpace(observation.RecoveryStep),
			Status:             status,
		})
	}
	return entries
}

func buildFailureReportFacts(observations []turnObservation, budget RecoveryBudget) failureReportFacts {
	facts := failureReportFacts{BudgetState: failureReportBudgetState(observations, budget)}
	for _, observation := range observations {
		if observation.Action != "continue" || !observation.Failed() {
			continue
		}
		facts.Attempts = append(facts.Attempts, failureReportAttempt{
			ToolName:     strings.TrimSpace(observation.Tool),
			InputSummary: failureReportInputSummary(observation.ToolInputKey),
			ErrorCode:    firstNonEmptyString(observation.FailureCode(), FailureCodes.OperationFailed.String()),
			FailureStage: firstNonEmptyString(observation.FailureStage(), strings.TrimSpace(observation.Tool)),
			Message:      failureReportMessage(observation),
		})
	}
	return facts
}

func failureReportBudgetState(observations []turnObservation, budget RecoveryBudget) string {
	budget = normalizeRecoveryBudget(budget)
	if budget.NoToolFallback > 0 {
		return "no_tool_fallback_available"
	}
	if recoveryStepUseCount(observations, recoveryStepCorrectedRetry) < budget.CorrectedRetry ||
		recoveryStepUseCount(observations, recoveryStepAlternateRoute) < budget.AlternateRoute ||
		recoveryStepUseCount(observations, recoveryStepAdjacentTool) < budget.AdjacentTool {
		return "recovery_tools_available"
	}
	return "failure_report_required"
}

func failureReportInputSummary(toolInputKey string) string {
	parts := strings.SplitN(toolInputKey, "\x00", 2)
	if len(parts) != 2 {
		return truncateText(compactWhitespace(redactUnsafeText(toolInputKey)), 120)
	}
	var document map[string]any
	if json.Unmarshal([]byte(parts[1]), &document) == nil {
		for _, fieldName := range []string{"expression", "query", "url", "message", "command"} {
			if value, isString := document[fieldName].(string); isString && strings.TrimSpace(value) != "" {
				return truncateText(compactWhitespace(redactUnsafeText(value)), 120)
			}
		}
	}
	return truncateText(compactWhitespace(redactUnsafeText(parts[1])), 120)
}

func failureReportMessage(observation turnObservation) string {
	if terminalSummary := summarizeTerminalFailure(observation); terminalSummary != "" {
		return truncateText(compactWhitespace(redactUnsafeText(terminalSummary)), 240)
	}
	message := observation.FailureSummary()
	if message == "" {
		message = strings.TrimSpace(observation.ContentText())
	}
	return truncateText(compactWhitespace(redactUnsafeText(message)), 240)
}

func failureDebtActionContractMessage(facts failureReportFacts) string {
	return strings.Join([]string{
		"FailureDebt is active. The action schema now requires failureResolution.",
		"If a RecoveryPacket is present, choose one of its allowedTools and satisfy evidenceNeeded before retrying the failed tool.",
		"Do not repeat a failed tool while RecoveryPacket.forbiddenRepeats applies; use an inspect/edit/repair/change-route action first.",
		"If you can answer directly without tools, return finish with failureResolution=no_tool_fallback and do not apologize or mention the failed tool unless the user asked about internals.",
		"If you cannot answer directly and recovery budget is exhausted, return fail with failureResolution=failure_report and copy the relevant facts into usedFailureFacts.",
		"FailureReportFacts:\n" + marshalEventBody(facts),
	}, "\n")
}

func isRecoveredFailureDebtResolution(failureResolution string) bool {
	switch strings.TrimSpace(failureResolution) {
	case failureResolutionRecoveredWithSuccess, failureResolutionNoToolFallback:
		return true
	default:
		return false
	}
}

func validateFailureReportAction(actionDocument turnActionDocument, facts failureReportFacts) completionGateResult {
	if strings.TrimSpace(actionDocument.FailureResolution) != failureResolutionFailureReport {
		return completionGateResult{Message: "FailureDebt failure reports require failureResolution=failure_report"}
	}
	if len(actionDocument.UsedFailureFacts.Attempts) == 0 {
		return completionGateResult{Message: "FailureDebt failure reports require usedFailureFacts.attempts"}
	}
	if strings.TrimSpace(actionDocument.UsedFailureFacts.BudgetState) == "" {
		return completionGateResult{Message: "FailureDebt failure reports require usedFailureFacts.budgetState"}
	}
	expectedAttempt, hasExpectedAttempt := latestFailureReportAttempt(facts)
	if hasExpectedAttempt && !usedFailureFactsContainAttempt(actionDocument.UsedFailureFacts.Attempts, expectedAttempt) {
		return completionGateResult{Message: "FailureDebt failure reports must preserve toolName, errorCode, failureStage, and message from FailureReportFacts"}
	}
	return completionGateResult{IsSatisfied: true}
}

func latestFailureReportAttempt(facts failureReportFacts) (failureReportAttempt, bool) {
	for index := len(facts.Attempts) - 1; index >= 0; index-- {
		if strings.TrimSpace(facts.Attempts[index].ToolName) != "" {
			return facts.Attempts[index], true
		}
	}
	return failureReportAttempt{}, false
}

func usedFailureFactsContainAttempt(attempts []failureReportAttempt, expectedAttempt failureReportAttempt) bool {
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.ToolName) != strings.TrimSpace(expectedAttempt.ToolName) {
			continue
		}
		if strings.TrimSpace(attempt.ErrorCode) == "" || strings.TrimSpace(attempt.FailureStage) == "" || strings.TrimSpace(attempt.Message) == "" {
			continue
		}
		if strings.TrimSpace(expectedAttempt.ErrorCode) != "" && strings.TrimSpace(attempt.ErrorCode) != strings.TrimSpace(expectedAttempt.ErrorCode) {
			continue
		}
		if strings.TrimSpace(expectedAttempt.FailureStage) != "" && strings.TrimSpace(attempt.FailureStage) != strings.TrimSpace(expectedAttempt.FailureStage) {
			continue
		}
		return true
	}
	return false
}

func recoveryToolBudgetExhaustedForRequest(observations []turnObservation, toolSet *ToolSet, budget RecoveryBudget, failureDebt FailureDebt) bool {
	if failureRecoveryIsTerminal(failureDebt.LatestFailure) {
		return true
	}
	budget = normalizeRecoveryBudget(budget)
	if toolAvailableForAction(toolSet, failureDebt.LatestFailure.Tool) && recoveryStepUseCount(observations, recoveryStepCorrectedRetry) < budget.CorrectedRetry {
		return false
	}
	if alternateRouteToolIsAvailable(toolSet, failureDebt.LatestFailure.Tool) {
		if recoveryStepUseCount(observations, recoveryStepAlternateRoute) < budget.AlternateRoute {
			return false
		}
	}
	if adjacentRecoveryToolIsAvailable(toolSet, failureDebt.LatestFailure.Tool) {
		if recoveryStepUseCount(observations, recoveryStepAdjacentTool) < budget.AdjacentTool {
			return false
		}
	}
	return true
}

func failureRecoveryIsTerminal(observation turnObservation) bool {
	return observation.Failure != nil && observation.Failure.Kind == FailureInteractionRequired
}

func alternateRouteToolIsAvailable(toolSet *ToolSet, failedToolName string) bool {
	if toolSet == nil {
		return false
	}
	normalizedFailedToolName := strings.TrimSpace(failedToolName)
	for _, toolName := range toolSet.ListToolNames() {
		if strings.TrimSpace(toolName) != "" && strings.TrimSpace(toolName) != normalizedFailedToolName && isAlternateRouteToolPair(normalizedFailedToolName, toolName) {
			return true
		}
	}
	return false
}

func adjacentRecoveryToolIsAvailable(toolSet *ToolSet, failedToolName string) bool {
	if toolSet == nil {
		return false
	}
	normalizedFailedToolName := strings.TrimSpace(failedToolName)
	for _, toolName := range toolSet.ListToolNames() {
		normalizedToolName := strings.TrimSpace(toolName)
		if normalizedToolName != "" && normalizedToolName != normalizedFailedToolName && !isAlternateRouteToolPair(normalizedFailedToolName, normalizedToolName) {
			return true
		}
	}
	return false
}
