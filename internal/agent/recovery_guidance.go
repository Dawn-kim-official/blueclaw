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
	if isAllowed, reason := recoveryChoiceIsAllowed(failureDebt, state.Observations, actionDocument.ToolName); !isAllowed {
		observation := recoveryChoiceRejectedObservation(len(state.Observations)+1, failureDebt.LatestFailure, reason)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.recovery_choice_rejected", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "recovery_choice_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return "", toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	recoveryStep := classifyRecoveryStep(failureDebt, actionDocument.ToolName)
	if !recoveryBudgetAllowsStep(state.Observations, agentTurnRunner.options.RecoveryBudget, recoveryStep) {
		observation := recoveryBudgetExhaustedObservation(len(state.Observations)+1, failureDebt.LatestFailure, recoveryStep)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, "agent.recovery_budget_exhausted", marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "recovery_budget_exhausted "+actionDocument.ToolName, observation.ContentText())
		if recoveryToolBudgetExhaustedForRequest(state.Observations, request.ToolSet, agentTurnRunner.options.RecoveryBudget, failureDebt) {
			result := agentTurnRunner.runTerminalNoToolsStep(ctx, taskRunID, stepID, request, state, "recovery_tool_budget_exhausted")
			return "", toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true}
		}
		result, shouldStop := stopForNoProgress(stepID)
		return "", toolCallActionOutcome{Result: result, ShouldReturn: shouldStop, WasHandled: true}
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.recovery_attempt", marshalEventBody(map[string]any{
		"status":       "started",
		"recoveryStep": recoveryStep,
		"toolName":     strings.TrimSpace(actionDocument.ToolName),
		"debt":         failureDebt,
	}))
	return recoveryStep, toolCallActionOutcome{}
}

func recoveryGuidanceObservation(index int, observation turnObservation) turnObservation {
	packet := buildRecoveryPacket(observation)
	content := recoveryGuidanceContent(observation) + " " + recoveryPacketContent(packet)
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

func recoveryGuidanceContent(observation turnObservation) string {
	parts := []string{"Analyze the latest failed tool result before responding."}
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
	if terminalRecoveryGuidance := terminalPathRecoveryGuidance(observation); terminalRecoveryGuidance != "" {
		parts = append(parts, terminalRecoveryGuidance)
	}
	if terminalDependencyGuidance := terminalPythonDependencyRecoveryGuidance(observation); terminalDependencyGuidance != "" {
		parts = append(parts, terminalDependencyGuidance)
	}
	if browserGuidance := browserPublicFetchRecoveryGuidance(observation); browserGuidance != "" {
		parts = append(parts, browserGuidance)
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

func terminalPathRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != "terminal.run" {
		return ""
	}
	switch strings.TrimSpace(observation.FailureStage()) {
	case "terminal_path_guardrail":
		return "Recovery route: retry terminal.run with virtual workspace paths: tmp/<slug> for draft work, home/<path> for requester-private durable source work, and artifacts/<slug> only when durable storage is explicitly needed. Do not call /opt/blueclaw, /tmp, concrete private POSIX paths, or runtime-internal paths directly. For built-in artifact skills, execute /workspace/skills/<skill>/scripts/skill_runtime.py and let the wrapper choose dependencies."
	case "terminal_working_directory_access":
		return "Recovery route: retry terminal.run with workingDirectoryPath set to tmp/<slug> or home/<path>, use relative paths inside the command, then deliver accepted output with file.deliver."
	default:
		if terminalCurrentDirectoryRecoveryNeeded(observation) {
			return "Recovery route: the command could not read its current working directory. Retry terminal.run with an existing virtual workspace directory. For site projects, use site.status and run builds from appWorkspacePath such as home/sites/<siteID>/app, not source subdirectories like app/src; run scripts with relative paths from that app directory."
		}
		return ""
	}
}

func terminalCurrentDirectoryRecoveryNeeded(observation turnObservation) bool {
	summary := strings.ToLower(observation.FailureSummary() + " " + observation.ContentText())
	for _, fragment := range []string{
		"couldntreadcurrentdirectory",
		"could not read current directory",
		"couldn't read current directory",
		"getcwd",
		"chdir",
	} {
		if strings.Contains(summary, fragment) {
			return true
		}
	}
	return false
}

func terminalPythonDependencyRecoveryGuidance(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != "terminal.run" || strings.TrimSpace(observation.FailureStage()) != "terminal_run" {
		return ""
	}
	summary := observation.FailureSummary()
	switch {
	case strings.Contains(summary, "ModuleNotFoundError: No module named 'pptx'"):
		return "Recovery route: do not probe or install python-pptx with system Python. Use the PPTX skill wrapper instead: create work under tmp/<deck-slug>, then run python3 /workspace/skills/pptx/scripts/skill_runtime.py python /workspace/skills/pptx/scripts/create_pptx.py deck.json output.pptx, or use /workspace/skills/simple-slides/scripts/build.sh after writing DESIGN.md and presentation.md."
	case strings.Contains(summary, "ModuleNotFoundError: No module named 'docx'"):
		return "Recovery route: do not probe or install python-docx with system Python. Use python3 /workspace/skills/docx/scripts/skill_runtime.py python /workspace/skills/docx/scripts/create_docx.py document.json output.docx from tmp/<document-slug>."
	case strings.Contains(summary, "ModuleNotFoundError: No module named 'openpyxl'"):
		return "Recovery route: do not probe or install openpyxl with system Python. Use python3 /workspace/skills/xlsx/scripts/skill_runtime.py python /workspace/skills/xlsx/scripts/create_xlsx.py workbook.json output.xlsx from tmp/<workbook-slug>."
	default:
		return ""
	}
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
