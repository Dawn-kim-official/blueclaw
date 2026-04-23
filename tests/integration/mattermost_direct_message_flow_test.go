package integration

import (
	"testing"

	"blueclaw/internal/connectors/mattermost"
	"blueclaw/tests/support"
)

func TestMattermostDirectMessageFlow(t *testing.T) {
	eventParser := mattermost.EventParser{}
	event, errorValue := eventParser.ParseEvent(support.MattermostMessagePayload())
	if errorValue != nil {
		t.Fatalf("expected mattermost event to parse: %v", errorValue)
	}
	if event.ConversationID != "channel-1" {
		t.Fatalf("expected conversation ID to match, got %s", event.ConversationID)
	}

	connector := mattermost.NewConnector()
	reply := connector.SendDirectReply(event.ConversationID, "hi")
	if reply == "" {
		t.Fatal("expected reply to be created")
	}
}
