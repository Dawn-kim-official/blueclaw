package agent

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/task"
)

func TestStalledOnRedundantInspectionDetectsCacheHit(t *testing.T) {
	if !stalledOnRedundantInspection([]turnObservation{{Summary: "file.read cache hit for app.tsx"}}) {
		t.Fatal("expected a trailing file.read cache hit to signal a redundant inspection stall")
	}
	if stalledOnRedundantInspection([]turnObservation{{Summary: "wrote App.tsx"}}) {
		t.Fatal("expected a non-cache-hit trailing observation to not signal a read stall")
	}
	if stalledOnRedundantInspection(nil) {
		t.Fatal("expected empty observations to not signal a read stall")
	}
}

func TestStalledRecoveryDirectiveNamesFailedToolAndForbidsAsking(t *testing.T) {
	failedBuild := newFailureObservation("obs-001", "continue", "site.app.build", "compile error", FailureExternalService, FailureCodes.OperationFailed, "tool")
	failedBuild.ToolInputKey = "site.app.build:lunch"
	directive := stalledRecoveryDirectiveObservation("obs-099", FailureDebt{LatestFailure: failedBuild})
	if !strings.Contains(directive.Summary, "site.app.build") {
		t.Fatalf("expected directive to name the failed tool, got %q", directive.Summary)
	}
	if !strings.Contains(directive.Summary, "file.edit") || !strings.Contains(directive.Summary, "do not ask") {
		t.Fatalf("expected directive to push an edit and forbid asking, got %q", directive.Summary)
	}
}

func TestContinueStalledRecoveryNudgesReadLoopThenBounds(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	taskRunID := "task-stall-recovery"
	failedBuild := newFailureObservation("obs-001", "continue", "site.app.build", "compile error", FailureExternalService, FailureCodes.OperationFailed, "tool")
	failedBuild.ToolInputKey = "site.app.build:lunch"
	state := &agentTaskState{Observations: []turnObservation{failedBuild}}
	tracker := newActionProgressTracker(state.Observations)
	allowance := recoveryAllowance{CanRecover: true}

	appendCacheHitRead := func() {
		state.Observations = append(state.Observations, turnObservation{
			ObservationID: nextObservationID(len(state.Observations) + 1),
			Action:        "policy",
			Tool:          "file.read",
			Summary:       "file.read cache hit for app.tsx",
		})
	}

	for attempt := 1; attempt <= maxStallRecoveryDirectivesPerEpisode; attempt++ {
		appendCacheHitRead()
		if !services.runner.continueStalledRecoveryIfAllowed(taskRunID, state, &tracker, allowance) {
			t.Fatalf("expected nudge %d for the redundant-read stall", attempt)
		}
	}

	appendCacheHitRead()
	if services.runner.continueStalledRecoveryIfAllowed(taskRunID, state, &tracker, allowance) {
		t.Fatal("expected stall recovery nudges to be bounded within an episode")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRunID), "agent.stall_recovery_directive", "site.app.build") {
		t.Fatal("expected stall recovery directive events naming the failed tool")
	}
}

func TestStallRecoveryBudgetRefreshesAfterRealProgress(t *testing.T) {
	tracker := newActionProgressTracker(nil)
	tracker.stallRecoveryDirectiveCount = maxStallRecoveryDirectivesPerEpisode

	progressObservations := []turnObservation{{
		ObservationID: "obs-progress",
		Action:        "continue",
		Tool:          "file.write",
		Output:        ToolOutput{Content: `{"path":"app.tsx"}`},
	}}
	evaluation := tracker.evaluate(progressObservations)

	if !evaluation.HasProgress {
		t.Fatal("expected a real edit to count as progress")
	}
	if tracker.stallRecoveryDirectiveCount != 0 {
		t.Fatalf("expected real progress to refresh the recovery-nudge budget, got %d", tracker.stallRecoveryDirectiveCount)
	}
}

func TestContinueStalledRecoverySkipsFinishStall(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	failedBuild := newFailureObservation("obs-001", "continue", "terminal.run", "EACCES", FailureExternalService, FailureCodes.OperationFailed, "tool")
	failedBuild.ToolInputKey = "terminal.run:build"
	state := &agentTaskState{Observations: []turnObservation{
		failedBuild,
		{ObservationID: "obs-002", Action: "evidence_missing", Summary: "finish is missing required expected result"},
	}}
	tracker := newActionProgressTracker(state.Observations)

	if services.runner.continueStalledRecoveryIfAllowed("task-finish-stall", state, &tracker, recoveryAllowance{CanRecover: true}) {
		t.Fatal("expected a finish-attempt stall to fall through to the existing pause path, not the read-loop nudge")
	}
}

func TestAgentTurnRunnerEscalatesToolCallLimitAfterDurableProgress(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/index.html","content":"one"}}`,
			`{"action":"continue","toolName":"site.app.build","toolInput":{"siteID":"site-1"}}`,
			`{"action":"continue","toolName":"file.write","toolInput":{"path":"tmp/app/index.html","content":"two"}}`,
			finishMessageDocument("continued after tool-call escalation"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		EffortLevel:       EffortLevelQuick,
		MaxIterationCount: 10,
		MaxToolCallCount:  2,
	})
	toolRegistry := newTestToolSet([]string{"file.write", "site.app.build"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "file.write"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"path":"tmp/app/index.html"}`), nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "site.app.build"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"status":"built"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build the site",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"file.write", "site.app.build"},
	})

	if errorValue != nil {
		t.Fatalf("expected tool-call escalation run, got error: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task after tool-call escalation, got %s", result.TaskRun.Status)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.budget_escalated", `"newEffortLevel":"standard"`) {
		t.Fatalf("expected tool-call limit to escalate the budget, got %+v", taskEvents)
	}
}
