package bluecollar

import (
	"context"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/model"
)

func TestTaskContextCompactionTriggersOnlyOverBudget(t *testing.T) {
	observations := numberedContextSummaryObservations(12, 2000, "history")
	summaryResponse := `{"goal":"ship","completedSteps":["rolled summary"],"artifacts":[],"keyDecisions":[],"exhaustedRecoveryRoutes":[],"activeFailureDebt":[],"nextPlan":["finish"]}`
	languageModel := &sequenceLanguageModel{contents: []string{summaryResponse, finishMessageDocument("done")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ContextWindowTokens: 1000})

	_, errorValue := services.runner.nextAction(context.Background(), "task-1", AgentTurnRequest{Prompt: "ship"}, nil, observations, ExecutionState{}, TaskContextSummary{}, true)

	if errorValue != nil {
		t.Fatalf("expected over-budget action to succeed: %v", errorValue)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected summary and action requests, got %d", len(languageModel.requests))
	}
	if languageModel.requests[0].StructuredOutputSchema.Name != "blueclaw_task_context_summary" {
		t.Fatalf("expected first request to compact context, got %s", languageModel.requests[0].StructuredOutputSchema.Name)
	}

	underBudgetModel := &sequenceLanguageModel{contents: []string{finishMessageDocument("done")}}
	underBudgetServices := newTurnRunnerTestServices(underBudgetModel, TurnOptions{ContextWindowTokens: 1000000})

	_, errorValue = underBudgetServices.runner.nextAction(context.Background(), "task-2", AgentTurnRequest{Prompt: "ship"}, nil, observations, ExecutionState{}, TaskContextSummary{}, true)

	if errorValue != nil {
		t.Fatalf("expected under-budget action to succeed: %v", errorValue)
	}
	if len(underBudgetModel.requests) != 1 {
		t.Fatalf("expected only action request under budget, got %d", len(underBudgetModel.requests))
	}
	if underBudgetModel.requests[0].StructuredOutputSchema.Name == "blueclaw_task_context_summary" {
		t.Fatal("did not expect summary request under budget")
	}
}

func TestTaskContextCompactionReplacesOldPromptObservationsOnly(t *testing.T) {
	observations := numberedContextSummaryObservations(12, 2000, "OLD_MARKER")
	summaryResponse := `{"goal":"ship","completedSteps":["rolled summary"],"artifacts":["/workspace/site/index.html"],"keyDecisions":[],"exhaustedRecoveryRoutes":[],"activeFailureDebt":[],"nextPlan":["finish"]}`
	languageModel := &sequenceLanguageModel{contents: []string{summaryResponse, finishMessageDocument("done")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ContextWindowTokens: 1000})

	_, errorValue := services.runner.nextAction(context.Background(), "task-1", AgentTurnRequest{Prompt: "ship"}, nil, observations, ExecutionState{}, TaskContextSummary{}, true)

	if errorValue != nil {
		t.Fatalf("expected action to succeed: %v", errorValue)
	}
	actionPrompt := structuredRequestText(languageModel.requests[1])
	if strings.Contains(actionPrompt, "OLD_MARKER-001") {
		t.Fatalf("expected compacted observation to be removed from action prompt")
	}
	if !strings.Contains(actionPrompt, "rolled summary") {
		t.Fatalf("expected synthetic summary in action prompt, got %s", actionPrompt)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-1"), taskContextSummaryEventName, "rolled summary") {
		t.Fatalf("expected context summary event to be persisted")
	}
	if !strings.Contains(observations[0].ContentText(), "OLD_MARKER-001") {
		t.Fatalf("expected canonical observations to stay intact, got %+v", observations[0])
	}
}

func TestTaskContextCompactionPinsActiveFailureDebt(t *testing.T) {
	observations := numberedContextSummaryObservations(5, 2000, "OLD_MARKER")
	failureObservation := newFailureObservation("obs-006", "continue", "terminal.run", "ACTIVE_FAILURE_MARKER", toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "terminal_run")
	failureObservation.ToolInputKey = "terminal.run\x00failed"
	observations = append(observations, failureObservation)
	for index := 7; index <= 18; index++ {
		observation := newContentObservation(nextObservationID(index), "recovery_guidance", "", strings.Repeat("recovery ", 250))
		observation.Summary = observation.ContentText()
		observations = append(observations, observation)
	}
	summaryResponse := `{"goal":"recover","completedSteps":["old work"],"artifacts":[],"keyDecisions":[],"exhaustedRecoveryRoutes":[],"activeFailureDebt":["ACTIVE_FAILURE_MARKER"],"nextPlan":["recover"]}`
	languageModel := &sequenceLanguageModel{contents: []string{summaryResponse, finishMessageDocument("done")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ContextWindowTokens: 1000})

	_, errorValue := services.runner.nextAction(context.Background(), "task-1", AgentTurnRequest{Prompt: "recover"}, nil, observations, ExecutionState{}, TaskContextSummary{}, true)

	if errorValue != nil {
		t.Fatalf("expected action to succeed: %v", errorValue)
	}
	actionPrompt := structuredRequestText(languageModel.requests[1])
	if !strings.Contains(actionPrompt, "ACTIVE_FAILURE_MARKER") {
		t.Fatalf("expected active failure debt to remain verbatim, got %s", actionPrompt)
	}
	if strings.Contains(actionPrompt, "OLD_MARKER-001") {
		t.Fatalf("expected older non-pinned observation to be compacted")
	}
}

func TestTaskContextSummaryTruncationIsNonFatal(t *testing.T) {
	observations := numberedContextSummaryObservations(12, 2000, "OLD_MARKER")
	languageModel := &truncatingSummaryLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ContextWindowTokens: 1000})

	_, errorValue := services.runner.nextAction(context.Background(), "task-1", AgentTurnRequest{Prompt: "ship"}, nil, observations, ExecutionState{}, TaskContextSummary{}, true)

	if errorValue != nil {
		t.Fatalf("expected summary truncation to be non-fatal: %v", errorValue)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected failed summary plus action request, got %d", len(languageModel.requests))
	}
	actionPrompt := structuredRequestText(languageModel.requests[1])
	if !strings.Contains(actionPrompt, "OLD_MARKER-001") {
		t.Fatalf("expected un-compacted observations to be preserved after truncation, got %s", actionPrompt)
	}
}

func numberedContextSummaryObservations(count int, contentSize int, markerPrefix string) []turnObservation {
	observations := []turnObservation{}
	for index := 1; index <= count; index++ {
		marker := markerPrefix + "-" + strings.TrimPrefix(nextObservationID(index), "obs-")
		observation := newContentObservation(nextObservationID(index), "continue", "terminal.run", marker+" "+strings.Repeat("x", contentSize))
		observation.Summary = marker
		observations = append(observations, observation)
	}
	return observations
}

func structuredRequestText(request model.StructuredResponseRequest) string {
	parts := []string{request.StructuredOutputSchema.Name}
	for _, message := range request.Messages {
		parts = append(parts, message.Role, message.Content)
		for _, part := range message.Parts {
			parts = append(parts, part.Text, part.DataBase64)
		}
	}
	return strings.Join(parts, "\n")
}

type truncatingSummaryLanguageModel struct {
	requests []model.StructuredResponseRequest
}

func (languageModel *truncatingSummaryLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *truncatingSummaryLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if request.StructuredOutputSchema.Name == "blueclaw_task_context_summary" {
		return model.StructuredResponse{}, llm.StructuredOutputTruncatedError{FinishReason: "length", ContentBytes: 128}
	}
	return model.StructuredResponse{Content: finishMessageDocument("done")}, nil
}
