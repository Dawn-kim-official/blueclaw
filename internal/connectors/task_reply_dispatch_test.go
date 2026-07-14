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
