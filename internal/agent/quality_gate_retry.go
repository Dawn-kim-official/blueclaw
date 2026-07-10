package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"blueclaw/internal/task"
)

const qualityGateMarkerPrefix = "QUALITY_GATE "

type qualityGateReport struct {
	Passed bool     `json:"passed"`
	Score  *float64 `json:"score"`
	Source string   `json:"source"`
}

// rejectBelowGateDelivery enforces the improvement loop that skill prompts can
// only request: while a machine-readable quality gate from a build tool says
// passed=false, the score is still improving, and enough budget remains for
// another revision round, artifact delivery is deferred so the model revises
// and rebuilds first. Once the score stalls, the budget status reaches
// consolidate/finalize, or the no-progress budget is exhausted, the
// best-effort artifact is never suppressed: delivery passes through untouched.
func (agentTurnRunner *AgentTurnRunner) rejectBelowGateDelivery(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	effectiveToolName, _ := effectiveActionToolNameAndInput(actionDocument.ToolName, actionDocument.ToolInput)
	if !IsArtifactDeliveryTool(effectiveToolName) {
		return toolCallActionOutcome{}
	}
	message, retryRequired := agentTurnRunner.qualityGateRetryDirective(state.Observations, state.ToolCallCount, request.TurnStartedAt)
	if !retryRequired {
		return toolCallActionOutcome{}
	}
	observation := qualityGateRetryObservation(nextObservationID(len(state.Observations)+1), effectiveToolName, message)
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.quality_gate_retry_required", marshalEventBody(observation))
	agentTurnRunner.saveStep(taskRunID, stepID, task.TaskStatusCompleted, "quality_gate_retry_required "+effectiveToolName, observation.ContentText())
	if _, shouldStop := stopForNoProgress(stepID); shouldStop {
		return toolCallActionOutcome{}
	}
	return toolCallActionOutcome{WasHandled: true}
}

func (agentTurnRunner *AgentTurnRunner) qualityGateRetryDirective(observations []turnObservation, toolCallCount int, turnStartedAt time.Time) (string, bool) {
	reports := qualityGateReports(observations)
	if len(reports) == 0 {
		return "", false
	}
	latestReport := reports[len(reports)-1]
	if latestReport.Passed {
		return "", false
	}
	if qualityGateScoreStalled(reports) {
		return "", false
	}
	pressureLevel := agentTurnRunner.limitPressureLevel(len(observations), toolCallCount, agentTurnRunner.turnElapsed(turnStartedAt))
	if pressureLevel == "consolidate" || pressureLevel == "finalize" {
		return "", false
	}
	return qualityGateRetryMessage(latestReport), true
}

func qualityGateScoreStalled(reports []qualityGateReport) bool {
	if len(reports) < 2 {
		return false
	}
	latestReport := reports[len(reports)-1]
	previousReport := reports[len(reports)-2]
	if latestReport.Score == nil || previousReport.Score == nil {
		return true
	}
	return *latestReport.Score <= *previousReport.Score
}

func qualityGateRetryMessage(latestReport qualityGateReport) string {
	scoreText := ""
	if latestReport.Score != nil {
		scoreText = fmt.Sprintf(" with score %.0f", *latestReport.Score)
	}
	return "The latest build's quality gate reported passed=false" + scoreText + " and improvement budget remains. Do not deliver or finish yet: inspect the build's review evidence, revise the source with targeted edits, and rebuild. Delivery unblocks when the gate passes, the score stops improving between consecutive builds, or the budget status says consolidate or finalize."
}

func qualityGateRetryObservation(observationID string, blockedToolName string, message string) turnObservation {
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":   message,
		"blockedTool": blockedToolName,
	}))
	observation.Summary = message
	return observation
}

func qualityGateReports(observations []turnObservation) []qualityGateReport {
	reports := []qualityGateReport{}
	for _, observation := range observations {
		reports = append(reports, observationQualityGateReports(observation)...)
	}
	return reports
}

func observationQualityGateReports(observation turnObservation) []qualityGateReport {
	content := observation.ContentText()
	if !strings.Contains(content, qualityGateMarkerPrefix) {
		return nil
	}
	reports := parseQualityGateLines(content)
	var decodedContent map[string]any
	if json.Unmarshal([]byte(content), &decodedContent) == nil {
		for _, fieldName := range []string{"stdout", "stderr", "content"} {
			if text, isText := decodedContent[fieldName].(string); isText {
				reports = append(reports, parseQualityGateLines(text)...)
			}
		}
	}
	return reports
}

func parseQualityGateLines(text string) []qualityGateReport {
	reports := []qualityGateReport{}
	for _, line := range strings.Split(text, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmedLine, qualityGateMarkerPrefix) {
			continue
		}
		report := qualityGateReport{}
		if json.Unmarshal([]byte(strings.TrimPrefix(trimmedLine, qualityGateMarkerPrefix)), &report) != nil {
			continue
		}
		reports = append(reports, report)
	}
	return reports
}
