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
	if elapsed := time.Since(startedAt); elapsed >= 1500*time.Millisecond {
		t.Fatalf("expected model call to respect remaining effort budget, took %s", elapsed)
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
