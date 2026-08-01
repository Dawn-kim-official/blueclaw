package connectors

import (
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func TestPendingApprovalActiveGoalIsActiveAndDirectsExecution(t *testing.T) {
	approval := pendingApproval{
		TaskRun:      task.TaskRun{TaskRunID: "task-1"},
		IntentPrompt: "send Chris a DM",
		ActiveGoal:   bluecollar.ActiveGoal{CurrentObjective: "send Chris a calendar reminder DM"},
	}

	goal := pendingApprovalActiveGoal(approval, "approved")

	if goal.Status != bluecollar.ActiveGoalStatusActive {
		t.Fatalf("expected active goal status after approval, got %q", goal.Status)
	}
	if !strings.Contains(goal.CurrentObjective, "do not call ask_confirm again") {
		t.Fatalf("expected execution directive in objective, got %q", goal.CurrentObjective)
	}
	if !strings.Contains(goal.CurrentObjective, "send Chris a calendar reminder DM") {
		t.Fatalf("expected the original objective to be preserved, got %q", goal.CurrentObjective)
	}
}
