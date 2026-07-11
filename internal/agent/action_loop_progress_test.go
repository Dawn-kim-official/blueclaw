package agent

import "testing"

func TestProgressEventsCapsFailureProgressWithoutSuccess(t *testing.T) {
	fingerprints := []string{"fp-a", "fp-b", "fp-c", "fp-d", "fp-e", "fp-f"}
	observations := []turnObservation{}
	for _, fingerprint := range fingerprints {
		observations = append(observations, turnObservation{
			ObservationID:      fingerprint,
			Action:             "continue",
			Tool:               fingerprint,
			Failure:            &ToolFailure{},
			AttemptFingerprint: fingerprint,
		})
	}
	if got := countFailureProgress(progressEvents(observations)); got != maxFailureProgressSinceSuccess {
		t.Fatalf("expected failure progress capped at %d, got %d", maxFailureProgressSinceSuccess, got)
	}

	withSuccess := append([]turnObservation{}, observations[:4]...)
	withSuccess = append(withSuccess, turnObservation{ObservationID: "ok", Action: "continue", Tool: "file.write", Output: ToolOutput{Content: "wrote"}})
	withSuccess = append(withSuccess, observations[4:]...)
	if got := countFailureProgress(progressEvents(withSuccess)); got != len(fingerprints) {
		t.Fatalf("expected a success to reset the failure-progress cap, got %d", got)
	}
}

func countFailureProgress(events []progressEvent) int {
	count := 0
	for _, event := range events {
		if event.Kind == "failure_fingerprint" {
			count++
		}
	}
	return count
}

func TestActionProgressTrackerStopsAfterThreeActionsWithoutProgress(t *testing.T) {
	tracker := newActionProgressTracker(nil)

	first := tracker.evaluate(nil)
	second := tracker.evaluate(nil)
	third := tracker.evaluate(nil)

	if first.shouldStop() || second.shouldStop() {
		t.Fatalf("expected first two no-progress actions to remain recoverable, got first=%+v second=%+v", first, second)
	}
	if !third.shouldStop() {
		t.Fatalf("expected third no-progress action to stop, got %+v", third)
	}
}

func TestActionProgressTrackerResetsWhenProgressAppears(t *testing.T) {
	tracker := newActionProgressTracker(nil)
	tracker.evaluate(nil)
	tracker.evaluate(nil)

	progress := tracker.evaluate([]turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "web.search",
		Output:        ToolOutput{Content: "ok"},
	}})
	afterProgress := tracker.evaluate([]turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "web.search",
		Output:        ToolOutput{Content: "ok"},
	}})

	if !progress.HasProgress {
		t.Fatalf("expected progress, got %+v", progress)
	}
	if afterProgress.ConsecutiveNoProgressActionCount != 1 || afterProgress.shouldStop() {
		t.Fatalf("expected no-progress count reset after progress, got %+v", afterProgress)
	}
}

func TestSelectToolsWithoutToolCallDoesNotCountAsProgress(t *testing.T) {
	tracker := newActionProgressTracker(nil)
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "tool.request",
		Output:        ToolOutput{Content: "selected"},
	}}

	first := tracker.evaluate(observations)
	second := tracker.evaluate(append(observations, turnObservation{
		ObservationID: "obs-002",
		Action:        "tool.request",
		Output:        ToolOutput{Content: "selected again"},
	}))
	third := tracker.evaluate(append(observations, turnObservation{
		ObservationID: "obs-002",
		Action:        "tool.request",
		Output:        ToolOutput{Content: "selected again"},
	}, turnObservation{
		ObservationID: "obs-003",
		Action:        "tool.request",
		Output:        ToolOutput{Content: "selected a third time"},
	}))

	if first.HasProgress || second.HasProgress || !third.shouldStop() {
		t.Fatalf("expected bare request_tools loop to stop without progress, got first=%+v second=%+v third=%+v", first, second, third)
	}
}

func TestSelectToolsCountsAsProgressAfterSuccessfulToolCall(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "tool.request",
		Output:        ToolOutput{Content: "selected"},
	}, {
		ObservationID: "obs-002",
		Action:        "continue",
		Tool:          "site.create",
		Output:        ToolOutput{Content: "created"},
	}}

	if progressEventCount(observations) != 2 {
		t.Fatalf("expected request_tools and successful tool call to count, got %+v", progressEvents(observations))
	}
}

func TestInspectionToolSuccessDoesNotCountAsLoopProgress(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.status",
		Output:        ToolOutput{Content: `{"workspaceHealth":"missing_source"}`},
	}}

	if progressEventCount(observations) != 0 {
		t.Fatalf("expected inspection tool to not count as loop progress, got %+v", progressEvents(observations))
	}
}

func TestSkillSearchSuccessDoesNotCountAsLoopProgress(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          SkillSearchToolName,
		Output:        ToolOutput{Content: `{"skills":[{"name":"scheduled-task"}]}`},
	}}

	if progressEventCount(observations) != 0 {
		t.Fatalf("expected skill discovery not to count as task progress, got %+v", progressEvents(observations))
	}
}

func TestEvaluateRecoveryAllowanceReportsRemainingBudget(t *testing.T) {
	failedObservation := terminalFailureObservation("obs-001", "tmp/deck", "bun run build", "missing package.json")
	allowance := evaluateRecoveryAllowance([]turnObservation{failedObservation}, defaultRecoveryBudget())

	if !allowance.CanRecover {
		t.Fatalf("expected recovery budget to remain, got %+v", allowance)
	}
}
