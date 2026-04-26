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

func TestConnectorRuntimeIgnoresEventWhileBackupPrepareIsActive(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, slog.Default())
	connectorRuntime.UseIngressGate(staticIngressGate{isPaused: true})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), &testAdapter{}, PlatformInboundEvent{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		SenderID:       "sender-1",
		ReplyTargetID:  "reply-target-1",
		Prompt:         "hello",
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Ignored || result.Reason != "backup_prepare_active" {
		t.Fatalf("expected backup prepare result, got %+v", result)
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatal("expected no task while backup prepare is active")
	}
}

type staticIngressGate struct {
	isPaused bool
}

func (staticIngressGate staticIngressGate) IsPaused() bool {
	return staticIngressGate.isPaused
}
