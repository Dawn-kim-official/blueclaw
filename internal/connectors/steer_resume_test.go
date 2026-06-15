package connectors

import (
	"context"
	"testing"

	"blueclaw/internal/agenttest"
)

func TestUserSteerTaskProfileSourceReferenceResolvesRealPlatform(t *testing.T) {
	profile := userSteerTaskProfile("mattermost", "task-1")
	if platformFromSourceReference(profile.sourceReference) != "mattermost" {
		t.Fatalf("steer source reference must resolve the real platform, got %q", profile.sourceReference)
	}
}

func TestResumePausedTaskForSteerWithoutLaunchContextFallsThrough(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	pausedTaskRun, errorValue := connectorRuntime.agentKernel.RunTask("person-1", "direct-1", "사이트 만들어")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-steer-resume")
	sendReply := func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		return "dispatch-1", nil
	}

	result, errorValue := connectorRuntime.resumePausedTaskForSteer(context.Background(), "test", event, ReplyTarget{}, pausedTaskRun, sendReply)

	if errorValue != nil {
		t.Fatalf("missing launch context must fall through without error, got %v", errorValue)
	}
	if result.isHandled {
		t.Fatal("missing launch context must fall through to the normal launch path")
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("fall-through must not send a reply, got %+v", adapter.sentReplies)
	}
}
