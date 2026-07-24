package agent

import (
	"context"
	"strings"

	"blueclaw/internal/task"
)

func (agentTurnRunner *AgentTurnRunner) prepareRecoveryAttempt(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) (string, toolCallActionOutcome) {
	failureDebt, hasFailureDebt := activeFailureDebt(state.Observations)
	if !hasFailureDebt {
		return "", toolCallActionOutcome{}
	}
	effectiveToolName := effectiveObservationToolName(actionDocument.ToolName, actionDocument.ToolInput)
	if isAllowed, reason := recoveryChoiceIsAllowed(failureDebt, state.Observations, effectiveToolName); !isAllowed {
		observation := recoveryChoiceRejectedObservation(len(state.Observations)+1, failureDebt.LatestFailure, reason)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.recovery_choice_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "recovery_choice_rejected "+effectiveToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return "", noProgressToolCallActionOutcome(result, shouldStop)
	}
	recoveryStep := classifyRecoveryStep(failureDebt, effectiveToolName)
	if !recoveryBudgetAllowsStep(state.Observations, agentTurnRunner.options.RecoveryBudget, recoveryStep) {
		observation := recoveryBudgetExhaustedObservation(len(state.Observations)+1, failureDebt.LatestFailure, recoveryStep, firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.recovery_budget_exhausted", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "recovery_budget_exhausted "+effectiveToolName, observation.ContentText())
		if recoveryToolBudgetExhaustedForRequest(state.Observations, request.ToolSet, agentTurnRunner.options.RecoveryBudget, failureDebt) {
			result := agentTurnRunner.runTerminalNoToolsStep(ctx, taskRunID, stepID, request, state, "recovery_tool_budget_exhausted")
			return "", toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true}
		}
		result, shouldStop := stopForNoProgress(stepID)
		return "", noProgressToolCallActionOutcome(result, shouldStop)
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.recovery_attempt", marshalEventBody(map[string]any{
		"status":       "started",
		"recoveryStep": recoveryStep,
		"toolName":     effectiveToolName,
		"debt":         failureDebt,
	}))
	return recoveryStep, toolCallActionOutcome{}
}

func recoveryGuidanceObservation(index int, observation turnObservation, originalInstruction string) turnObservation {
	packet := buildRecoveryPacket(observation)
	content := recoveryGuidanceContent(observation, originalInstruction) + " " + recoveryPacketContent(packet)
	return turnObservation{
		ObservationID:        nextObservationID(index),
		Action:               "recovery_guidance",
		Tool:                 observation.Tool,
		Output:               ToolOutput{Content: content},
		Summary:              content,
		Failure:              observation.Failure,
		ToolInputKey:         observation.ToolInputKey,
		RecoveryPacket:       &packet,
		RecoveryAttemptKey:   observation.RecoveryAttemptKey,
		RecoveryAttemptSpent: observation.RecoveryAttemptSpent,
	}
}

func recoveryChoiceRejectedObservation(index int, failedObservation turnObservation, reason string) turnObservation {
	packet := buildRecoveryPacket(failedObservation)
	content := "Invalid recovery choice. " + strings.TrimSpace(reason) + " " + recoveryPacketContent(packet)
	return turnObservation{
		ObservationID:  nextObservationID(index),
		Action:         "policy",
		Tool:           failedObservation.Tool,
		Output:         ToolOutput{Content: content},
		Summary:        content,
		Failure:        &ToolFailure{Kind: FailurePolicyBlocked, Code: FailureCodes.PolicyBlocked.String(), Stage: "recovery_policy", UserSafeSummary: reason},
		ToolInputKey:   failedObservation.ToolInputKey,
		RecoveryPacket: &packet,
	}
}

func recoveryGuidanceContent(observation turnObservation, originalInstruction string) string {
	parts := []string{"Analyze the latest failed tool result before responding."}
	if instruction := strings.TrimSpace(originalInstruction); instruction != "" {
		parts = append(parts, "The user's original request is still: \""+instruction+"\". Recover toward that request; do not drift into an unrelated question or topic because of this failure.")
	}
	if observation.FailureCode() != "" {
		parts = append(parts, "errorCode="+observation.FailureCode())
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "failureStage="+observation.FailureStage())
	}
	if observation.FailureSummary() != "" {
		parts = append(parts, "message="+observation.FailureSummary())
	}
	if observation.RecoveryAttemptKey != "" {
		parts = append(parts, "A safe automatic retry has already been attempted for this tool input.")
	}
	if terminalRecoveryGuidance := terminalWorkingDirectoryRecoveryGuidance(observation); terminalRecoveryGuidance != "" {
		parts = append(parts, terminalRecoveryGuidance)
	}
	if browserGuidance := browserPublicFetchRecoveryGuidance(observation); browserGuidance != "" {
		parts = append(parts, browserGuidance)
	}
	if sitePublishGuidance := sitePublishPrerequisiteRecoveryGuidance(observation); sitePublishGuidance != "" {
		parts = append(parts, sitePublishGuidance)
	}
	for _, recoveryRoute := range recoveryRoutesForObservation(observation) {
		parts = append(parts, recoveryRoute.Guidance())
	}
	return strings.Join(parts, " ")
}

func browserPublicFetchRecoveryGuidance(observation turnObservation) string {
	if !strings.HasPrefix(strings.TrimSpace(observation.Tool), "browser.") {
		return ""
	}
	return "Recovery route: browser capability operations run on the user's Companion and are only for sign-in, page interaction, screenshots, or pages that block fetching. To read or copy public web page content, use web.fetch (or web.search) instead of a browser; only fall back to the browser handoff when fetching fails or the user explicitly asks for a visible browser. Do not pass a tool name or a localhost address as the browser URL."
}

func sitePublishPrerequisiteRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.FailureStage()) != "workflow_prerequisite" {
		return ""
	}
	switch strings.TrimSpace(observation.Tool) {
	case "site.serve":
		return "Recovery route: for content-only changes, edit the site's app/public/site-content.json and serve directly, no build needed. A build is only required after a structural change under app/src or app config files."
	default:
		return ""
	}
}

func terminalWorkingDirectoryRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != "terminal.run" {
		return ""
	}
	if strings.TrimSpace(observation.FailureStage()) == "terminal_working_directory_access" {
		return "Recovery route: retry terminal.run with workingDirectoryPath set to ~/documents or another ~ path, use relative paths inside the command, then deliver accepted output with file.deliver."
	}
	return ""
}

type RecoveryRoute struct {
	ToolName     string
	UseWhen      string
	DoNotUseWhen string
}

func (recoveryRoute RecoveryRoute) Guidance() string {
	return "Recovery route: use " + recoveryRoute.ToolName + " only when " + recoveryRoute.UseWhen + "; do not use it when " + recoveryRoute.DoNotUseWhen + "."
}

func recoveryRoutesForObservation(observation turnObservation) []RecoveryRoute {
	if strings.TrimSpace(observation.Tool) != "memory.search" || observation.FailureCode() != FailureCodes.Unavailable.String() {
		return nil
	}
	return []RecoveryRoute{{
		ToolName:     "web.search",
		UseWhen:      "the missing information is required and public, current, or external",
		DoNotUseWhen: "private person or circle memory is required",
	}}
}

func recoveryAttemptCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		if observation.RecoveryAttemptSpent {
			count++
		}
	}
	return count
}
