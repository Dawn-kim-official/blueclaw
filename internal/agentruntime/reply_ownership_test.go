package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSendingToTheConversationTheTaskCameFromIsRefused(t *testing.T) {
	request := ToolCatalogRequest{ConversationID: "thread:abc"}

	if !replyBelongsToTheRuntime("message_send", request, json.RawMessage(`{"conversationID":"thread:abc","body":"살아있어요"}`)) {
		t.Fatal("expected the runtime to own the reply to the conversation the task came from, because two senders is how the same answer arrives twice")
	}
	failure := originConversationReplyFailure()
	if !strings.Contains(failure.Failure.UserSafeSummary, "finishing message") {
		t.Fatalf("expected the refusal to say where the reply belongs instead, got %q", failure.Failure.UserSafeSummary)
	}
}

func TestSendingSomewhereElseIsStillTheAgentsToDo(t *testing.T) {
	request := ToolCatalogRequest{ConversationID: "thread:abc"}

	if replyBelongsToTheRuntime("message_send", request, json.RawMessage(`{"channelID":"channel:general","body":"공지"}`)) {
		t.Fatal("expected a message to another conversation to stay the agent's own to send")
	}
}

func TestATaskWithNoConversationLeavesTheToolAlone(t *testing.T) {
	if replyBelongsToTheRuntime("message_send", ToolCatalogRequest{}, json.RawMessage(`{"conversationID":"thread:abc"}`)) {
		t.Fatal("expected a task with no originating conversation, such as a scheduled one, to send freely")
	}
}

func TestOtherToolsAreUntouched(t *testing.T) {
	request := ToolCatalogRequest{ConversationID: "thread:abc"}

	if replyBelongsToTheRuntime("message_update", request, json.RawMessage(`{"conversationID":"thread:abc"}`)) {
		t.Fatal("expected only the sending tool to be governed by who owns the reply")
	}
}
