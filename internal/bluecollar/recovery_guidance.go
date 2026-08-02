package bluecollar

import (
	"context"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

func (agentTurnRunner *AgentTurnRunner) prepareRecoveryAttempt(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) (string, toolCallActionOutcome) {
	failureDebt, hasFailureDebt := activeFailureDebt(state.Observations)
	if !hasFailureDebt {
		return "", toolCallActionOutcome{}
	}
	effectiveToolName := effectiveObservationToolName(actionDocument.ToolName, actionDocument.ToolInput)
	recoveryStep := classifyRecoveryStep(request.ToolSet, failureDebt, effectiveToolName)
	if !recoveryBudgetAllowsStep(state.Observations, agentTurnRunner.options.RecoveryBudget, recoveryStep) {
		observation := recoveryBudgetExhaustedObservation(request.ToolSet, len(state.Observations)+1, failureDebt.LatestFailure, recoveryStep, firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.recovery_budget_exhausted", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, taskstate.TaskStatusCompleted, "recovery_budget_exhausted "+effectiveToolName, observation.ContentText())
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

func recoveryGuidanceObservation(toolSet *toolcontract.ToolSet, index int, observation turnObservation, originalInstruction string) turnObservation {
	packet := buildRecoveryPacket(observation)
	content := recoveryGuidanceContent(toolSet, observation, originalInstruction) + " " + recoveryPacketContent(packet)
	return turnObservation{
		ObservationID:        nextObservationID(index),
		Action:               "recovery_guidance",
		Tool:                 observation.Tool,
		Output:               toolcontract.ToolOutput{Content: content},
		Summary:              content,
		Failure:              observation.Failure,
		ToolInputKey:         observation.ToolInputKey,
		RecoveryPacket:       &packet,
		RecoveryAttemptKey:   observation.RecoveryAttemptKey,
		RecoveryAttemptSpent: observation.RecoveryAttemptSpent,
	}
}

func recoveryGuidanceContent(toolSet *toolcontract.ToolSet, observation turnObservation, originalInstruction string) string {
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
	if browserGuidance := browserPublicFetchRecoveryGuidance(toolSet, observation); browserGuidance != "" {
		parts = append(parts, browserGuidance)
	}
	return strings.Join(parts, " ")
}

// The descriptor says the call needed the browser on the requester's own machine.
// A name prefix said it too, right up until a tool was renamed.
func browserPublicFetchRecoveryGuidance(toolSet *toolcontract.ToolSet, observation turnObservation) string {
	definition, isFound := toolDefinitionForRecovery(toolSet, observation.Tool)
	if !isFound || !definition.RequiresRequesterDevice {
		return ""
	}
	return "Recovery route: browser capability operations run on the user's Companion and are only for sign-in, page interaction, screenshots, or pages that block fetching. To read or copy public web page content, use web_fetch (or web_search) instead of a browser; only fall back to the browser handoff when fetching fails or the user explicitly asks for a visible browser. Do not pass a tool name or a localhost address as the browser URL."
}

func terminalWorkingDirectoryRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.FailureStage()) == "terminal_working_directory_access" {
		return "Recovery route: retry terminal_run with workingDirectoryPath set to ~/documents or another ~ path, use relative paths inside the command, then deliver accepted output with file.deliver."
	}
	return ""
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
