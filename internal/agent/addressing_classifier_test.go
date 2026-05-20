package agent

import (
	"strings"
	"testing"
)

func TestAddressingClassificationSchemaOmitsReasonByDefault(t *testing.T) {
	schema := addressingClassificationSchema(false)

	if containsAny(schema.Document, []string{"reason", "addressingClass"}) {
		t.Fatalf("expected compact addressing schema without reason or legacy field, got %s", schema.Document)
	}
	for _, fragment := range []string{"target", "shouldReply", "bot", "human", "anyone", "none", "unclear"} {
		if !strings.Contains(schema.Document, fragment) {
			t.Fatalf("expected addressing schema to contain %q, got %s", fragment, schema.Document)
		}
	}
}

func TestAddressingClassificationSchemaIncludesReasonOnlyForDebug(t *testing.T) {
	schema := addressingClassificationSchema(true)

	for _, fragment := range []string{"target", "shouldReply", "reason"} {
		if !strings.Contains(schema.Document, fragment) {
			t.Fatalf("expected debug addressing schema to contain %q, got %s", fragment, schema.Document)
		}
	}
}

func TestAddressingClassificationPromptGuidesHumanDirectedAcknowledgements(t *testing.T) {
	prompt := addressingClassificationPrompt(AddressingClassificationRequest{Prompt: "네 확인해볼게요"})

	for _, fragment := range []string{"target=human", "shouldReply=false", "short acknowledgement", "human-directed message"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected addressing prompt to contain %q, got %s", fragment, prompt)
		}
	}
}
