package bluecollar

import (
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
)

func TestPromptAssemblerIncludesArtifactManifest(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(agentcontract.AgentTurnRequest{
		Prompt: "change the title",
		ArtifactManifest: []agentcontract.ArtifactManifestEntry{{
			RelativePath:  "private/people/person-1/artifacts/deck/deck.pptx",
			ProducingTool: "file_deliver",
			ModifiedAt:    time.Date(2026, time.June, 12, 3, 0, 0, 0, time.UTC),
		}},
	}, nil, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Previously delivered artifacts in this conversation") || !strings.Contains(body, "private/people/person-1/artifacts/deck/deck.pptx") {
		t.Fatalf("expected artifact manifest context, got %s", body)
	}
}
