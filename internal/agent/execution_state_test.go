package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"blueclaw/internal/llm"
)

func TestBuildAgentActionRequestIncludesExecutionStateAndTerminalTail(t *testing.T) {
	state := agentTaskState{
		Request: AgentTurnRequest{Prompt: "make a pptx", ToolSet: newTestToolSet([]string{"terminal.run"})},
		Options: TurnOptions{RecoveryBudget: defaultRecoveryBudget()},
		ExecutionState: ExecutionState{
			Goal:           "create and attach pptx",
			Workspace:      "tmp/deck",
			KnownFacts:     []string{"presentation.md was written"},
			CurrentBlocker: "build cannot see presentation.md",
			NextPlan:       "inspect cwd",
		},
		Observations: []turnObservation{terminalFailureObservation("obs-001", "tmp/deck", "NAME=deck build.sh", longTerminalOutput("Error: presentation.md not found"))},
	}

	request := BuildAgentActionRequest(state)
	body := messagesText(request.Messages)

	if !strings.Contains(body, "Compact ExecutionState") || !strings.Contains(body, "build cannot see presentation.md") {
		t.Fatalf("expected execution state in prompt, got %s", body)
	}
	if !strings.Contains(body, "Recent terminal observation tails") || !strings.Contains(body, "Error: presentation.md not found") {
		t.Fatalf("expected terminal tail in prompt, got %s", body)
	}
	if strings.Contains(body, `"line 1"`) {
		t.Fatalf("expected terminal tail to omit older lines, got %s", body)
	}
}

func TestTerminalObservationTailKeepsSmallBuildListingsUseful(t *testing.T) {
	observation := terminalSuccessObservation("obs-003", "tmp/deck", "ls -R build/", strings.Join([]string{
		"build/:",
		"deck-notes.txt",
		"deck.html",
		"deck.pdf",
		"deck.pptx",
		"review",
		"",
		"build/review:",
		"contact-sheet-01.png",
		"slide-review.json",
		"slide-review.md",
	}, "\n"))

	tail, ok := terminalObservationTail(observation)

	if !ok {
		t.Fatal("expected terminal tail")
	}
	if !strings.Contains(strings.Join(tail.StdoutTail, "\n"), "deck.pptx") {
		t.Fatalf("expected pptx filename to remain visible, got %+v", tail.StdoutTail)
	}
}

func TestTerminalObservationTailPreservesToolInteractionInputAndResult(t *testing.T) {
	observation := terminalFailureObservation("obs-002", "tmp/site/app", "bun run build", "CouldntReadCurrentDirectory")

	tail, ok := terminalObservationTail(observation)

	if !ok {
		t.Fatal("expected terminal tail")
	}
	if tail.ObservationID != "obs-002" || tail.ToolName != "terminal.run" {
		t.Fatalf("expected observation identity preserved, got %+v", tail)
	}
	if tail.WorkingDirectory != "tmp/site/app" || tail.Command != "bun run build" {
		t.Fatalf("expected tool input preserved, got %+v", tail)
	}
	if len(tail.StderrTail) != 1 || tail.StderrTail[0] != "CouldntReadCurrentDirectory" {
		t.Fatalf("expected result tail preserved, got %+v", tail.StderrTail)
	}
}

func TestNormalizeExecutionStateEnforcesLimits(t *testing.T) {
	state := ExecutionState{
		KnownFacts:     []string{"a", "a", "b", "c", "d", "e", "f", "g", "h", "i"},
		TriedAndFailed: []string{"1", "2", "3", "4", "5", "6", "7"},
		CurrentBlocker: strings.Repeat("x", 600),
		NextPlan:       strings.Repeat("y", 600),
	}

	normalized := normalizeExecutionState(state)

	if len(normalized.KnownFacts) > executionStateMaxKnownFacts {
		t.Fatalf("expected known facts capped, got %+v", normalized.KnownFacts)
	}
	if len(normalized.TriedAndFailed) > executionStateMaxTriedAndFailed {
		t.Fatalf("expected triedAndFailed capped, got %+v", normalized.TriedAndFailed)
	}
	if strings.Count(strings.Join(normalized.KnownFacts, ","), "a") != 1 {
		t.Fatalf("expected duplicate facts removed, got %+v", normalized.KnownFacts)
	}
	if len(normalized.CurrentBlocker) > 403 || len(normalized.NextPlan) > 403 {
		t.Fatalf("expected long fields truncated, got blocker=%d next=%d", len(normalized.CurrentBlocker), len(normalized.NextPlan))
	}
}

func TestValidateTerminalToolInputRejectsVirtualPathInsideTmpWorkingDirectory(t *testing.T) {
	input := MarshalToolInput(map[string]any{
		"workingDirectoryPath": "tmp/deck",
		"command":              "ls -R tmp/deck",
	})

	errorValue := validateTerminalToolInput("terminal.run", input)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "already scoped tmp workingDirectoryPath") {
		t.Fatalf("expected scoped tmp diagnostic rejection, got %v", errorValue)
	}
}

func terminalFailureObservation(observationID string, workingDirectoryPath string, command string, stderr string) turnObservation {
	content := marshalEventBody(map[string]any{
		"exitCode": 1,
		"stdout":   "",
		"stderr":   stderr,
		"timedOut": false,
	})
	input := MarshalToolInput(map[string]any{
		"workingDirectoryPath": workingDirectoryPath,
		"command":              command,
	})
	return turnObservation{
		ObservationID:      observationID,
		Action:             "continue",
		Tool:               "terminal.run",
		Output:             ToolOutput{Content: content},
		Failure:            &ToolFailure{Kind: FailureExternalService, Code: FailureCodes.OperationFailed.String(), Stage: "terminal_run", UserSafeSummary: content},
		ToolInputKey:       canonicalToolCallKey("terminal.run", input),
		AttemptFingerprint: attemptFingerprint(canonicalToolCallKey("terminal.run", input), FailureCodes.OperationFailed.String()),
	}
}

func terminalSuccessObservation(observationID string, workingDirectoryPath string, command string, stdout string) turnObservation {
	content := marshalEventBody(map[string]any{
		"exitCode": 0,
		"stdout":   stdout,
		"stderr":   "",
		"timedOut": false,
	})
	input := MarshalToolInput(map[string]any{
		"workingDirectoryPath": workingDirectoryPath,
		"command":              command,
	})
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          "terminal.run",
		Output:        ToolOutput{Content: content},
		ToolInputKey:  canonicalToolCallKey("terminal.run", input),
	}
}

func longTerminalOutput(finalLine string) string {
	lines := []string{}
	for index := 1; index <= terminalObservationTailMaxLines+1; index++ {
		lines = append(lines, "line "+strconv.Itoa(index))
	}
	lines = append(lines, finalLine)
	return strings.Join(lines, "\n")
}

func messagesText(messages []llm.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func TestExecutionStateSchemaIsPresentOnToolCalls(t *testing.T) {
	schemaDocument := buildActionSchemaFromToolDefinitions([]ToolDefinition{{Name: "terminal.run"}}, false, nil, false)
	var schema struct {
		OneOf []map[string]any `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, variant := range schema.OneOf {
		properties := mapFromAny(variant["properties"])
		if action := mapFromAny(properties["action"]); containsString(stringSliceFromAny(action["enum"]), "continue") {
			if _, exists := properties["executionStateUpdate"]; !exists {
				t.Fatalf("expected executionStateUpdate on continue schema: %s", schemaDocument)
			}
		}
	}
}
