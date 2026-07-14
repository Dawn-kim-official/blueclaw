package agent

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolAgentActionFixtureMatchesTurnActionDocument(t *testing.T) {
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid/agent-action.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var document turnActionDocument
	if errorValue := json.Unmarshal(documentBytes, &document); errorValue != nil {
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
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid/agent-message.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var message AgentMessage
	if errorValue := json.Unmarshal(documentBytes, &message); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(message.Parts) != 2 || message.Parts[1].File == nil {
		t.Fatalf("unexpected agent message fixture: %#v", message)
	}
	if message.Parts[1].Source.MessageID != "message-1" {
		t.Fatalf("agent message fixture lost source identity: %#v", message.Parts[1].Source)
	}
}
