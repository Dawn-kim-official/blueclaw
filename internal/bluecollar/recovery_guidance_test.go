package bluecollar

import (
	"context"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"testing"
)

func TestAgentTurnRunnerAllowsCorrectedRetryAfterSafeFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"Dana","message":"please take a look"}}`,
		`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"Dana Lee","message":"please take a look"}}`,
		finishMessageWithEvidence("sent", "obs-003", "message_send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"message_send"})
	callCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_send"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		callCount++
		if callCount == 1 {
			return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
		}
		return testToolSuccess(`{"dispatchID":"post-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "send dm",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message_send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message_send"}},
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
	failure := newFailureObservation("obs-001", "continue", "message_send", "failed", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "message_send")
	passiveGuidance := recoveryGuidanceObservation(nil, 2, failure, "")
	spentGuidance := recoveryGuidanceObservation(nil, 3, failure, "")
	spentGuidance.RecoveryAttemptSpent = true
	retryObservation := failure
	retryObservation.ObservationID = "obs-004"
	retryObservation.RecoveryAttemptKey = "message_send\x00{}"
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
		`{"action":"continue","toolName":"file_read","toolInput":{"path":"home/sites/site-1/draft/app/src/App.tsx"}}`,
		finishMessageDocument("Checked."),
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
	toolRegistry := newHybridKernelCapabilityToolSet([]string{"file_read", "file_edit"}, []string{"site.build"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "site.build"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "source failed"},
			Failure: &toolcontract.ToolFailure{
				Kind:            toolcontract.FailureInvalidInput,
				Code:            toolcontract.FailureCodes.InvalidInput.String(),
				Stage:           "site_build_source",
				UserSafeSummary: "site source failed",
				Retryable:       true,
				FailureClass:    failureClassQuality,
				RetryPolicy:     retryPolicyAfterPrecondition,
				RecoveryHints:   []toolcontract.RecoveryHint{{Action: "edit_resource", ToolNames: []string{"file_read", "file_edit"}}},
			},
		}, nil
	})
	fileReadCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_read", SideEffectClass: toolcontract.ToolSideEffectRead}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		fileReadCount++
		return testToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","content":"broken"}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_edit"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","matchCount":1}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "look into the site build problem",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"site.build", "file_read", "file_edit"},
	})
	if errorValue != nil {
		t.Fatalf("expected inspection recovery to continue: %v", errorValue)
	}
	if fileReadCount != 1 {
		t.Fatalf("expected file_read to run despite exhausted adjacent budget, got %d", fileReadCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(events, "agent.recovery_budget_exhausted", "file_read") {
		t.Fatal("did not expect inspection tool to be blocked by adjacent recovery budget")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "inspection") {
		t.Fatal("expected inspection recovery event")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "precondition") {
		t.Fatal("expected precondition recovery event")
	}
}
