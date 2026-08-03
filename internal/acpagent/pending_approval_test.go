package acpagent

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
)

func TestAHeldCallReachesTheClientAsAToolCallStillWaitingOnTheRequester(t *testing.T) {
	sessionUpdate := sessionUpdateForTurnEvent(bluecollar.TurnEvent{
		Kind:     bluecollar.TurnEventApproval,
		ToolName: "message_send",
		Message:  "Send this to the whole team?",
	}, nil, acp.ToolCallId("tool-call-1"))

	toolCall := sessionUpdate.ToolCall
	if toolCall == nil {
		t.Fatalf("expected a held call to be reported as a tool call, got %+v", sessionUpdate)
	}
	if toolCall.Status != acp.ToolCallStatusPending {
		t.Fatalf("expected the call to be reported as still pending, got %q", toolCall.Status)
	}
	if toolCall.Title != "message_send" {
		t.Fatalf("expected the client to learn which call is waiting, got %q", toolCall.Title)
	}
}
