package agent

import (
	"encoding/json"
	"testing"
)

func repeatedTaskUpdateFailure(observationID string, taskInput string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          "task.update",
		Failure:       &ToolFailure{Code: FailureCodes.OperationFailed.String(), Stage: "target_resolution"},
		ToolInputKey:  "task.update\x00" + taskInput,
	}
}

func TestEvaluateRecoveryAllowanceStopsRepeatedStructuralFailure(t2 *testing.T) {
	twice := evaluateRecoveryAllowance([]turnObservation{
		repeatedTaskUpdateFailure("obs-1", `{"taskID":"a"}`),
		repeatedTaskUpdateFailure("obs-2", `{"taskID":"b"}`),
	}, defaultRecoveryBudget())
	if !twice.CanRecover {
		t2.Fatalf("expected recovery to remain allowed after two attempts, got %+v", twice)
	}

	thrice := evaluateRecoveryAllowance([]turnObservation{
		repeatedTaskUpdateFailure("obs-1", `{"taskID":"a"}`),
		repeatedTaskUpdateFailure("obs-2", `{"taskID":"b"}`),
		repeatedTaskUpdateFailure("obs-3", `{"taskID":"c"}`),
	}, defaultRecoveryBudget())
	if thrice.CanRecover {
		t2.Fatalf("expected recovery to stop once the same failure signature recurred across cosmetic input changes, got %+v", thrice)
	}
}

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

func TestInspectionToolSuccessDoesNotCountAsLoopProgress(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.list",
		Output:        ToolOutput{Content: `{"workspaceHealth":"missing_source"}`},
	}}

	if progressEventCount(observations) != 0 {
		t.Fatalf("expected inspection tool to not count as loop progress, got %+v", progressEvents(observations))
	}
}

func TestTerminalProgressRequiresStructuredCompletion(t *testing.T) {
	testCases := []struct {
		name        string
		data        json.RawMessage
		failure     *ToolFailure
		hasProgress bool
	}{
		{name: "completed command", data: json.RawMessage(`{"mode":"command","completed":true}`), hasProgress: true},
		{name: "running session", data: json.RawMessage(`{"mode":"session_status","completed":false}`)},
		{name: "session start", data: json.RawMessage(`{"mode":"session_start","completed":false}`)},
		{name: "session write", data: json.RawMessage(`{"mode":"session_write","completed":false}`)},
		{name: "session close", data: json.RawMessage(`{"mode":"session_close","completed":false}`)},
		{name: "missing data"},
		{name: "malformed data", data: json.RawMessage(`{`)},
		{name: "failed command", data: json.RawMessage(`{"mode":"command","completed":true}`), failure: &ToolFailure{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			observation := turnObservation{
				ObservationID: "obs-001",
				Action:        "continue",
				Tool:          TerminalRunToolName,
				Output: ToolOutput{
					Content: `{"exitCode":0,"completed":true}`,
					Data:    testCase.data,
				},
				Failure: testCase.failure,
			}
			_, hasProgress := qualifyingDurableProgressEvent(observation)
			if hasProgress != testCase.hasProgress {
				t.Fatalf("expected progress=%t, got %+v", testCase.hasProgress, observation)
			}
		})
	}
}

func TestEvaluateRecoveryAllowanceReportsRemainingBudget(t *testing.T) {
	failedObservation := terminalFailureObservation("obs-001", "tmp/deck", "bun run build", "missing package.json")
	allowance := evaluateRecoveryAllowance([]turnObservation{failedObservation}, defaultRecoveryBudget())

	if !allowance.CanRecover {
		t.Fatalf("expected recovery budget to remain, got %+v", allowance)
	}
}
