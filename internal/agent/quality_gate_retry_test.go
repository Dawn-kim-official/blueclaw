package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func gateTestRunner() *AgentTurnRunner {
	return &AgentTurnRunner{options: TurnOptions{
		MaxIterationCount: 40,
		MaxToolCallCount:  40,
		MaxElapsedSecond:  3600,
	}}
}

func gateObservation(t *testing.T, index int, passed bool, score float64) turnObservation {
	t.Helper()
	stdout := "Building requested formats: pdf\nQUALITY_GATE " + string(mustMarshalGate(t, passed, score)) + "\nDone."
	content, errorValue := json.Marshal(map[string]any{"exitCode": 0, "stdout": stdout, "stderr": ""})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return newContentObservation(nextObservationID(index), "continue", "terminal.run", string(content))
}

func mustMarshalGate(t *testing.T, passed bool, score float64) []byte {
	t.Helper()
	document, errorValue := json.Marshal(map[string]any{"source": "presentation-review", "passed": passed, "score": score})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return document
}

func TestQualityGateReportsParseTerminalOutput(t *testing.T) {
	observations := []turnObservation{
		newContentObservation(nextObservationID(1), "continue", "file.write", `{"path":"slides.html"}`),
		gateObservation(t, 2, false, 18),
	}
	reports := qualityGateReports(observations)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Passed || reports[0].Score == nil || *reports[0].Score != 18 {
		t.Fatalf("unexpected report: %+v", reports[0])
	}
}

func TestQualityGateRetryRequiredWhileImproving(t *testing.T) {
	runner := gateTestRunner()
	observations := []turnObservation{
		gateObservation(t, 1, false, 0),
		gateObservation(t, 2, false, 18),
	}
	message, retryRequired := runner.qualityGateRetryDirective(observations, 2, time.Now())
	if !retryRequired {
		t.Fatal("expected retry to be required for an improving failed gate with budget")
	}
	if message == "" {
		t.Fatal("expected a retry directive message")
	}
}

func TestQualityGateRetryNotRequiredWhenScoreStalls(t *testing.T) {
	runner := gateTestRunner()
	observations := []turnObservation{
		gateObservation(t, 1, false, 18),
		gateObservation(t, 2, false, 18),
	}
	if _, retryRequired := runner.qualityGateRetryDirective(observations, 2, time.Now()); retryRequired {
		t.Fatal("expected best-effort delivery when the score stopped improving")
	}
}

func TestQualityGateRetryNotRequiredWhenGatePasses(t *testing.T) {
	runner := gateTestRunner()
	observations := []turnObservation{
		gateObservation(t, 1, false, 60),
		gateObservation(t, 2, true, 90),
	}
	if _, retryRequired := runner.qualityGateRetryDirective(observations, 2, time.Now()); retryRequired {
		t.Fatal("expected no retry requirement after the gate passed")
	}
}

func TestQualityGateRetryNotRequiredWithoutReports(t *testing.T) {
	runner := gateTestRunner()
	observations := []turnObservation{
		newContentObservation(nextObservationID(1), "continue", "file.write", `{"path":"slides.html"}`),
	}
	if _, retryRequired := runner.qualityGateRetryDirective(observations, 1, time.Now()); retryRequired {
		t.Fatal("expected no retry requirement without gate reports")
	}
}

func TestQualityGateRetryNotRequiredUnderBudgetPressure(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{
		MaxIterationCount: 4,
		MaxToolCallCount:  4,
		MaxElapsedSecond:  3600,
	}}
	observations := []turnObservation{
		gateObservation(t, 1, false, 0),
		gateObservation(t, 2, false, 18),
		gateObservation(t, 3, false, 30),
	}
	if _, retryRequired := runner.qualityGateRetryDirective(observations, 3, time.Now()); retryRequired {
		t.Fatal("expected best-effort delivery once budget pressure reached consolidate")
	}
}

func TestQualityGateSingleScorelessFailureForcesOneRetry(t *testing.T) {
	runner := gateTestRunner()
	scorelessContent, errorValue := json.Marshal(map[string]any{"exitCode": 0, "stdout": "QUALITY_GATE {\"passed\": false}", "stderr": ""})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	first := []turnObservation{newContentObservation(nextObservationID(1), "continue", "terminal.run", string(scorelessContent))}
	if _, retryRequired := runner.qualityGateRetryDirective(first, 1, time.Now()); !retryRequired {
		t.Fatal("expected the first scoreless failure to force a retry")
	}
	second := append(first, newContentObservation(nextObservationID(2), "continue", "terminal.run", string(scorelessContent)))
	if _, retryRequired := runner.qualityGateRetryDirective(second, 2, time.Now()); retryRequired {
		t.Fatal("expected scoreless failures to stall after the forced retry")
	}
}
