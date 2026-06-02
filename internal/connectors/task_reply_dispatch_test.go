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

func TestDecideTaskReplySuppressesMissingAttachmentClaim(t *testing.T) {
	decision := decideTaskReply(agent.AgentTurnResult{
		TaskRun:       task.TaskRun{Status: task.TaskStatusCompleted},
		FinishMessage: "파일을 첨부했습니다.",
	}, false)

	if decision.Kind != taskReplyDecisionSuppressMissingAttach || decision.Reason != "missing_attachment_evidence" {
		t.Fatalf("expected missing attachment suppression, got %+v", decision)
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
