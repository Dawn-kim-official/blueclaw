package intake

import (
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
)

func TestAddressingPromptCarriesMessageAndContextTime(t *testing.T) {
	sentAt := time.Date(2026, 7, 10, 14, 3, 0, 0, agentcontract.ContextRenderLocation())
	prompt := addressingClassificationPrompt(agentcontract.AddressingClassificationRequest{
		Prompt:        "and this too",
		MessageSentAt: sentAt.Add(30 * time.Second),
		VisibleContext: agentcontract.VisibleContext{
			Messages: []agentcontract.VisibleContextMessage{
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
