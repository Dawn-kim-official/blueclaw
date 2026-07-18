package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func newTimePressureRunner() *AgentTurnRunner {
	return &AgentTurnRunner{
		options: TurnOptions{
			MaxIterationCount: 72,
			MaxToolCallCount:  30,
			MaxElapsedSecond:  int((40 * time.Minute).Seconds()),
		},
	}
}

func TestLimitPressureLevelRisesWithElapsedWhileStepsAreLow(t *testing.T) {
	runner := newTimePressureRunner()
	maxElapsed := 40 * time.Minute

	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{elapsed: 0, want: ""},
		{elapsed: time.Duration(float64(maxElapsed) * 0.4), want: ""},
		{elapsed: time.Duration(float64(maxElapsed) * 0.5), want: "budget"},
		{elapsed: time.Duration(float64(maxElapsed) * 0.75), want: "consolidate"},
		{elapsed: time.Duration(float64(maxElapsed) * 0.95), want: "finalize"},
	}
	for _, testCase := range cases {
		level := runner.limitPressureLevel(1, 0, testCase.elapsed)
		if level != testCase.want {
			t.Fatalf("elapsed %s: expected level %q, got %q", testCase.elapsed, testCase.want, level)
		}
	}
}

func TestLimitPressureLevelUsesMaxOfStepAndTime(t *testing.T) {
	runner := newTimePressureRunner()
	highSteps := runner.limitPressureLevel(68, 28, 0)
	if highSteps != "finalize" {
		t.Fatalf("expected step pressure to still drive finalize, got %q", highSteps)
	}
	highTimeLowSteps := runner.limitPressureLevel(1, 0, 39*time.Minute)
	if highTimeLowSteps != "finalize" {
		t.Fatalf("expected elapsed pressure to drive finalize, got %q", highTimeLowSteps)
	}
}

func TestExecutionEffortClockDoesNotIncludePreflightTime(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{MaxElapsedSecond: 30}}

	if runner.currentEffortElapsed(time.Now()) {
		t.Fatal("expected a fresh execution effort budget after preflight")
	}
	if !runner.currentEffortElapsed(time.Now().Add(-31 * time.Second)) {
		t.Fatal("expected execution effort budget to expire from its own start time")
	}
}

func TestAgentTurnRunnerCancelsModelCallAtExecutionEffortDeadline(t *testing.T) {
	primaryLanguageModel := deadlineBlockingLanguageModel{}
	recoveryLanguageModel := &sequenceLanguageModel{
		contents:      []string{recoveryDecisionDocument("model call exceeded the task budget", "no result", "retry", "report the timeout")},
		textResponses: []string{"작업 시간 제한에 도달해 중지했습니다. 다시 시도해 주세요."},
	}
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	runner := NewAgentTurnRunnerWithRecoveryModel(
		taskRunService,
		task.NewTaskStepService(),
		task.NewTaskArtifactService(),
		primaryLanguageModel,
		recoveryLanguageModel,
		TurnOptions{MaxElapsedSecond: 1},
	)

	runContext, cancelRun := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRun()
	startedAt := time.Now()
	result, errorValue := runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "run a bounded task",
		ToolSet:           newTestToolSet(nil),
		EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded limit result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed block, got %+v", result.TaskRun)
	}
	if result.UserNotice != recoveryLanguageModel.textResponses[0] {
		t.Fatalf("expected an honest model-authored limit reply, got %q", result.UserNotice)
	}
	if elapsed := time.Since(startedAt); elapsed >= 1500*time.Millisecond {
		t.Fatalf("expected model call to respect remaining effort budget, took %s", elapsed)
	}
}

func TestAgentTurnRunnerFinishesElapsedActionErrorFromExactEvidence(t *testing.T) {
	primaryLanguageModel := &nativeActionErrorLanguageModel{}
	recoveryLanguageModel := &sequenceLanguageModel{textResponses: []string{"고객지원 분기 결산 업무가 남아 있습니다."}}
	services := newTurnRunnerTestServicesWithRecoveryModel(
		primaryLanguageModel,
		recoveryLanguageModel,
		TurnOptions{MaxElapsedSecond: 1, LimitFinalizationGrace: 500 * time.Millisecond},
	)
	toolRegistry := newTestToolSet([]string{"task.list"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "task.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"count":1,"tasks":[{"title":"고객지원 분기 결산"}]}`), nil
	})
	startedAt := time.Now()

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "내 업무를 조회해서 고객지원 분기 결산 업무가 남아 있는지 알려줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.list"},
		EffortStartedAt:       time.Now().Add(-800 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded evidence completion, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", result.TaskRun)
	}
	if result.FinishMessage != recoveryLanguageModel.textResponses[0] {
		t.Fatalf("expected model-authored completion reply, got %q", result.FinishMessage)
	}
	if primaryLanguageModel.chatCalls != 2 || primaryLanguageModel.finalizerCalls != 1 {
		t.Fatalf("expected two native actions and one bounded finalizer, got chat=%d finalizer=%d", primaryLanguageModel.chatCalls, primaryLanguageModel.finalizerCalls)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.limit_stop", "max_elapsed") {
		t.Fatal("expected max_elapsed stop before post-limit wording")
	}
	if !taskEventsContain(taskEvents, "agent.limit_completed_from_evidence", "max_elapsed") {
		t.Fatal("expected elapsed evidence completion")
	}
	if len(recoveryLanguageModel.textPrompts) != 1 || !strings.Contains(recoveryLanguageModel.textPrompts[0], "- task.list:") {
		t.Fatalf("expected exact task.list evidence in completion prompt, got %+v", recoveryLanguageModel.textPrompts)
	}
	if elapsed := time.Since(startedAt); elapsed >= 1500*time.Millisecond {
		t.Fatalf("expected finalizer to respect the effort deadline, took %s", elapsed)
	}
}

func TestAgentTurnRunnerFinalizesSuccessfulSideEffectAtExecutionEffortDeadline(t *testing.T) {
	languageModel := &elapsedFinalizationLanguageModel{
		firstAction: `{"action":"continue","toolName":"task.add","toolInput":{"prompt":"분기 결산 운영 검토"}}`,
		finalAction: finishMessageWithEvidence("분기 결산 운영 검토 업무를 등록했습니다.", "obs-001", "task.add", 0),
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1, LimitFinalizationGrace: 500 * time.Millisecond})
	toolRegistry := newTestToolSet([]string{"task.add"})
	toolCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"taskID":"task-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "분기 결산 운영 검토 업무를 등록해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.add"},
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed finalization to complete, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", result.TaskRun)
	}
	if result.FinishMessage != "분기 결산 운영 검토 업무를 등록했습니다." {
		t.Fatalf("expected model-authored completion reply, got %q", result.FinishMessage)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected the side effect exactly once, got %d", toolCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.finalizer_action", "obs-001") {
		t.Fatal("expected deadline finalization to cite successful evidence")
	}
}

func TestAgentTurnRunnerCompletesSuccessfulSideEffectWhenElapsedFinalizerFails(t *testing.T) {
	languageModel := &elapsedFinalizationLanguageModel{
		firstAction:    `{"action":"continue","toolName":"task.add","toolInput":{"prompt":"분기 결산 운영 검토"}}`,
		finalizerError: context.DeadlineExceeded,
	}
	recoveryLanguageModel := &sequenceLanguageModel{textResponses: []string{"분기 결산 운영 검토 업무를 등록했습니다."}}
	services := newTurnRunnerTestServicesWithRecoveryModel(languageModel, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})
	toolRegistry := newTestCapabilityToolSet([]string{"task.add"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"taskID":"task-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "분기 결산 운영 검토 업무를 등록해줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.add"},
		EffortStartedAt:       time.Now().Add(-750 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed evidence completion, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", result.TaskRun)
	}
	if result.FinishMessage != recoveryLanguageModel.textResponses[0] {
		t.Fatalf("expected model-authored completion reply, got %q", result.FinishMessage)
	}
	if languageModel.finalizerCalls != 1 {
		t.Fatalf("expected one failed finalizer attempt, got %d", languageModel.finalizerCalls)
	}
	if len(recoveryLanguageModel.textPrompts) != 1 || !strings.Contains(recoveryLanguageModel.textPrompts[0], "- task.add:") {
		t.Fatalf("expected normalized task.add evidence in completion prompt, got %+v", recoveryLanguageModel.textPrompts)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_state_finalized", "task.add") {
		t.Fatal("expected completion to preserve successful evidence")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_completed_from_evidence", "max_elapsed") {
		t.Fatal("expected elapsed evidence completion event")
	}
}

func TestAgentTurnRunnerCompletesSuccessfulReadAtExecutionEffortDeadline(t *testing.T) {
	primaryLanguageModel := &elapsedFinalizationLanguageModel{
		firstAction: `{"action":"continue","toolName":"task.list","toolInput":{"query":"고객지원 분기 결산"},"goalSatisfied":true,"hasRemainingWork":false}`,
	}
	recoveryLanguageModel := &sequenceLanguageModel{textResponses: []string{"고객지원 분기 결산 검토 완료 업무가 남아 있습니다."}}
	services := newTurnRunnerTestServicesWithRecoveryModel(primaryLanguageModel, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1, LimitFinalizationGrace: 500 * time.Millisecond})
	toolRegistry := newTestToolSet([]string{"task.list"})
	toolCallCount := 0
	toolRegistry.RegisterTool(ToolDefinition{Name: "task.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return ToolSuccess(`{"count":1,"tasks":[{"title":"고객지원 분기 결산 검토 완료"}]}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "내 업무를 조회해서 고객지원 분기 결산 업무가 남아 있는지 알려줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed read completion, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", result.TaskRun)
	}
	if result.FinishMessage != recoveryLanguageModel.textResponses[0] {
		t.Fatalf("expected model-authored read result, got %q", result.FinishMessage)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one successful read before the deadline, got %d", toolCallCount)
	}
	if len(recoveryLanguageModel.textPrompts) != 1 || !strings.Contains(recoveryLanguageModel.textPrompts[0], "- task.list:") {
		t.Fatalf("expected task.list evidence in completion prompt, got %+v", recoveryLanguageModel.textPrompts)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_completed_from_evidence", "max_elapsed") {
		t.Fatal("expected elapsed read completion event")
	}
}

func TestAgentTurnRunnerDoesNotCompleteReadWithoutModelCompletionIntent(t *testing.T) {
	primaryLanguageModel := &elapsedFinalizationLanguageModel{
		firstAction: `{"action":"continue","toolName":"task.list","toolInput":{"query":"고객지원 분기 결산"},"goalSatisfied":false,"hasRemainingWork":true}`,
	}
	recoveryLanguageModel := &sequenceLanguageModel{
		contents:      []string{recoveryDecisionDocument("execution time elapsed", "task list was read", "retry", "report incomplete work")},
		textResponses: []string{"업무 조회는 완료했지만 요청한 작업은 아직 끝나지 않았습니다."},
	}
	services := newTurnRunnerTestServicesWithRecoveryModel(primaryLanguageModel, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})
	toolRegistry := newTestToolSet([]string{"task.list"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "task.list"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(`{"count":1,"tasks":[{"title":"고객지원 분기 결산 검토 완료"}]}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "업무를 찾아서 제목을 바꿔줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded incomplete result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed block without completion intent, got %+v", result.TaskRun)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_completed_from_evidence", "max_elapsed") {
		t.Fatal("expected no elapsed completion from an intermediate read")
	}
}

func TestCompletionIntentDoesNotSurviveLaterIncompleteToolAction(t *testing.T) {
	goalSatisfied := true
	state := agentTaskState{}
	updateCompletionIntent(&state, turnActionDocument{GoalSatisfied: &goalSatisfied}, newContentObservation("obs-001", "continue", "task.list", `{"count":1}`))
	if state.CompletionIntentToolName != "task.list" {
		t.Fatalf("expected explicit completion intent, got %q", state.CompletionIntentToolName)
	}

	goalSatisfied = false
	updateCompletionIntent(&state, turnActionDocument{GoalSatisfied: &goalSatisfied, HasRemainingWork: true}, newContentObservation("obs-002", "continue", "task.list", `{"count":1}`))
	if state.CompletionIntentToolName != "" {
		t.Fatalf("expected later incomplete action to clear stale intent, got %q", state.CompletionIntentToolName)
	}
}

func TestElapsedCompletionHonorsCallerCancellation(t *testing.T) {
	recoveryLanguageModel := &cancellationRecoveryLanguageModel{started: make(chan struct{})}
	services := newTurnRunnerTestServicesWithRecoveryModel(deadlineBlockingLanguageModel{}, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})
	request := AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1", Prompt: "업무를 등록해줘"}
	requirements := []toolUseRequirement{{ToolName: "task.add"}}
	finalization := limitFinalizationResult{Observations: []turnObservation{newContentObservation("obs-001", "continue", "task.add", `{"taskID":"task-1"}`)}}
	callerContext, cancelCaller := context.WithCancel(context.Background())
	resultChannel := make(chan limitFinalizationResult, 1)

	go func() {
		resultChannel <- services.runner.finalizeElapsedLimitWithEvidence(callerContext, "task-1", request, "max_elapsed", requirements, nil, finalization)
	}()
	<-recoveryLanguageModel.started
	cancelCaller()

	select {
	case result := <-resultChannel:
		if result.IsCompleted {
			t.Fatal("expected caller cancellation to stop finalization")
		}
	case <-time.After(time.Second):
		t.Fatal("expected finalization to honor caller cancellation")
	}
}

func TestElapsedCompletionDoesNotReuseEffortContext(t *testing.T) {
	recoveryLanguageModel := &contextInspectingLanguageModel{textResponse: "업무를 등록했습니다."}
	services := newTurnRunnerTestServicesWithRecoveryModel(deadlineBlockingLanguageModel{}, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})
	request := AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1", Prompt: "업무를 등록해줘"}
	requirements := []toolUseRequirement{{ToolName: "task.add"}}
	finalization := limitFinalizationResult{Observations: []turnObservation{newContentObservation("obs-001", "continue", "task.add", `{"taskID":"task-1"}`)}}
	result := services.runner.finalizeElapsedLimitWithEvidence(context.Background(), "task-1", request, "max_elapsed", requirements, nil, finalization)

	if !result.IsCompleted {
		t.Fatal("expected detached finalization context to complete")
	}
	if recoveryLanguageModel.contextError != nil {
		t.Fatalf("expected live finalization context, got %v", recoveryLanguageModel.contextError)
	}
}

func TestElapsedCompletionDoesNotSubstituteDifferentSideEffectEvidence(t *testing.T) {
	recoveryLanguageModel := &sequenceLanguageModel{textResponses: []string{"업무를 수정했습니다."}}
	services := newTurnRunnerTestServicesWithRecoveryModel(deadlineBlockingLanguageModel{}, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})
	request := AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "고객지원 분기 결산 업무를 수정해줘",
	}
	requirements := []toolUseRequirement{{ToolName: "task.history"}}
	finalization := limitFinalizationResult{
		Observations: []turnObservation{newContentObservation("obs-001", "continue", "task.update", `{"taskID":"task-1"}`)},
	}

	result := services.runner.finalizeElapsedLimitWithEvidence(context.Background(), "task-1", request, "max_elapsed", requirements, nil, finalization)

	if result.IsCompleted {
		t.Fatal("expected task.update not to satisfy a different task.history requirement")
	}
	if len(recoveryLanguageModel.textPrompts) != 0 {
		t.Fatal("expected no completion wording without matching required evidence")
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent("task-1"), "agent.limit_completed_from_evidence", "max_elapsed") {
		t.Fatal("expected no max_elapsed completion event for mismatched evidence")
	}
}

func TestAgentTurnRunnerPreservesCallerCancellationBeforeEffortDeadline(t *testing.T) {
	services := newTurnRunnerTestServices(deadlineBlockingLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "취소할 작업",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("expected cancellation result, got %v", errorValue)
	}
	if !result.ReplySuppressed {
		t.Fatal("expected caller cancellation to suppress the reply")
	}
	if result.TaskRun.FailureReason == "max_elapsed" {
		t.Fatalf("expected caller cancellation to remain distinct from max_elapsed, got %+v", result.TaskRun)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
		t.Fatal("expected no max_elapsed stop event for caller cancellation")
	}
}

func TestAgentTurnRunnerPreservesCallerDeadlineBeforeEffortDeadline(t *testing.T) {
	services := newTurnRunnerTestServices(deadlineBlockingLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelRun()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "시간 제한 전에 취소할 작업",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("expected caller deadline result, got %v", errorValue)
	}
	if !result.ReplySuppressed {
		t.Fatal("expected caller deadline to suppress the reply")
	}
	if result.TaskRun.FailureReason == "max_elapsed" {
		t.Fatalf("expected caller deadline to remain distinct from max_elapsed, got %+v", result.TaskRun)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
		t.Fatal("expected no max_elapsed stop event for caller deadline")
	}
}

func TestAgentTurnRunnerBoundsToolCallAtExecutionEffortDeadline(t *testing.T) {
	primaryLanguageModel := &elapsedFinalizationLanguageModel{
		firstAction: `{"action":"continue","toolName":"slow.tool","toolInput":{}}`,
	}
	recoveryLanguageModel := &sequenceLanguageModel{
		contents:      []string{recoveryDecisionDocument("tool call exceeded the task budget", "no result", "retry", "report the timeout")},
		textResponses: []string{"작업 시간 제한에 도달해 중지했습니다."},
	}
	services := newTurnRunnerTestServicesWithRecoveryModel(primaryLanguageModel, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})
	toolRegistry := newTestToolSet([]string{"slow.tool"})
	toolCancelled := make(chan struct{})
	toolRegistry.RegisterTool(ToolDefinition{Name: "slow.tool"}, func(toolContext context.Context, _ ToolInvocation) (ToolResult, error) {
		<-toolContext.Done()
		close(toolCancelled)
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "slow.tool", toolContext.Err().Error()), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "bounded tool call",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		EffortStartedAt:   time.Now(),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded limit result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed block, got %+v", result.TaskRun)
	}
	select {
	case <-toolCancelled:
	default:
		t.Fatal("expected tool call to receive the execution effort deadline")
	}
}

func TestAgentTurnRunnerUsesRawLimitFallbackWhenRecoveryFails(t *testing.T) {
	primaryLanguageModel := deadlineBlockingLanguageModel{}
	recoveryLanguageModel := failingRecoveryLanguageModel{errorValue: errors.New("recovery unavailable")}
	services := newTurnRunnerTestServicesWithRecoveryModel(primaryLanguageModel, recoveryLanguageModel, TurnOptions{MaxElapsedSecond: 1})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "raw fallback",
		ToolSet:           newTestToolSet(nil),
		EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded limit result, got %v", errorValue)
	}
	if result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed failure, got %+v", result.TaskRun)
	}
	if result.FailureNotice.Source != "raw_error" {
		t.Fatalf("expected raw error fallback, got %+v", result.FailureNotice)
	}
	if result.UserNotice != "Execution limit reached; completed progress was saved for continuation." {
		t.Fatalf("expected compact raw error summary, got %q", result.UserNotice)
	}
	if strings.Contains(result.UserNotice, "max_elapsed") {
		t.Fatalf("raw fallback exposed internal runtime jargon: %q", result.UserNotice)
	}
}

func TestAgentTurnRunnerBlocksBeforeLimitWordingAndBoundsGrace(t *testing.T) {
	recoveryLanguageModel := &blockingLimitWordingLanguageModel{started: make(chan struct{})}
	services := newTurnRunnerTestServicesWithRecoveryModel(
		deadlineBlockingLanguageModel{},
		recoveryLanguageModel,
		TurnOptions{MaxElapsedSecond: 1, LimitFinalizationGrace: 40 * time.Millisecond},
	)
	resultChannel := make(chan AgentTurnResult, 1)
	startedAt := time.Now()

	go func() {
		result, _ := services.runner.RunTurn(context.Background(), AgentTurnRequest{
			RequesterPersonID: "person-1",
			ConversationID:    "conversation-1",
			Prompt:            "bounded task",
			ResponseLanguage:  ResponseLanguageEnglish,
			ToolSet:           newTestToolSet(nil),
			EffortStartedAt:   time.Now().Add(-950 * time.Millisecond),
		})
		resultChannel <- result
	}()

	<-recoveryLanguageModel.started
	taskRuns := services.taskRunService.ListTaskRunByPersonID("person-1")
	if len(taskRuns) != 1 || taskRuns[0].Status != task.TaskStatusBlocked {
		t.Fatalf("expected max_elapsed to block before wording, got %+v", taskRuns)
	}
	select {
	case result := <-resultChannel:
		if result.TaskRun.Status != task.TaskStatusBlocked || result.FailureNotice.Source != "raw_error" {
			t.Fatalf("expected bounded raw fallback, got %+v", result)
		}
		if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
			t.Fatal("expected limit stop before fallback")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected total finalization grace to bound the turn")
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Fatalf("expected bounded post-limit return, took %s", elapsed)
	}
	if recoveryLanguageModel.calls != 1 {
		t.Fatalf("expected one user wording attempt, got %d", recoveryLanguageModel.calls)
	}
}

func TestAgentTurnRunnerSharesLimitGraceAcrossFinalizerAndFallback(t *testing.T) {
	primaryLanguageModel := &elapsedFinalizationLanguageModel{
		firstAction:      `{"action":"continue","toolName":"task.add","toolInput":{"prompt":"분기 결산"}}`,
		finalizerStarted: make(chan struct{}),
	}
	recoveryLanguageModel := &countingLimitWordingLanguageModel{}
	services := newTurnRunnerTestServicesWithRecoveryModel(
		primaryLanguageModel,
		recoveryLanguageModel,
		TurnOptions{MaxElapsedSecond: 1, LimitFinalizationGrace: 40 * time.Millisecond},
	)
	toolRegistry := newTestToolSet([]string{"task.add"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		time.Sleep(30 * time.Millisecond)
		return ToolSuccess(`{"taskID":"task-1"}`), nil
	})
	startedAt := time.Now()

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "분기 결산 업무를 등록해줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"task.add"},
		EffortStartedAt:       time.Now().Add(-990 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded finalization fallback, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.FailureNotice.Source != "raw_error" {
		t.Fatalf("expected blocked raw fallback, got %+v", result)
	}
	if primaryLanguageModel.finalizerCalls != 1 {
		t.Fatalf("expected one evidence finalizer call, got %d", primaryLanguageModel.finalizerCalls)
	}
	if recoveryLanguageModel.calls != 0 {
		t.Fatalf("expected exhausted shared grace not to start another wording call, got %d", recoveryLanguageModel.calls)
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Fatalf("expected one total grace deadline, took %s", elapsed)
	}
}

func TestReplyFinalizationContextCarriesRequesterAndDeadline(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{LimitFinalizationGrace: 40 * time.Millisecond}}
	request := AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1", Platform: "mattermost"}
	finalizationContext, cancelFinalization := runner.replyFinalizationContext(context.Background(), request)
	defer cancelFinalization()

	deadline, hasDeadline := finalizationContext.Deadline()
	if !hasDeadline || time.Until(deadline) > 50*time.Millisecond {
		t.Fatalf("expected explicit short finalization deadline, got %v %v", deadline, hasDeadline)
	}
	requestContext := llm.RequestContextFromContext(finalizationContext)
	if requestContext.RequesterPersonID != request.RequesterPersonID || requestContext.ConversationID != request.ConversationID {
		t.Fatalf("expected requester context in finalization grace, got %+v", requestContext)
	}
}

func TestRecoveryFinalizationContextCarriesRequesterWithoutDeadline(t *testing.T) {
	request := AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterEmail:    "person@example.com",
		ConversationID:    "conversation-1",
		Platform:          "mattermost",
	}
	recoveryContext, cancelRecovery := recoveryFinalizationContext(request)
	defer cancelRecovery()

	if _, hasDeadline := recoveryContext.Deadline(); hasDeadline {
		t.Fatal("expected recovery finalization without an arbitrary deadline")
	}
	requestContext := llm.RequestContextFromContext(recoveryContext)
	if requestContext.RequesterPersonID != request.RequesterPersonID || requestContext.ConversationID != request.ConversationID {
		t.Fatalf("expected requester context to survive recovery detachment, got %+v", requestContext)
	}
}

type deadlineBlockingLanguageModel struct{}

type cancellationRecoveryLanguageModel struct {
	started chan struct{}
}

type contextInspectingLanguageModel struct {
	textResponse string
	contextError error
}

type blockingLimitWordingLanguageModel struct {
	started chan struct{}
	calls   int
}

type countingLimitWordingLanguageModel struct {
	calls int
}

func (languageModel *blockingLimitWordingLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	languageModel.calls++
	close(languageModel.started)
	<-responseContext.Done()
	return "", responseContext.Err()
}

func (*blockingLimitWordingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("unexpected structured recovery call")
}

func (languageModel *countingLimitWordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	languageModel.calls++
	return "unexpected recovery reply", nil
}

func (*countingLimitWordingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("unexpected structured recovery call")
}

func (languageModel *contextInspectingLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	languageModel.contextError = responseContext.Err()
	return languageModel.textResponse, nil
}

func (*contextInspectingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("structured recovery is unavailable")
}

func (languageModel *cancellationRecoveryLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	close(languageModel.started)
	<-responseContext.Done()
	return "", responseContext.Err()
}

func (*cancellationRecoveryLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("structured recovery is unavailable")
}

func newTurnRunnerTestServicesWithRecoveryModel(primaryLanguageModel llm.LanguageModelProvider, recoveryLanguageModel llm.LanguageModelProvider, options TurnOptions) turnRunnerTestServices {
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	return turnRunnerTestServices{
		runner:              NewAgentTurnRunnerWithRecoveryModel(taskRunService, taskStepService, taskArtifactService, primaryLanguageModel, recoveryLanguageModel, options),
		taskRunService:      taskRunService,
		taskEventService:    taskEventService,
		taskStepService:     taskStepService,
		taskArtifactService: taskArtifactService,
	}
}

func (deadlineBlockingLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	<-responseContext.Done()
	return "", responseContext.Err()
}

func (deadlineBlockingLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	<-responseContext.Done()
	return llm.StructuredResponse{}, responseContext.Err()
}

type elapsedFinalizationLanguageModel struct {
	firstAction      string
	finalAction      string
	finalizerError   error
	finalizerStarted chan struct{}
	finalizerCalls   int
	actionCount      int
}

type nativeActionErrorLanguageModel struct {
	chatCalls      int
	finalizerCalls int
}

func (*nativeActionErrorLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *nativeActionErrorLanguageModel) GenerateStructuredResponse(responseContext context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != "blueclaw_agent_turn_finalizer" {
		return llm.StructuredResponse{}, errors.New("unexpected structured request")
	}
	languageModel.finalizerCalls++
	<-responseContext.Done()
	return llm.StructuredResponse{}, responseContext.Err()
}

func (languageModel *nativeActionErrorLanguageModel) GenerateChatCompletion(_ context.Context, _ llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	languageModel.chatCalls++
	if languageModel.chatCalls == 1 {
		return llm.ChatCompletionResponse{
			FinishReason: "tool_calls",
			Message: llm.ChatCompletionMessage{
				Role:      "assistant",
				ToolCalls: []llm.ChatCompletionToolCall{nativeAgentActionToolCall("task.list", `{}`)},
			},
		}, nil
	}
	return llm.ChatCompletionResponse{
		FinishReason: "stop",
		Message:      llm.ChatCompletionMessage{Role: "assistant", Content: "고객지원 분기 결산 업무가 남아 있습니다."},
	}, nil
}

func (languageModel *elapsedFinalizationLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *elapsedFinalizationLanguageModel) GenerateStructuredResponse(responseContext context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "blueclaw_agent_turn_finalizer" {
		languageModel.finalizerCalls++
		if languageModel.finalizerStarted != nil {
			close(languageModel.finalizerStarted)
			<-responseContext.Done()
			return llm.StructuredResponse{}, responseContext.Err()
		}
		if languageModel.finalizerError != nil {
			return llm.StructuredResponse{}, languageModel.finalizerError
		}
		return llm.StructuredResponse{Content: languageModel.finalAction}, nil
	}
	languageModel.actionCount++
	if languageModel.actionCount == 1 {
		return llm.StructuredResponse{Content: languageModel.firstAction}, nil
	}
	<-responseContext.Done()
	return llm.StructuredResponse{}, responseContext.Err()
}

func TestLimitPressureMessageIncludesElapsedWhenBounded(t *testing.T) {
	message := limitPressureMessage("budget", 2, 30, 1, 72, 20*time.Minute, 40*time.Minute)
	if !strings.Contains(message, "Time: 20m0s/40m0s elapsed.") {
		t.Fatalf("expected elapsed time in message, got %q", message)
	}
}

func TestLimitPressureMessageOmitsElapsedWhenUnbounded(t *testing.T) {
	message := limitPressureMessage("budget", 2, 30, 1, 72, 0, 0)
	if strings.Contains(message, "Time:") {
		t.Fatalf("expected no time line when unbounded, got %q", message)
	}
}
