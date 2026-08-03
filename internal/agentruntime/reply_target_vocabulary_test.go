package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
)

const messageSendInputSchema = `{"type":"object","properties":{"targetType":{"type":"string","enum":["directMessage","currentThread","currentChannel","channel"]},"body":{"type":"string"}},"required":["targetType","body"]}`

func targetValues(t *testing.T, inputSchema json.RawMessage) []string {
	t.Helper()
	decoded := map[string]any{}
	if errorValue := json.Unmarshal(inputSchema, &decoded); errorValue != nil {
		t.Fatalf("expected a readable schema: %v", errorValue)
	}
	properties, _ := decoded["properties"].(map[string]any)
	targetType, _ := properties["targetType"].(map[string]any)
	offered, _ := targetType["enum"].([]any)
	values := []string{}
	for _, offeredValue := range offered {
		if valueText, isText := offeredValue.(string); isText {
			values = append(values, valueText)
		}
	}
	return values
}

func TestTheModelIsNeverOfferedItsOwnConversationAsADestination(t *testing.T) {
	narrowed := narrowMessageSendTargets(json.RawMessage(messageSendInputSchema), true)

	values := targetValues(t, narrowed)
	for _, value := range values {
		if value == "currentThread" || value == "currentChannel" {
			t.Fatalf("expected the conversation the task came from to be unsayable, still offered %q", value)
		}
	}
	if len(values) != 2 {
		t.Fatalf("expected the other destinations to survive, got %v", values)
	}
}

func TestOtherDestinationsSurviveTheNarrowing(t *testing.T) {
	narrowed := narrowMessageSendTargets(json.RawMessage(messageSendInputSchema), true)

	values := strings.Join(targetValues(t, narrowed), ",")
	if !strings.Contains(values, "directMessage") || !strings.Contains(values, "channel") {
		t.Fatalf("expected messages to other people and channels to stay possible, got %v", values)
	}
}

func TestATaskWithNoConversationKeepsEveryDestination(t *testing.T) {
	unchanged := narrowMessageSendTargets(json.RawMessage(messageSendInputSchema), false)

	if len(targetValues(t, unchanged)) != 4 {
		t.Fatal("expected a task with no originating conversation, such as a scheduled one, to keep every destination")
	}
}

func TestTheNarrowingSaysWhereTheReplyBelongs(t *testing.T) {
	narrowed := narrowMessageSendTargets(json.RawMessage(messageSendInputSchema), true)

	if !strings.Contains(string(narrowed), "finishing message") {
		t.Fatal("expected the schema itself to say where the reply belongs, so the model is not left guessing why a destination vanished")
	}
}
