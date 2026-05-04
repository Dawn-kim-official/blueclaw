package agent

import (
	"path/filepath"
	"testing"
)

func TestValidityStateAcceptsPDFWithoutDetectablePageMarkers(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "deck.pdf")
	writeAgentTestFile(t, artifactPath, "%PDF-1.7\ncompressed body\n%%EOF")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".pdf",
		Filename:     "deck.pdf",
		RelativePath: "deck.pdf",
		path:         artifactPath,
	}})

	if !validityState.Passed {
		t.Fatalf("expected PDF header validity to pass, got %+v", validityState)
	}
}

func TestValidityStateRejectsBrokenPPTX(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "deck.pptx")
	writeAgentTestFile(t, artifactPath, "not a zip package")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".pptx",
		Filename:     "deck.pptx",
		RelativePath: "deck.pptx",
		path:         artifactPath,
	}})

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected broken PPTX validity failure, got %+v", validityState)
	}
}
