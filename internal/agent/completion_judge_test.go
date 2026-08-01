package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
)

type completionJudgeStubLanguageModel struct {
	response   llm.StructuredResponse
	errorValue error
	requests   []llm.StructuredResponseRequest
}

func (model *completionJudgeStubLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (model *completionJudgeStubLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	model.requests = append(model.requests, request)
	return model.response, model.errorValue
}

func completionJudgeTestToolSet() *ToolSet {
	return newTestToolSetWithDefinitions([]ToolDefinition{
		testToolDescriptor("task.add"),
		testToolDescriptor("task.list"),
	})
}

func successfulSideEffectObservation(observationID string, toolName string, toolInput string, resultContent string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolName,
		ToolID:        "test:" + toolName,
		ToolInput:     json.RawMessage(toolInput),
		Output:        ToolOutput{Content: resultContent},
	}
}

func completionJudgeFinishActionDocument() turnActionDocument {
	goalSatisfied := true
	return turnActionDocument{Action: "finish", GoalStatus: "satisfied", GoalSatisfied: &goalSatisfied}
}

func TestCompletionJudgeMessagesCarryTheFinishReplyAsDelivered(t *testing.T) {
	actionDocument := turnActionDocument{Action: "finish", Message: "deploy complete: https://sites.example/launch"}
	joined := joinedMessageContent(completionJudgeMessages(AgentTurnRequest{Prompt: "publish the site and give me the link"}, nil, nil, actionDocument))
	if !strings.Contains(joined, "https://sites.example/launch") {
		t.Fatalf("expected the finish reply text in the judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, "delivers to the user") || !strings.Contains(joined, "never require a separate send or delivery operation") {
		t.Fatalf("expected the delivered-reply framing in the judge prompt, got %s", joined)
	}
}

func TestOutcomeContractHasSideEffectEvidenceForRequiredEvidenceTools(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	contract := OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}
	if !outcomeContractHasSideEffectEvidence(toolSet, contract) {
		t.Fatal("expected a state-changing required evidence tool to trigger the judge")
	}
}

func TestOutcomeContractHasSideEffectEvidenceForRequiredEvidenceAnyOf(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	contract := OutcomeContract{RequiredEvidenceAnyOf: [][]string{{"task.list"}, {"task.add"}}}
	if !outcomeContractHasSideEffectEvidence(toolSet, contract) {
		t.Fatal("expected a side-effect tool inside any RequiredEvidenceAnyOf group to trigger the judge")
	}
}

func TestOutcomeContractHasSideEffectEvidenceFalseForReadOnlyTools(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	contract := OutcomeContract{RequiredEvidenceTools: []string{"task.list"}}
	if outcomeContractHasSideEffectEvidence(toolSet, contract) {
		t.Fatal("expected a read-only required evidence tool to not trigger the judge")
	}
}

func TestCompletionJudgeLedgerIncludesSuccessfulReadsAndWrites(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	observations := []turnObservation{
		successfulSideEffectObservation("obs-001", "task.add", `{"title":"a"}`, "created"),
		successfulSideEffectObservation("obs-002", "task.list", `{}`, "listed"),
		{ObservationID: "obs-003", Tool: "task.add", ToolID: "test:task.add", Failure: &ToolFailure{}},
	}

	ledger := completionJudgeLedger(toolSet, observations)

	if len(ledger) != 2 || ledger[0].Tool != "task.add" || ledger[1].Tool != "task.list" {
		t.Fatalf("expected successful reads and writes without the failed call, got %+v", ledger)
	}
}

func TestCompletionJudgeLedgerBudgetsBytesAndNamesDroppedEntries(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	observations := []turnObservation{}
	observationCount := completionJudgeLedgerByteBudget/completionJudgeInputMaxLength + 4
	for index := 0; index < observationCount; index++ {
		bulkyInput := `{"page":` + strconv.Itoa(index) + `,"filler":"` + strings.Repeat("x", completionJudgeInputMaxLength-40) + `"}`
		observations = append(observations, successfulSideEffectObservation("obs", "task.list", bulkyInput, "listed"))
	}

	ledger := completionJudgeLedger(toolSet, observations)

	if len(ledger) >= observationCount {
		t.Fatalf("expected the byte budget to drop early entries, got %d of %d", len(ledger), observationCount)
	}
	if ledger[0].Tool != "earlier_operations" || !strings.Contains(ledger[0].Result, "not shown here") {
		t.Fatalf("expected a marker naming the dropped entries, got %+v", ledger[0])
	}
	if !strings.Contains(ledger[len(ledger)-1].Input, `{"page":`+strconv.Itoa(observationCount-1)) {
		t.Fatalf("expected the most recent observation to survive the budget, got %+v", ledger[len(ledger)-1])
	}
}

func TestCompletionJudgeLedgerTruncatesInputAndResult(t *testing.T) {
	longInput := strings.Repeat("a", completionJudgeInputMaxLength+50)
	longResult := strings.Repeat("b", completionJudgeResultMaxLength+50)
	toolSet := completionJudgeTestToolSet()
	observation := successfulSideEffectObservation("obs-001", "task.add", `{"note":"`+longInput+`"}`, longResult)

	ledger := completionJudgeLedger(toolSet, []turnObservation{observation})

	if len(ledger) != 1 {
		t.Fatalf("expected one ledger entry, got %+v", ledger)
	}
	if !strings.HasPrefix(ledger[0].Input, `{"note":"aaa`) || !strings.Contains(ledger[0].Input, "…[display truncated; full ") {
		t.Fatalf("expected truncated input with an explicit display marker, got %q", ledger[0].Input[len(ledger[0].Input)-80:])
	}
	if !strings.Contains(ledger[0].Result, "…[display truncated; full ") {
		t.Fatalf("expected truncated result with an explicit display marker, got %q", ledger[0].Result)
	}
}

func TestCompletionJudgeMessagesIncludeOriginalInstructionAndExpectedResults(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:          "fallback prompt",
		ActiveGoal:      ActiveGoal{OriginalInstruction: "add a task to check the missing quarterly settlement"},
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeMessage, Description: "completion check", Required: true}}},
	}

	messages := completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument())
	joined := joinedMessageContent(messages)

	if !strings.Contains(joined, "add a task to check the missing quarterly settlement") {
		t.Fatalf("expected original instruction in judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, "completion check") {
		t.Fatalf("expected expected-result description in judge prompt, got %s", joined)
	}
}

func TestCompletionJudgeMessagesIncludeTemporalContext(t *testing.T) {
	turnStartedAt := time.Date(2026, 7, 23, 1, 43, 0, 0, time.UTC)
	request := AgentTurnRequest{Prompt: "move tomorrow's schedule", TurnStartedAt: turnStartedAt}

	joined := joinedMessageContent(completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument()))

	if !strings.Contains(joined, "Runtime temporal context:") {
		t.Fatalf("expected temporal context in judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, buildTemporalContextDescription(turnStartedAt)) {
		t.Fatalf("expected turn-start temporal description in judge prompt, got %s", joined)
	}
}

func TestEvaluateCompletionJudgeFailsOpenOnProviderError(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{errorValue: errors.New("provider unavailable")}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-1", request, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected fail-open satisfied result on provider error, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-1"), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event on provider error")
	}
}

func TestEvaluateCompletionJudgeFailsOpenOnMalformedContent(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: "not json"}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-2", request, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected fail-open satisfied result on malformed judge content, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-2"), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event for malformed content")
	}
}

func TestEvaluateCompletionJudgeRecordsUnsatisfiedVerdictEvent(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate is missing"],"reason":"missing due date"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-3", request, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected an unsatisfied judge result")
	}
	if !strings.Contains(result.Message, "missing due date") || !strings.Contains(result.Message, "endDate is missing") {
		t.Fatalf("expected reason and missing work in the gate message, got %q", result.Message)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-3"), "completion_judge.verdict", `"satisfied":false`) {
		t.Fatal("expected a completion_judge.verdict event recording the unsatisfied verdict")
	}
}

func TestValidateCompletionGateWithJudgeSkipsJudgeWithoutSideEffectEvidence(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":[],"reason":"should not run"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "read only task",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.list"}},
	}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task.list", `{}`, "listed")}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-4", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected deterministic-only satisfied result for a read-only task, got %+v", result)
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected the judge to be skipped for a read-only outcome contract, got %d calls", len(languageModel.requests))
	}
}

func TestValidateCompletionGateWithJudgeSkipsJudgeWhenDeterministicGateFails(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":true,"missingWork":[],"reason":"unused"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "side effect task",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-5", request, nil, nil, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the deterministic gate to fail without any task.add evidence")
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected the judge to be skipped once the deterministic gate already failed, got %d calls", len(languageModel.requests))
	}
}

func TestValidateCompletionGateWithJudgeReturnsJudgeUnsatisfied(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate is missing"],"reason":"missing due date"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "add a task to check the missing quarterly settlement, due July 24",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task.add", `{"title":"missing quarterly settlement check"}`, "created")}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-6", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the judge to reject a semantically incomplete finish")
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one judge call, got %d", len(languageModel.requests))
	}
}

func TestCompletionGateRunsJudgeFromLedgerWhenContractIsEmpty(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate is empty"],"reason":"no due date set"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "add task", ToolSet: completionJudgeTestToolSet()}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task.add", `{"title":"t"}`, `{"endDate":""}`)}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-ledger", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the ledger-triggered judge to reject the finish")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-ledger"), "completion_judge.verdict", `"satisfied":false`) {
		t.Fatal("expected a completion_judge.verdict event from the ledger-triggered judge")
	}
}
