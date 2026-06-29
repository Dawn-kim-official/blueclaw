package agent

import (
	"context"
	"testing"
)

func TestAgentTurnRunnerAllowsCorrectedRetryAfterSafeFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"동하"},"message":"확인 부탁해"}}`,
		`{"action":"continue","toolName":"message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"이동하"},"message":"확인 부탁해"}}`,
		finishMessageWithEvidence("sent", "obs-003", "message.send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 3})
	toolRegistry := newTestToolSet([]string{"message.send"})
	callCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "message.send"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		callCount++
		if callCount == 1 {
			return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
		}
		return ToolSuccess(`{"dispatchID":"post-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "send dm",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message.send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message.send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected retry recovery: %v", errorValue)
	}
	if callCount != 2 {
		t.Fatalf("expected corrected retry, got %d calls", callCount)
	}
	if result.FinishMessage != "sent" {
		t.Fatalf("expected final reply after corrected retry, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_attempt", "corrected_retry") {
		t.Fatal("expected corrected retry event")
	}
}

func TestRecoveryAttemptCountOnlyIncludesSpentInterventions(t *testing.T) {
	failure := newFailureObservation("obs-001", "continue", "message.send", "failed", FailureExternalService, FailureCodes.OperationFailed, "message_send")
	passiveGuidance := recoveryGuidanceObservation(2, failure)
	spentGuidance := recoveryGuidanceObservation(3, failure)
	spentGuidance.RecoveryAttemptSpent = true
	retryObservation := failure
	retryObservation.ObservationID = "obs-004"
	retryObservation.RecoveryAttemptKey = "message.send\x00{}"
	retryObservation.RecoveryAttemptSpent = true

	if count := recoveryAttemptCount([]turnObservation{failure, passiveGuidance}); count != 0 {
		t.Fatalf("expected passive guidance not to spend recovery budget, got %d", count)
	}
	if count := recoveryAttemptCount([]turnObservation{failure, passiveGuidance, spentGuidance, retryObservation}); count != 2 {
		t.Fatalf("expected spent guidance and retry to consume budget, got %d", count)
	}
}

func TestAgentTurnRunnerAllowsInspectionAfterAdjacentRecoveryBudgetExhausted(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
		`{"action":"continue","toolName":"file.read","toolInput":{"path":"home/sites/site-1/draft/app/src/App.tsx"}}`,
		`{"action":"continue","toolName":"file.edit","toolInput":{"path":"home/sites/site-1/draft/app/src/App.tsx","oldText":"broken","newText":"fixed"}}`,
		finishMessageDocument("확인했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 6,
		MaxToolCallCount:  4,
		RecoveryBudget: RecoveryBudget{
			CorrectedRetry: 0,
			AlternateRoute: 0,
			AdjacentTool:   -1,
			NoToolFallback: 0,
		},
	})
	toolRegistry := newTestToolSet([]string{"site.build", "file.read", "file.edit"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output: ToolOutput{Content: "source failed"},
			Failure: &ToolFailure{
				Kind:                  FailureInvalidInput,
				Code:                  FailureCodes.InvalidInput.String(),
				Stage:                 "site_build_source",
				UserSafeSummary:       "site source failed",
				Retryable:             true,
				FailureClass:          failureClassQuality,
				RetryPolicy:           retryPolicyAfterPrecondition,
				RequiredPreconditions: []string{"source_changed"},
				RecoveryHints:         []RecoveryHint{{Action: "edit_resource", ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"}}},
			},
		}, nil
	})
	fileReadCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.read"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fileReadCount++
		return ToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","content":"broken"}`), nil
	})
	fileEditCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.edit"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fileEditCount++
		return ToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","matchCount":1}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "사이트 빌드 문제 확인해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"site.build", "file.read", "file.edit"},
	})
	if errorValue != nil {
		t.Fatalf("expected inspection recovery to continue: %v", errorValue)
	}
	if fileReadCount != 1 {
		t.Fatalf("expected file.read to run despite exhausted adjacent budget, got %d", fileReadCount)
	}
	if fileEditCount != 1 {
		t.Fatalf("expected file.edit precondition action to run despite exhausted adjacent budget, got %d", fileEditCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(events, "agent.recovery_budget_exhausted", "file.read") {
		t.Fatal("did not expect inspection tool to be blocked by adjacent recovery budget")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "inspection") {
		t.Fatal("expected inspection recovery event")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "precondition") {
		t.Fatal("expected precondition recovery event")
	}
}
