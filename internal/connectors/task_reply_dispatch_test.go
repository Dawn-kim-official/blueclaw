package connectors

import (
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

func TestDecideTaskReplySuppressesCancelledTask(t *testing.T) {
	decision := decideTaskReply(agent.AgentTurnResult{
		TaskRun: task.TaskRun{Status: task.TaskStatusCompleted},
	}, true)

	if decision.Kind != taskReplyDecisionSuppressCancelled || decision.Reason != "task_cancelled" {
		t.Fatalf("expected cancelled suppression, got %+v", decision)
	}
}

func TestDecideTaskReplySuppressesSupersededInterruptedTask(t *testing.T) {
	decision := decideTaskReply(agent.AgentTurnResult{
		TaskRun: task.TaskRun{Status: task.TaskStatusInterrupted, FailureReason: "superseded_by_new_message"},
	}, false)

	if decision.Kind != taskReplyDecisionSuppressSuperseded || decision.Reason != "superseded_by_new_message" {
		t.Fatalf("expected superseded suppression, got %+v", decision)
	}
}

func TestDecideTaskReplyDoesNotInspectAttachmentClaimText(t *testing.T) {
	decision := decideTaskReply(agent.AgentTurnResult{
		TaskRun:       task.TaskRun{Status: task.TaskStatusCompleted},
		FinishMessage: "파일을 첨부했습니다.",
	}, false)

	if decision.Kind != taskReplyDecisionSendFinal {
		t.Fatalf("expected final reply decision, got %+v", decision)
	}
}

func TestDecideTaskReplySendsCompletedReply(t *testing.T) {
	decision := decideTaskReply(agent.AgentTurnResult{
		TaskRun:       task.TaskRun{Status: task.TaskStatusCompleted},
		FinishMessage: "완료했습니다.",
	}, false)

	if decision.Kind != taskReplyDecisionSendFinal {
		t.Fatalf("expected final reply, got %+v", decision)
	}
}
