package connectors

import (
	"testing"

	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

func messageSendEvents(conversationID string, isFailure bool) []taskstate.TaskEvent {
	resultBody := `{"toolName":"message_send","output":{"content":"보냈습니다"}}`
	if isFailure {
		resultBody = `{"toolName":"message_send","failure":{"code":"operation_failed"}}`
	}
	return []taskstate.TaskEvent{
		{Name: "tool.message_send.requested", Body: `{"toolName":"message_send","input":{"conversationID":"` + conversationID + `","body":"살아있어요"}}`},
		{Name: "tool.message_send.result", Body: resultBody},
	}
}

func TestAReplyTheAgentAlreadySentIsNotSentAgain(t *testing.T) {
	if !agentAlreadyRepliedToConversation(messageSendEvents("thread:abc", false), "thread:abc") {
		t.Fatal("expected a message the agent already delivered to this conversation to count, because the runtime would otherwise say the same thing twice")
	}
}

func TestAMessageToSomewhereElseStillLeavesTheRequesterAnAnswer(t *testing.T) {
	if agentAlreadyRepliedToConversation(messageSendEvents("channel:general", false), "thread:abc") {
		t.Fatal("expected a message sent to another conversation to leave the requester's own reply alone")
	}
}

func TestAFailedSendDoesNotCountAsAnAnswer(t *testing.T) {
	if agentAlreadyRepliedToConversation(messageSendEvents("thread:abc", true), "thread:abc") {
		t.Fatal("expected a send that failed to leave the requester with a reply rather than silence")
	}
}

func TestATaskThatSentNothingStillReplies(t *testing.T) {
	if agentAlreadyRepliedToConversation([]taskstate.TaskEvent{{Name: "task.completed", Body: "{}"}}, "thread:abc") {
		t.Fatal("expected an ordinary task to reply")
	}
}
