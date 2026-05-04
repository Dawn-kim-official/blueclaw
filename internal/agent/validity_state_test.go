package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestValidityStateRejectsDeckArtifactMissingIntentManifest(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "hermes-analysis")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "hermes-analysis.html"), "<html><body>Hermes Agent 장단점 분석</body></html>")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "hermes-analysis.html",
		RelativePath: ".blueclaw/tmp/hermes-analysis/hermes-analysis.html",
		path:         filepath.Join(artifactDirectoryPath, "hermes-analysis.html"),
	}})

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected missing intent manifest failure, got %+v", validityState)
	}
	if validityState.InvalidArtifacts[0].Reason != "deck intent manifest is missing" {
		t.Fatalf("expected missing manifest reason, got %+v", validityState.InvalidArtifacts[0])
	}
}

func TestValidityStateRejectsSampleTokenForGenericDeck(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "hermes-analysis")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "hermes-analysis-intent.json"), `{
  "output_slug": "hermes-analysis",
  "mode": "generic",
  "topic": "Hermes Agent",
  "slide_intent": "장단점 분석",
  "requested_slide_count": 1,
  "requested_formats": ["html"],
  "slide_count": 1
}`)
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "hermes-analysis.html"), "<html><body>Hermes Agent 장단점 분석 InternKim capability deck</body></html>")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "hermes-analysis.html",
		RelativePath: ".blueclaw/tmp/hermes-analysis/hermes-analysis.html",
		path:         filepath.Join(artifactDirectoryPath, "hermes-analysis.html"),
	}})

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected sample token failure, got %+v", validityState)
	}
	if validityState.InvalidArtifacts[0].Reason != "non-capabilities artifact contains built-in capability sample text" {
		t.Fatalf("expected sample token reason, got %+v", validityState.InvalidArtifacts[0])
	}
}

func TestValidityStateRejectsStaleAttachedArtifact(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "hermes-analysis")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "hermes-analysis-intent.json"), `{
  "output_slug": "hermes-analysis",
  "mode": "generic",
  "topic": "Hermes Agent",
  "slide_intent": "장단점 분석",
  "requested_slide_count": 1,
  "requested_formats": ["html"],
  "slide_count": 1
}`)
	artifactPath := filepath.Join(artifactDirectoryPath, "hermes-analysis.html")
	writeAgentTestFile(t, artifactPath, "<html><body>Hermes Agent 장단점 분석</body></html>")
	taskStartedAt := time.Now()
	oldModifiedAt := taskStartedAt.Add(-time.Hour)
	if errorValue := os.Chtimes(artifactPath, oldModifiedAt, oldModifiedAt); errorValue != nil {
		t.Fatal(errorValue)
	}

	validityState := buildAttachmentValidityState(workspaceRootPath, []FileAttachment{{
		DevicePath: "/workspace/.blueclaw/tmp/hermes-analysis/hermes-analysis.html",
		Filename:   "hermes-analysis.html",
	}}, taskStartedAt)

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected stale artifact failure, got %+v", validityState)
	}
	if validityState.InvalidArtifacts[0].Reason != "artifact was not created during this task run" {
		t.Fatalf("expected stale artifact reason, got %+v", validityState.InvalidArtifacts[0])
	}
}

func TestValidityStateAcceptsDeckArtifactMatchingIntentManifest(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "hermes-analysis")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "hermes-analysis-intent.json"), `{
  "output_slug": "hermes-analysis",
  "mode": "generic",
  "topic": "Hermes Agent",
  "slide_intent": "장단점 분석",
  "requested_slide_count": 1,
  "requested_formats": ["html"],
  "slide_count": 1
}`)
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "hermes-analysis.html"), "<html><body>Hermes Agent 장단점 분석</body></html>")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "hermes-analysis.html",
		RelativePath: ".blueclaw/tmp/hermes-analysis/hermes-analysis.html",
		path:         filepath.Join(artifactDirectoryPath, "hermes-analysis.html"),
	}})

	if !validityState.Passed {
		t.Fatalf("expected matching intent artifact to pass, got %+v", validityState)
	}
}
