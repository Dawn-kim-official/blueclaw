package connectors

import (
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

func TestPendingApprovalActiveGoalIsActiveAndDirectsExecution(t *testing.T) {
	approval := pendingApproval{
		TaskRun:      task.TaskRun{TaskRunID: "task-1"},
		IntentPrompt: "견본님께 DM 보내기",
		ActiveGoal:   agent.ActiveGoal{CurrentObjective: "견본님께 캘린더 안내 DM 보내기"},
	}

	goal := pendingApprovalActiveGoal(approval, "approved")

	if goal.Status != agent.ActiveGoalStatusActive {
		t.Fatalf("expected active goal status after approval, got %q", goal.Status)
	}
	if !strings.Contains(goal.CurrentObjective, "do not call ask.confirm again") {
		t.Fatalf("expected execution directive in objective, got %q", goal.CurrentObjective)
	}
	if !strings.Contains(goal.CurrentObjective, "견본님께 캘린더 안내 DM 보내기") {
		t.Fatalf("expected the original objective to be preserved, got %q", goal.CurrentObjective)
	}
}
