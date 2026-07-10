package connectors

import (
	"context"
	"log/slog"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/identity"
	"blueclaw/internal/policy"
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

// The final completed-task reply is always a normal message, never ephemeral.
// Ephemeral is reserved for checkpoints and ask.choice/ask.confirm prompts;
// an ephemeral final reply vanishes and cannot carry native file attachments,
// so the requester would silently lose the deliverable.
func TestCompletedTaskReplyIsNeverEphemeral(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, slog.Default())

	var sentReply OutboundReply
	sendReply := func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
		sentReply = reply
		return "dispatch-1", nil
	}

	turnResult := agent.AgentTurnResult{
		TaskRun:       task.TaskRun{TaskRunID: "task-1", Status: task.TaskStatusCompleted},
		FinishMessage: "완료했습니다.",
		Attachments:   []agent.FileAttachment{{Filename: "deck.pptx", DevicePath: "/workspace/private/people/p1/tmp/deck.pptx"}},
	}

	_, errorValue := connectorRuntime.dispatchTaskReply(context.Background(), "mattermost", &testAdapter{}, PlatformInboundEvent{SenderID: "sender-1"}, ReplyTarget{}, turnResult, sendReply)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if sentReply.EphemeralUserID != "" {
		t.Fatalf("expected the final reply to be a normal post, got ephemeral to %q", sentReply.EphemeralUserID)
	}
	if len(sentReply.Attachments) != 1 {
		t.Fatalf("expected the deliverable attachment to be carried, got %d", len(sentReply.Attachments))
	}
}
