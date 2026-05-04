package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidityStateAcceptsReadableNonEmptyArtifact(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "deck.pptx")
	writeAgentTestFile(t, artifactPath, "not a real pptx but still a delivered file")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".pptx",
		Filename:     "deck.pptx",
		RelativePath: "deck.pptx",
		path:         artifactPath,
	}})

	if !validityState.Passed {
		t.Fatalf("expected readable non-empty artifact to pass basic validity, got %+v", validityState)
	}
}

func TestValidityStateRejectsEmptyArtifact(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "deck.html")
	writeAgentTestFile(t, artifactPath, "")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "deck.html",
		RelativePath: "deck.html",
		path:         artifactPath,
	}})

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected empty artifact validity failure, got %+v", validityState)
	}
	if validityState.InvalidArtifacts[0].Reason != "artifact file is empty" {
		t.Fatalf("expected empty artifact reason, got %+v", validityState.InvalidArtifacts[0])
	}
}

func TestValidityStateAcceptsDeckWithoutIntentManifest(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "hermes-analysis")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	artifactPath := filepath.Join(artifactDirectoryPath, "hermes-analysis.html")
	writeAgentTestFile(t, artifactPath, "<html><body>Hermes Agent 장단점 분석</body></html>")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "hermes-analysis.html",
		RelativePath: ".blueclaw/tmp/hermes-analysis/hermes-analysis.html",
		path:         artifactPath,
	}})

	if !validityState.Passed {
		t.Fatalf("expected deck artifact without intent manifest to pass basic validity, got %+v", validityState)
	}
}
