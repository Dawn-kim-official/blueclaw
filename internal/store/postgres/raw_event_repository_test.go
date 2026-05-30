package postgres

import (
	"strings"
	"testing"

	"blueclaw/internal/connectors"
)

func TestConnectorReplyOutboxIDSeparatesCheckpointAndFinalReplies(t *testing.T) {
	rawEventID := "mattermost:thread:channel:root"
	checkpointID := connectorReplyOutboxID(rawEventID, connectors.OutboundReply{
		TaskRunID: "task-1",
		ReplyKind: "checkpoint",
		Message:   "작업 중입니다.",
	})
	finalID := connectorReplyOutboxID(rawEventID, connectors.OutboundReply{
		TaskRunID: "task-1",
		ReplyKind: "user_notice",
		Message:   "작업이 실패했습니다.",
	})

	if checkpointID == finalID {
		t.Fatalf("expected distinct outbox ids, got %q", checkpointID)
	}
	if !strings.HasPrefix(checkpointID, rawEventID+":reply:checkpoint:") {
		t.Fatalf("expected checkpoint prefix, got %q", checkpointID)
	}
	if !strings.HasPrefix(finalID, rawEventID+":reply:user_notice:") {
		t.Fatalf("expected final notice prefix, got %q", finalID)
	}
}
