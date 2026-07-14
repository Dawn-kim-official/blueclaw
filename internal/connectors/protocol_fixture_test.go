package connectors

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolPlatformEventFixtureMatchesPlatformInboundEvent(t *testing.T) {
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid/platform-inbound-event.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var event PlatformInboundEvent
	if errorValue := json.Unmarshal(documentBytes, &event); errorValue != nil {
		t.Fatal(errorValue)
	}
	if event.ConversationID != "channel-1" || event.MessageID != "message-1" {
		t.Fatalf("unexpected platform event fixture: %#v", event)
	}
	if len(event.Context.Messages) != 1 || len(event.InputParts) != 1 {
		t.Fatalf("platform event fixture lost context or attachments: %#v", event)
	}
	if event.Context.Messages[0].SentAt.IsZero() {
		t.Fatalf("platform event fixture lost sent timestamp: %#v", event.Context.Messages[0])
	}
}
