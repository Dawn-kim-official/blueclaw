package agent

import (
	"context"
	"encoding/json"
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

func TestRedundantToolSelectionIsDetectedWithUseNowDirective(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.app.create", "site.app.status"})
	base := AgentTurnRequest{ToolSet: toolSet}

	afterFirst, firstResult := applyToolSelectionRequest(base, selectToolsRequest{ToolNames: []string{"site.app.create", "site.app.status"}})
	if toolSelectionAddedNothing(base, afterFirst, firstResult) {
		t.Fatal("first selection of new tools should add tools")
	}

	afterSecond, secondResult := applyToolSelectionRequest(afterFirst, selectToolsRequest{ToolNames: []string{"site.app.create", "site.app.status"}})
	if !toolSelectionAddedNothing(afterFirst, afterSecond, secondResult) {
		t.Fatal("re-selecting already-available tools should add nothing")
	}

	observation := redundantToolSelectionObservation(1, selectToolsRequest{ToolNames: []string{"site.app.create"}}, secondResult)
	if !strings.Contains(observation.Summary, "already available") || !strings.Contains(observation.Summary, "call one of them now") {
		t.Fatalf("expected a use-them-now directive, got %q", observation.Summary)
	}
}

func TestBrowserFailureRecoveryGuidanceRedirectsToWebFetch(t *testing.T) {
	failedBrowser := newFailureObservation("obs-001", "continue", "browser.open", "Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.", FailureDependencyUnavailable, FailureCodes.Unavailable, "browser_open")
	guidance := recoveryGuidanceContent(failedBrowser)
	if !strings.Contains(guidance, "web.fetch") {
		t.Fatalf("expected browser failure to steer toward web.fetch, got %q", guidance)
	}
	nonBrowser := newFailureObservation("obs-002", "continue", "terminal.run", "boom", FailureExternalService, FailureCodes.OperationFailed, "terminal_run")
	if strings.Contains(recoveryGuidanceContent(nonBrowser), "browser tools run on the user's Companion") {
		t.Fatal("expected non-browser failures not to get browser guidance")
	}
}

func toolResultTestEvent(name string, observationID string, tool string, content string, failed bool) task.TaskEvent {
	observation := map[string]any{
		"observationID": observationID,
		"action":        "continue",
		"tool":          tool,
		"output":        map[string]any{"content": content},
	}
	if failed {
		observation["failure"] = map[string]any{"kind": "external_service", "code": "operation_failed", "stage": "tool"}
	}
	body, _ := json.Marshal(observation)
	return task.TaskEvent{Name: name, Body: string(body)}
}

func TestCleanRestartDiscardsPoisonedContextOnReSteerAfterStall(t *testing.T) {
	poisoned := []task.TaskEvent{
		toolResultTestEvent("tool.browser.open.result", "obs-001", "browser.open", "browser URL must be absolute", true),
		{Name: "agent.no_progress_loop_stopped", Body: "{}"},
		{Name: "task.steer.requested", Body: "{}"},
	}
	if !shouldCleanRestartRestoredTask(poisoned) {
		t.Fatal("a user steer after a stall should trigger a clean restart")
	}

	state, errorValue := agentTaskStateForTurn(AgentTurnRequest{IsRuntimeRestartResume: true}, TurnOptions{}, task.TaskRun{TaskRunID: "task-1"}, poisoned)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, observation := range state.Observations {
		if observation.Tool == "browser.open" {
			t.Fatalf("clean restart must discard the poisoned browser.open observation, got %+v", state.Observations)
		}
	}
	if len(state.Observations) == 0 || !strings.Contains(state.Observations[len(state.Observations)-1].Summary, "stalled") {
		t.Fatalf("expected a re-grounding observation, got %+v", state.Observations)
	}
}

func TestCleanRestartPreservesDurablePublishEvidence(t *testing.T) {
	events := []task.TaskEvent{
		toolResultTestEvent("tool.site.app.publish.result", "obs-010", "site.app.publish", `{"publishedURL":"https://x.intern.kim"}`, false),
		toolResultTestEvent("tool.browser.open.result", "obs-011", "browser.open", "garbage", true),
		{Name: "agent.limit_stop", Body: "{}"},
		{Name: "task.steer.requested", Body: "{}"},
	}
	state, _ := agentTaskStateForTurn(AgentTurnRequest{IsRuntimeRestartResume: true}, TurnOptions{}, task.TaskRun{TaskRunID: "task-2"}, events)
	hasPublish := false
	for _, observation := range state.Observations {
		if observation.Tool == "site.app.publish" {
			hasPublish = true
		}
		if observation.Tool == "browser.open" {
			t.Fatal("clean restart must drop the poisoned browser observation while keeping durable publish")
		}
	}
	if !hasPublish {
		t.Fatalf("clean restart must preserve the successful publish observation, got %+v", state.Observations)
	}
}

func TestNonStalledResumeRestoresNormally(t *testing.T) {
	events := []task.TaskEvent{
		toolResultTestEvent("tool.file.read.result", "obs-001", "file.read", "content", false),
	}
	if shouldCleanRestartRestoredTask(events) {
		t.Fatal("a resume without a prior stall must not clean-restart")
	}
	state, _ := agentTaskStateForTurn(AgentTurnRequest{IsRuntimeRestartResume: true}, TurnOptions{}, task.TaskRun{TaskRunID: "task-3"}, events)
	if len(state.Observations) != 1 || state.Observations[0].Tool != "file.read" {
		t.Fatalf("normal resume should restore observations, got %+v", state.Observations)
	}
}

func TestCleanRestartScrubsPoisonedGoalContext(t *testing.T) {
	events := []task.TaskEvent{
		toolResultTestEvent("tool.browser.open.result", "obs-001", "browser.open", "garbage", true),
		{Name: "agent.no_progress_loop_stopped", Body: "{}"},
		{Name: "task.steer.requested", Body: "{}"},
	}
	request := AgentTurnRequest{
		IsRuntimeRestartResume: true,
		ActiveGoal: ActiveGoal{
			KnownContext: []string{"Assess prior progress from the task event ledger and restored observations."},
		},
	}
	state, errorValue := agentTaskStateForTurn(request, TurnOptions{}, task.TaskRun{TaskRunID: "task-1"}, events)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	joined := strings.Join(state.Request.ActiveGoal.KnownContext, " ")
	if strings.Contains(joined, "restored observations") || !strings.Contains(joined, "re-ground") {
		t.Fatalf("clean restart must scrub the poisoned goal context, got %q", joined)
	}
}
