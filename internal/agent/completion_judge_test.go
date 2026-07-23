package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
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

func TestCompletionJudgeLedgerCapsAtMostRecentEntries(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	observations := []turnObservation{}
	for index := 0; index < completionJudgeMaxLedgerEntries+5; index++ {
		observations = append(observations, successfulSideEffectObservation("obs", "task.list", `{"page":`+strconv.Itoa(index)+`}`, "listed"))
	}

	ledger := completionJudgeLedger(toolSet, observations)

	if len(ledger) != completionJudgeMaxLedgerEntries {
		t.Fatalf("expected the ledger to cap at %d entries, got %d", completionJudgeMaxLedgerEntries, len(ledger))
	}
	if ledger[len(ledger)-1].Input != `{"page":`+strconv.Itoa(completionJudgeMaxLedgerEntries+4)+`}` {
		t.Fatalf("expected the most recent observations to survive the cap, got %+v", ledger[len(ledger)-1])
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
		ActiveGoal:      ActiveGoal{OriginalInstruction: "분기 결산 누락 확인 업무를 추가해줘"},
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeMessage, Description: "완료 확인", Required: true}}},
	}

	messages := completionJudgeMessages(request, nil)
	joined := joinedMessageContent(messages)

	if !strings.Contains(joined, "분기 결산 누락 확인 업무를 추가해줘") {
		t.Fatalf("expected original instruction in judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, "완료 확인") {
		t.Fatalf("expected expected-result description in judge prompt, got %s", joined)
	}
}

func TestCompletionJudgeMessagesIncludeTemporalContext(t *testing.T) {
	turnStartedAt := time.Date(2026, 7, 23, 1, 43, 0, 0, time.UTC)
	request := AgentTurnRequest{Prompt: "내일 일정 옮겨줘", TurnStartedAt: turnStartedAt}

	joined := joinedMessageContent(completionJudgeMessages(request, nil))

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

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-1", request, nil)

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

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-2", request, nil)

	if !result.IsSatisfied {
		t.Fatalf("expected fail-open satisfied result on malformed judge content, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-2"), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event for malformed content")
	}
}

func TestEvaluateCompletionJudgeRecordsUnsatisfiedVerdictEvent(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate가 없습니다"],"reason":"마감일 누락"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-3", request, nil)

	if result.IsSatisfied {
		t.Fatal("expected an unsatisfied judge result")
	}
	if !strings.Contains(result.Message, "마감일 누락") || !strings.Contains(result.Message, "endDate가 없습니다") {
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
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate가 없습니다"],"reason":"마감일 누락"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "분기 결산 누락 확인 업무를 7월 24일 마감으로 추가해줘",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
	}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task.add", `{"title":"분기 결산 누락 확인"}`, "created")}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-6", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the judge to reject a semantically incomplete finish")
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one judge call, got %d", len(languageModel.requests))
	}
}

func TestCompletionGateRunsJudgeFromLedgerWhenContractIsEmpty(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: llm.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate가 비어 있습니다"],"reason":"마감일 미설정"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "업무 추가", ToolSet: completionJudgeTestToolSet()}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task.add", `{"title":"t"}`, `{"endDate":""}`)}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-ledger", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the ledger-triggered judge to reject the finish")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-ledger"), "completion_judge.verdict", `"satisfied":false`) {
		t.Fatal("expected a completion_judge.verdict event from the ledger-triggered judge")
	}
}
