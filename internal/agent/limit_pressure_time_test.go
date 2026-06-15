package agent

import (
	"strings"
	"testing"
	"time"
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
