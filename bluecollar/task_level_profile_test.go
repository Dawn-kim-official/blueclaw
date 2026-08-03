package bluecollar

import "testing"

func TestTaskLevelProfileMapping(t *testing.T) {
	profile := TaskLevelProfileForLevel(TaskLevelMedium)

	if profile.MaxIterationCount != 180 || profile.MaxToolCallCount != 100 || profile.Duration.Minutes() != 20 {
		t.Fatalf("expected medium profile, got %+v", profile)
	}
}
