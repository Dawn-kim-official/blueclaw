package integration

import (
	"testing"

	"blueclaw/internal/connectors/slack"
	"blueclaw/tests/support"
)

func TestSlackDirectMessageFlow(t *testing.T) {
	eventParser := slack.EventParser{}
	event, errorValue := eventParser.ParseEvent(support.SlackMessagePayload())
	if errorValue != nil {
		t.Fatalf("expected slack event to parse: %v", errorValue)
	}
	if event.ConversationID != "C123" {
		t.Fatalf("expected conversation ID to match, got %s", event.ConversationID)
	}

	connector := slack.NewConnector()
	reply := connector.SendDirectReply(event.ConversationID, "hi")
	if reply == "" {
		t.Fatal("expected reply to be created")
	}
}
