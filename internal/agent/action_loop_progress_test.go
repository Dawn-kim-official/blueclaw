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

func TestEvaluateRecoveryAllowanceReportsRemainingBudget(t *testing.T) {
	failedObservation := terminalFailureObservation("obs-001", "tmp/deck", "bun run build", "missing package.json")
	allowance := evaluateRecoveryAllowance([]turnObservation{failedObservation}, defaultRecoveryBudget())

	if !allowance.CanRecover {
		t.Fatalf("expected recovery budget to remain, got %+v", allowance)
	}
}
