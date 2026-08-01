package bluecollar

import (
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/taskstate"
)

const executionStateMaxCharacters = 2500
const executionStateMaxKnownFacts = 8
const executionStateMaxTriedAndFailed = 6
const terminalObservationTailMaxLines = 20
const terminalObservationTailMaxCharacters = 2000

type ExecutionState struct {
	Goal           string     `json:"goal,omitempty"`
	Workspace      string     `json:"workspace,omitempty"`
	Steps          []PlanStep `json:"steps,omitempty"`
	KnownFacts     []string   `json:"knownFacts,omitempty"`
	TriedAndFailed []string   `json:"triedAndFailed,omitempty"`
	CurrentBlocker string     `json:"currentBlocker,omitempty"`
	NextPlan       string     `json:"nextPlan,omitempty"`
	WasCompacted   bool       `json:"wasCompacted,omitempty"`
}

type PlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

const executionStateMaxPlanSteps = 12

func NormalizePlan(goal string, steps []PlanStep) (string, []PlanStep) {
	return truncateText(compactWhitespace(goal), 300), normalizePlanSteps(steps)
}

var planStepStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"done":        true,
	"skipped":     true,
}

func normalizePlanSteps(steps []PlanStep) []PlanStep {
	normalizedSteps := []PlanStep{}
	for _, step := range steps {
		title := truncateText(compactWhitespace(step.Title), 200)
		status := strings.TrimSpace(step.Status)
		if title == "" {
			continue
		}
		if !planStepStatuses[status] {
			status = "pending"
		}
		normalizedSteps = append(normalizedSteps, PlanStep{Title: title, Status: status})
		if len(normalizedSteps) >= executionStateMaxPlanSteps {
			break
		}
	}
	return normalizedSteps
}

type TerminalObservationTail struct {
	ObservationID      string   `json:"observationID"`
	ToolName           string   `json:"toolName"`
	WorkingDirectory   string   `json:"workingDirectoryPath,omitempty"`
	Command            string   `json:"command,omitempty"`
	ExitCode           *int     `json:"exitCode,omitempty"`
	StdoutTail         []string `json:"stdoutTail,omitempty"`
	StderrTail         []string `json:"stderrTail,omitempty"`
	StdoutTruncated    bool     `json:"stdoutTruncated,omitempty"`
	StderrTruncated    bool     `json:"stderrTruncated,omitempty"`
	TimedOut           bool     `json:"timedOut,omitempty"`
	FailureStage       string   `json:"failureStage,omitempty"`
	ErrorCode          string   `json:"errorCode,omitempty"`
	RepeatCount        int      `json:"repeatCount,omitempty"`
	AttemptFingerprint string   `json:"attemptFingerprint,omitempty"`
}

type toolInteraction struct {
	ObservationID      string
	ToolName           string
	Input              terminalObservationInputDocument
	ResultData         json.RawMessage
	FailureStage       string
	ErrorCode          string
	AttemptFingerprint string
}

func normalizeExecutionState(state ExecutionState) ExecutionState {
	state.Goal = truncateText(compactWhitespace(state.Goal), 300)
	state.Workspace = truncateText(compactWhitespace(state.Workspace), 160)
	state.CurrentBlocker = truncateText(compactWhitespace(state.CurrentBlocker), 400)
	state.NextPlan = truncateText(compactWhitespace(state.NextPlan), 400)
	state.Steps = normalizePlanSteps(state.Steps)
	state.KnownFacts = normalizeExecutionStateList(state.KnownFacts, executionStateMaxKnownFacts)
	state.TriedAndFailed = normalizeExecutionStateList(state.TriedAndFailed, executionStateMaxTriedAndFailed)
	if executionStateLength(state) <= executionStateMaxCharacters {
		return state
	}
	state.WasCompacted = true
	state.KnownFacts = truncateStringSlice(state.KnownFacts, 4)
	state.TriedAndFailed = truncateStringSlice(state.TriedAndFailed, 3)
	state.CurrentBlocker = truncateText(state.CurrentBlocker, 240)
	state.NextPlan = truncateText(state.NextPlan, 240)
	return state
}

func normalizeExecutionStateList(values []string, limit int) []string {
	normalizedValues := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		trimmedValue := truncateText(compactWhitespace(value), 300)
		if trimmedValue == "" || seen[trimmedValue] {
			continue
		}
		seen[trimmedValue] = true
		normalizedValues = append(normalizedValues, trimmedValue)
		if len(normalizedValues) >= limit {
			break
		}
	}
	return normalizedValues
}

func truncateStringSlice(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append([]string{}, values[len(values)-limit:]...)
}

func executionStateLength(state ExecutionState) int {
	content, errorValue := json.Marshal(state)
	if errorValue != nil {
		return 0
	}
	return len(content)
}

func executionStateIsEmpty(state ExecutionState) bool {
	state = normalizeExecutionState(state)
	return state.Goal == "" &&
		state.Workspace == "" &&
		len(state.Steps) == 0 &&
		len(state.KnownFacts) == 0 &&
		len(state.TriedAndFailed) == 0 &&
		state.CurrentBlocker == "" &&
		state.NextPlan == ""
}

func executionStateFromTaskEvents(events []taskstate.TaskEvent) ExecutionState {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if strings.TrimSpace(event.Name) != "agent.execution_state" {
			continue
		}
		var state ExecutionState
		if json.Unmarshal([]byte(event.Body), &state) == nil {
			return normalizeExecutionState(state)
		}
	}
	return ExecutionState{}
}

func buildExecutionStateContext(state ExecutionState, observations []turnObservation) string {
	sections := []string{}
	if !executionStateIsEmpty(state) {
		sections = append(sections, "Compact ExecutionState maintained by the model. Keep it short, update it in executionStateUpdate on every action, and use it as working memory instead of re-reading the whole event history:\n"+marshalEventBody(normalizeExecutionState(state)))
	}
	if terminalContext := buildTerminalObservationTailContext(observations); terminalContext != "" {
		sections = append(sections, terminalContext)
	}
	if len(sections) == 0 {
		return ""
	}
	sections = append(sections, "Use exact terminal tails above for recovery. Do not replace them with generic permission, environment, or system-limitation explanations unless the tail or ExecutionState says that. Do not repeat the same failed terminal command unchanged.")
	return strings.Join(sections, "\n\n")
}

func buildTerminalObservationTailContext(observations []turnObservation) string {
	tails := compactTerminalObservationTails(observations, 3)
	if len(tails) == 0 {
		return ""
	}
	return "Recent terminal observation tails. These are capped raw tails for unresolved execution context; full output remains in the event log:\n" + marshalEventBody(tails)
}

func compactTerminalObservationTails(observations []turnObservation, limit int) []TerminalObservationTail {
	tails := []TerminalObservationTail{}
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if strings.TrimSpace(observation.Tool) != "terminal.run" {
			continue
		}
		tail, ok := terminalObservationTail(observation)
		if !ok {
			continue
		}
		if len(tails) > 0 && tails[0].AttemptFingerprint != "" && tails[0].AttemptFingerprint == tail.AttemptFingerprint {
			tails[0].RepeatCount++
			tails[0].ObservationID = tail.ObservationID
			continue
		}
		if tail.RepeatCount == 0 {
			tail.RepeatCount = 1
		}
		tails = append([]TerminalObservationTail{tail}, tails...)
		if len(tails) >= limit {
			break
		}
	}
	return tails
}

func terminalObservationTail(observation turnObservation) (TerminalObservationTail, bool) {
	interaction := toolInteractionFromObservation(observation)
	if len(interaction.ResultData) == 0 && interaction.Input.Command == "" && interaction.Input.WorkingDirectory == "" {
		return TerminalObservationTail{}, false
	}
	var result struct {
		ExitCode *int   `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		TimedOut bool   `json:"timedOut"`
	}
	hasResult := json.Unmarshal(interaction.ResultData, &result) == nil
	tail := TerminalObservationTail{
		ObservationID:      interaction.ObservationID,
		ToolName:           interaction.ToolName,
		WorkingDirectory:   interaction.Input.WorkingDirectory,
		Command:            interaction.Input.Command,
		FailureStage:       interaction.FailureStage,
		ErrorCode:          interaction.ErrorCode,
		AttemptFingerprint: interaction.AttemptFingerprint,
	}
	if hasResult {
		tail.ExitCode = result.ExitCode
		tail.StdoutTail, tail.StdoutTruncated = tailLines(result.Stdout, terminalObservationTailMaxLines, terminalObservationTailMaxCharacters)
		tail.StderrTail, tail.StderrTruncated = tailLines(result.Stderr, terminalObservationTailMaxLines, terminalObservationTailMaxCharacters)
		tail.TimedOut = result.TimedOut
		return tail, true
	}
	return tail, true
}

func toolInteractionFromObservation(observation turnObservation) toolInteraction {
	return toolInteraction{
		ObservationID:      observation.ObservationID,
		ToolName:           strings.TrimSpace(observation.Tool),
		Input:              terminalObservationInput(observation.ToolInputKey),
		ResultData:         append(json.RawMessage{}, observation.Output.Data...),
		FailureStage:       observation.FailureStage(),
		ErrorCode:          observation.FailureCode(),
		AttemptFingerprint: strings.TrimSpace(observation.AttemptFingerprint),
	}
}

type terminalObservationInputDocument struct {
	WorkingDirectory string
	Command          string
}

func terminalObservationInput(toolInputKey string) terminalObservationInputDocument {
	parts := strings.SplitN(toolInputKey, "\x00", 2)
	if len(parts) != 2 {
		return terminalObservationInputDocument{}
	}
	var document map[string]any
	if json.Unmarshal([]byte(parts[1]), &document) != nil {
		return terminalObservationInputDocument{}
	}
	return terminalObservationInputDocument{
		WorkingDirectory: strings.TrimSpace(stringValue(document["workingDirectoryPath"])),
		Command:          strings.TrimSpace(stringValue(document["command"])),
	}
}

func tailLines(value string, maxLines int, maxCharacters int) ([]string, bool) {
	value = redactUnsafeText(strings.TrimRight(value, "\n"))
	if value == "" {
		return nil, false
	}
	lines := strings.Split(value, "\n")
	truncated := false
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		truncated = true
	}
	joined := strings.Join(lines, "\n")
	if maxCharacters > 0 && len(joined) > maxCharacters {
		joined = joined[len(joined)-maxCharacters:]
		truncated = true
		lines = strings.Split(joined, "\n")
	}
	return lines, truncated
}

func executionStateSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"goal":           stringSchema(),
			"workspace":      stringSchema(),
			"steps":          planStepArraySchema(),
			"knownFacts":     stringArraySchema(executionStateMaxKnownFacts),
			"triedAndFailed": stringArraySchema(executionStateMaxTriedAndFailed),
			"currentBlocker": stringSchema(),
			"nextPlan":       stringSchema(),
		},
	}
}

func stringArraySchema(maxItems int) map[string]any {
	schema := map[string]any{
		"type":  "array",
		"items": stringSchema(),
	}
	return schema
}

func planStepArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"title", "status"},
			"properties": map[string]any{
				"title":  stringSchema(),
				"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done", "skipped"}},
			},
		},
	}
}
