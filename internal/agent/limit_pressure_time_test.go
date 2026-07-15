package agent

import (
	"context"
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

func TestAgentTurnRunnerFinalizesSuccessfulSideEffectAtExecutionEffortDeadline(t *testing.T) {
	languageModel := &elapsedFinalizationLanguageModel{
		firstAction: `{"action":"continue","toolName":"task.add","toolInput":{"prompt":"분기 결산 운영 검토"}}`,
		finalAction: finishMessageWithEvidence("분기 결산 운영 검토 업무를 등록했습니다.", "obs-001", "task.add", 0),
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})
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

func TestRecoveryFinalizationContextIsBoundedAndCarriesRequester(t *testing.T) {
	request := AgentTurnRequest{
		RequesterPersonID: "person-1",
		RequesterEmail:    "person@example.com",
		ConversationID:    "conversation-1",
		Platform:          "mattermost",
	}
	recoveryContext, cancelRecovery := recoveryFinalizationContext(request)
	defer cancelRecovery()

	deadline, hasDeadline := recoveryContext.Deadline()
	if !hasDeadline || time.Until(deadline) <= 0 || time.Until(deadline) > recoveryFinalizationTimeout {
		t.Fatalf("expected bounded recovery deadline, got %v", deadline)
	}
	requestContext := llm.RequestContextFromContext(recoveryContext)
	if requestContext.RequesterPersonID != request.RequesterPersonID || requestContext.ConversationID != request.ConversationID {
		t.Fatalf("expected requester context to survive recovery detachment, got %+v", requestContext)
	}
}

type deadlineBlockingLanguageModel struct{}

func (deadlineBlockingLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	<-responseContext.Done()
	return "", responseContext.Err()
}

func (deadlineBlockingLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	<-responseContext.Done()
	return llm.StructuredResponse{}, responseContext.Err()
}

type elapsedFinalizationLanguageModel struct {
	firstAction string
	finalAction string
	actionCount int
}

func (languageModel *elapsedFinalizationLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *elapsedFinalizationLanguageModel) GenerateStructuredResponse(responseContext context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "blueclaw_agent_turn_finalizer" {
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
