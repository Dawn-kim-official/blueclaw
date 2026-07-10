package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"blueclaw/internal/task"
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

func TestRejectBelowGateDeliveryNeverSuppressesArtifactWhenBudgetExhausted(t *testing.T) {
	services := newTurnRunnerTestServices(nil, TurnOptions{
		MaxIterationCount: 40,
		MaxToolCallCount:  40,
		MaxElapsedSecond:  3600,
	})
	state := &agentTaskState{
		Observations: []turnObservation{
			gateObservation(t, 1, false, 0),
			gateObservation(t, 2, false, 18),
		},
		ToolCallCount: 2,
	}
	actionDocument := turnActionDocument{
		ToolName:  "file.deliver",
		ToolInput: json.RawMessage(`{"files":[{"path":"deck.pdf"}]}`),
	}
	request := AgentTurnRequest{TurnStartedAt: time.Now()}

	budgetExhausted := func(string) (AgentTurnResult, bool) { return AgentTurnResult{}, true }
	if outcome := services.runner.rejectBelowGateDelivery("task-1", "step-1", request, state, actionDocument, budgetExhausted); outcome.WasHandled {
		t.Fatal("expected below-gate delivery to pass through once the no-progress budget is exhausted, not be suppressed")
	}

	budgetRemains := func(string) (AgentTurnResult, bool) { return AgentTurnResult{}, false }
	outcome := services.runner.rejectBelowGateDelivery("task-1", "step-2", request, state, actionDocument, budgetRemains)
	if !outcome.WasHandled {
		t.Fatal("expected below-gate delivery to be deferred while retry budget remains")
	}
	if outcome.ShouldReturn {
		t.Fatal("expected deferral to continue the loop, not end the turn")
	}
}

func TestRunTurnDefersDeliveryUntilQualityGateImproves(t *testing.T) {
	failedGateOutput := `{"exitCode":0,"stdout":"QUALITY_GATE {\"source\":\"presentation-review\",\"passed\":false,\"score\":10}","stderr":""}`
	passedGateOutput := `{"exitCode":0,"stdout":"QUALITY_GATE {\"source\":\"presentation-review\",\"passed\":true,\"score\":90}","stderr":""}`
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build.sh"}}`,
			`{"action":"continue","toolName":"file.deliver","toolInput":{"files":[{"path":"tmp/deck/build/deck.pdf"}]}}`,
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/deck/slides.html","content":"<html>revised</html>"}}`,
			`{"action":"continue","toolName":"terminal.run","toolInput":{"command":"build.sh"}}`,
			`{"action":"continue","toolName":"file.deliver","toolInput":{"files":[{"path":"tmp/deck/build/deck.pdf"}]}}`,
			`{"action":"finish","message":"덱을 전달했습니다.","replyParts":[{"type":"text","text":"덱을 전달했습니다."}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":"obs-005","toolName":"file.deliver"}]}`,
		},
		textResponses: []string{"progress saved"},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		TaskLevel:         TaskLevelLow,
		MaxIterationCount: 20,
		MaxToolCallCount:  20,
	})
	terminalRunCount := 0
	deliverCount := 0
	toolRegistry := newTestToolSet([]string{"terminal.run", "file.write", "file.deliver"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "terminal.run"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		terminalRunCount++
		if terminalRunCount == 1 {
			return ToolSuccess(failedGateOutput), nil
		}
		return ToolSuccess(passedGateOutput), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/deck/slides.html"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.deliver"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		deliverCount++
		return ToolSuccess(`files delivered`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "make the deck",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"terminal.run", "file.write", "file.deliver"},
	})

	if errorValue != nil {
		t.Fatalf("expected completed run, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		for _, taskEvent := range services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID) {
			t.Logf("event %s: %.200s", taskEvent.Name, taskEvent.Body)
		}
		t.Fatalf("expected completed task, got %s (%s)", result.TaskRun.Status, result.TaskRun.FailureReason)
	}
	if deliverCount != 1 {
		t.Fatalf("expected exactly one executed delivery, got %d", deliverCount)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.quality_gate_retry_required", "passed=false") {
		t.Fatalf("expected quality gate retry event, got %+v", taskEvents)
	}
}
