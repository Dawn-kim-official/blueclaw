package agent

import (
	"encoding/json"
	"strings"
)

const (
	recoveryStepCorrectedRetry = "corrected_retry"
	recoveryStepAlternateRoute = "alternate_route"
	recoveryStepAdjacentTool   = "adjacent_tool"
	recoveryStepRejectedRepeat = "rejected_repeat"

	failureResolutionRecoveredWithSuccess = "recovered_with_success"
	failureResolutionNoToolFallback       = "no_tool_fallback"
	failureResolutionFailureReport        = "failure_report"
)

type FailureDebt struct {
	LatestFailure turnObservation `json:"latestFailure"`
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
		if observation.Action != "call_tool" {
			continue
		}
		if observation.IsError && strings.TrimSpace(observation.ToolInputKey) != "" {
			activeDebt = FailureDebt{LatestFailure: observation}
			continue
		}
		if !observation.IsError && strings.TrimSpace(activeDebt.LatestFailure.ObservationID) != "" {
			activeDebt = FailureDebt{}
		}
	}
	return activeDebt, strings.TrimSpace(activeDebt.LatestFailure.ObservationID) != ""
}

func attemptFingerprint(toolInputKey string, errorCode string) string {
	normalizedToolInputKey := strings.TrimSpace(toolInputKey)
	normalizedErrorCode := strings.TrimSpace(errorCode)
	if normalizedErrorCode == "" {
		normalizedErrorCode = "tool_failed"
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
		if observation.Action != "call_tool" {
			continue
		}
		if !observation.IsError {
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
	return recoveryStepAdjacentTool
}

func isAlternateRouteToolPair(firstToolName string, secondToolName string) bool {
	firstRoute := recoveryRouteGroup(firstToolName)
	secondRoute := recoveryRouteGroup(secondToolName)
	return firstRoute != "" && firstRoute == secondRoute
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

func repeatedFailedAttemptObservation(index int, failedObservation turnObservation) turnObservation {
	content := "This exact tool/input/error fingerprint already failed. Do not repeat it. Change the input, use another route or adjacent tool, answer without tools using failureResolution=no_tool_fallback if enough context exists, or fail after recovery budget is exhausted."
	observation := recoveryGuidanceObservation(index, failedObservation)
	observation.Action = "policy"
	observation.Content = content + " " + observation.Content
	observation.Summary = observation.Content
	observation.RecoveryStep = recoveryStepRejectedRepeat
	observation.RecoveryAttemptSpent = true
	return observation
}

func recoveryBudgetExhaustedObservation(index int, failedObservation turnObservation, recoveryStep string) turnObservation {
	content := "The recovery budget for " + strings.TrimSpace(recoveryStep) + " is exhausted. Choose another recovery step, answer without tools using failureResolution=no_tool_fallback if enough context exists, or return fail if no recovery tool budget remains."
	observation := recoveryGuidanceObservation(index, failedObservation)
	observation.Action = "policy"
	observation.Content = content + " " + observation.Content
	observation.Summary = observation.Content
	observation.RecoveryStep = strings.TrimSpace(recoveryStep)
	observation.RecoveryAttemptSpent = true
	return observation
}

func activeFailureDebtEventBody(observations []turnObservation, budget RecoveryBudget) map[string]any {
	failureDebt, _ := activeFailureDebt(observations)
	return map[string]any{
		"failureDebt":    failureDebt,
		"attemptLedger":  attemptLedger(observations),
		"recoveryBudget": normalizeRecoveryBudget(budget),
	}
}

func attemptLedger(observations []turnObservation) []attemptLedgerEntry {
	entries := []attemptLedgerEntry{}
	for _, observation := range observations {
		if observation.Action != "call_tool" || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		status := "success"
		if observation.IsError {
			status = "error"
		}
		entries = append(entries, attemptLedgerEntry{
			ObservationID:      observation.ObservationID,
			ToolName:           strings.TrimSpace(observation.Tool),
			ToolInputKey:       strings.TrimSpace(observation.ToolInputKey),
			AttemptFingerprint: strings.TrimSpace(observation.AttemptFingerprint),
			FailureStage:       strings.TrimSpace(observation.FailureStage),
			ErrorCode:          strings.TrimSpace(observation.ErrorCode),
			RecoveryStep:       strings.TrimSpace(observation.RecoveryStep),
			Status:             status,
		})
	}
	return entries
}

func failureDebtFinalizationGate(observations []turnObservation, actionDocument turnActionDocument, budget RecoveryBudget) completionGateResult {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return completionGateResult{IsSatisfied: true}
	}
	switch strings.TrimSpace(actionDocument.FailureResolution) {
	case failureResolutionNoToolFallback:
		if normalizeRecoveryBudget(budget).NoToolFallback > 0 {
			return completionGateResult{IsSatisfied: true}
		}
		return completionGateResult{Message: "FailureDebt is active and no-tool fallback budget is disabled"}
	case failureResolutionRecoveredWithSuccess:
		return completionGateResult{Message: "FailureDebt is active because no later successful tool call resolved the latest failure"}
	case failureResolutionFailureReport:
		return completionGateResult{Message: "FailureDebt failure reports must use the fail action, not final_reply"}
	default:
		return completionGateResult{Message: failureDebtFinalizationMessage(failureDebt)}
	}
}

func failureDebtFinalizationMessage(failureDebt FailureDebt) string {
	latestFailure := failureDebt.LatestFailure
	parts := []string{"A failed tool call created FailureDebt, so final_reply is locked."}
	if strings.TrimSpace(latestFailure.Tool) != "" {
		parts = append(parts, "tool="+strings.TrimSpace(latestFailure.Tool))
	}
	if strings.TrimSpace(latestFailure.FailureStage) != "" {
		parts = append(parts, "failureStage="+strings.TrimSpace(latestFailure.FailureStage))
	}
	if strings.TrimSpace(latestFailure.ErrorCode) != "" {
		parts = append(parts, "errorCode="+strings.TrimSpace(latestFailure.ErrorCode))
	}
	if strings.TrimSpace(latestFailure.AttemptFingerprint) != "" {
		parts = append(parts, "attemptFingerprint="+strings.TrimSpace(latestFailure.AttemptFingerprint))
	}
	parts = append(parts, "Recover with a different successful tool call, use failureResolution=no_tool_fallback when answering from current context without tools, or use fail after recovery budget is exhausted.")
	return strings.Join(parts, " ")
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
	combinedText := strings.ToLower(strings.TrimSpace(observation.ErrorCode + " " + observation.FailureStage + " " + observation.Message + " " + observation.Content))
	return strings.Contains(combinedText, "blocked_by_captcha")
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
