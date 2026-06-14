package agent

import "testing"

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
		Action:        "select_tools",
		Output:        ToolOutput{Content: "selected"},
	}}

	first := tracker.evaluate(observations)
	second := tracker.evaluate(append(observations, turnObservation{
		ObservationID: "obs-002",
		Action:        "select_tools",
		Output:        ToolOutput{Content: "selected again"},
	}))
	third := tracker.evaluate(append(observations, turnObservation{
		ObservationID: "obs-002",
		Action:        "select_tools",
		Output:        ToolOutput{Content: "selected again"},
	}, turnObservation{
		ObservationID: "obs-003",
		Action:        "select_tools",
		Output:        ToolOutput{Content: "selected a third time"},
	}))

	if first.HasProgress || second.HasProgress || !third.shouldStop() {
		t.Fatalf("expected bare select_tools loop to stop without progress, got first=%+v second=%+v third=%+v", first, second, third)
	}
}

func TestSelectToolsCountsAsProgressAfterSuccessfulToolCall(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "select_tools",
		Output:        ToolOutput{Content: "selected"},
	}, {
		ObservationID: "obs-002",
		Action:        "continue",
		Tool:          "site.app.create",
		Output:        ToolOutput{Content: "created"},
	}}

	if progressEventCount(observations) != 2 {
		t.Fatalf("expected select_tools and successful tool call to count, got %+v", progressEvents(observations))
	}
}

func TestEvaluateRecoveryAllowanceReportsRemainingBudget(t *testing.T) {
	failedObservation := terminalFailureObservation("obs-001", "tmp/deck", "bun run build", "missing package.json")
	allowance := evaluateRecoveryAllowance([]turnObservation{failedObservation}, defaultRecoveryBudget())

	if !allowance.CanRecover {
		t.Fatalf("expected recovery budget to remain, got %+v", allowance)
	}
}
