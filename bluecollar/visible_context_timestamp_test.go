package bluecollar

import (
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
)

func TestBuildVisibleContextDescriptionRendersTimestamp(t *testing.T) {
	sentAt := time.Date(2026, 7, 10, 14, 3, 0, 0, agentcontract.ContextRenderLocation())
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
