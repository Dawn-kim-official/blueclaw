package agent

import (
	"strings"
	"testing"
	"time"
)

func TestBuildVisibleContextDescriptionRendersTimestamp(t *testing.T) {
	sentAt := time.Date(2026, 7, 10, 14, 3, 0, 0, defaultTurnLocation())
	description := buildVisibleContextDescription(VisibleContext{
		Messages: []VisibleContextMessage{
			{Speaker: "Wendy", SpeakerCallingName: "Wendy", Text: "tidy up this file", SentAt: sentAt},
		},
	})
	if !strings.Contains(description, "[07-10 14:03]") {
		t.Fatalf("expected rendered timestamp in context, got %q", description)
	}
}

func TestBuildVisibleContextDescriptionOmitsZeroTimestamp(t *testing.T) {
	description := buildVisibleContextDescription(VisibleContext{
		Messages: []VisibleContextMessage{
			{Speaker: "Wendy", SpeakerCallingName: "Wendy", Text: "hello"},
		},
	})
	if strings.Contains(description, "[") {
		t.Fatalf("zero timestamp must render without a bracketed time, got %q", description)
	}
}

func TestAddressingPromptCarriesMessageAndContextTime(t *testing.T) {
	sentAt := time.Date(2026, 7, 10, 14, 3, 0, 0, defaultTurnLocation())
	prompt := addressingClassificationPrompt(AddressingClassificationRequest{
		Prompt:        "and this too",
		MessageSentAt: sentAt.Add(30 * time.Second),
		VisibleContext: VisibleContext{
			Messages: []VisibleContextMessage{
				{Speaker: "Wendy", SpeakerCallingName: "Wendy", Text: "tidy it up", SentAt: sentAt},
			},
		},
	})
	if !strings.Contains(prompt, "messageTime: 07-10 14:03") {
		t.Fatalf("expected messageTime line, got %q", prompt)
	}
	if !strings.Contains(prompt, "context: [07-10 14:03]") {
		t.Fatalf("expected timestamped context line, got %q", prompt)
	}
}
