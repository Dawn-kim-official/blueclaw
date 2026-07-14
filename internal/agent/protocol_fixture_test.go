package agent

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolAgentActionFixtureMatchesTurnActionDocument(t *testing.T) {
	var document turnActionDocument
	if errorValue := json.Unmarshal(protocolAgentFixture(t, "agent-action"), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if document.Action != "continue" || document.ToolName != "file.read" {
		t.Fatalf("unexpected action fixture: %#v", document)
	}
	if len(document.ToolInput) == 0 || document.ExecutionStateUpdate.Goal == "" {
		t.Fatalf("action fixture lost required content: %#v", document)
	}
}

func TestProtocolAgentMessageFixtureMatchesAgentMessage(t *testing.T) {
	var message AgentMessage
	if errorValue := json.Unmarshal(protocolAgentFixture(t, "agent-message"), &message); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(message.Parts) != 2 || message.Parts[1].File == nil {
		t.Fatalf("unexpected agent message fixture: %#v", message)
	}
	if message.Parts[1].Source.MessageID != "message-1" {
		t.Fatalf("agent message fixture lost source identity: %#v", message.Parts[1].Source)
	}
}

func protocolAgentFixture(t *testing.T, fixtureName string) json.RawMessage {
	t.Helper()
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var fixtures map[string][]json.RawMessage
	if errorValue := json.Unmarshal(documentBytes, &fixtures); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fixtures[fixtureName]) != 1 {
		t.Fatalf("expected one %s fixture", fixtureName)
	}
	return fixtures[fixtureName][0]
}
