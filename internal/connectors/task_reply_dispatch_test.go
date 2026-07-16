package connectors

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/identity"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestCompletedTaskReplyCarriesModelWordingAndNativeAttachments(t *testing.T) {
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
		FinishMessage: "완료했습니다: sandbox:/mnt/data/deck.pptx",
		Attachments:   []agent.FileAttachment{{Filename: "deck.pptx", DevicePath: "/workspace/private/people/p1/tmp/deck.pptx"}},
	}

	_, errorValue := connectorRuntime.dispatchTaskReply(context.Background(), "mattermost", &testAdapter{}, PlatformInboundEvent{SenderID: "sender-1"}, ReplyTarget{}, turnResult, sendReply)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(sentReply.Attachments) != 1 {
		t.Fatalf("expected the deliverable attachment to be carried, got %d", len(sentReply.Attachments))
	}
	if sentReply.Message != turnResult.FinishMessage {
		t.Fatalf("expected connector to preserve model wording, got %q", sentReply.Message)
	}
}

func TestFailedTaskReplyPreservesModelWording(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	connectorRuntime := NewConnectorRuntime(
		identity.NewIdentityService(policy.PolicyProjection{}),
		agent.NewAgentKernel(taskRunService, task.NewTaskStepService()),
		slog.Default(),
	)
	message := "실패 내역은 file:///tmp/report.txt에서 확인했습니다."
	var sentReply OutboundReply
	sendReply := func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
		sentReply = reply
		return "dispatch-1", nil
	}
	turnResult := agent.AgentTurnResult{
		TaskRun:    task.TaskRun{TaskRunID: "task-1", Status: task.TaskStatusFailed},
		UserNotice: message,
	}

	_, errorValue := connectorRuntime.dispatchTaskReply(context.Background(), "mattermost", &testAdapter{}, PlatformInboundEvent{SenderID: "sender-1"}, ReplyTarget{}, turnResult, sendReply)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.HasPrefix(sentReply.Message, message) {
		t.Fatalf("expected connector to preserve failure wording, got %q", sentReply.Message)
	}
}
