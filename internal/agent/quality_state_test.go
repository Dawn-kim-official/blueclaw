package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQualityStateUsesSlideRenderReviewForVisualChecks(t *testing.T) {
	artifactDirectoryPath := t.TempDir()
	writeSlideRenderReviewTestFile(t, artifactDirectoryPath, true)
	validityState := ValidityState{Passed: true, CheckedArtifacts: []ArtifactValidity{{
		Filename:  "deck.html",
		Suffix:    ".html",
		SizeBytes: 128,
		Passed:    true,
		path:      filepath.Join(artifactDirectoryPath, "deck.html"),
	}}}

	state := buildQualityState(AgentTurnRequest{
		QualityRecommendedChecks: []string{"slide_render_images", "render_nonblank", "max_text_overflow"},
	}, nil, validityState)

	if !state.Passed {
		t.Fatalf("expected visual quality checks to pass, got %+v", state)
	}
	if state.RenderReview == nil || state.RenderReview.SlideCount != 2 {
		t.Fatalf("expected render review summary, got %+v", state.RenderReview)
	}
	if len(state.RenderReview.ContactSheets) != 1 || state.RenderReview.ContactSheets[0] != "contact-sheet-01.png" {
		t.Fatalf("expected contact sheet summary, got %+v", state.RenderReview)
	}
}

func TestQualityStateWarnsForSlideOverflowWithoutBlockingPolicy(t *testing.T) {
	artifactDirectoryPath := t.TempDir()
	writeSlideRenderReviewTestFile(t, artifactDirectoryPath, false)
	validityState := ValidityState{Passed: true, CheckedArtifacts: []ArtifactValidity{{
		Filename:  "deck.html",
		Suffix:    ".html",
		SizeBytes: 128,
		Passed:    true,
		path:      filepath.Join(artifactDirectoryPath, "deck.html"),
	}}}

	state := buildQualityState(AgentTurnRequest{
		QualityRecommendedChecks: []string{"max_text_overflow"},
	}, nil, validityState)

	if state.Passed {
		t.Fatalf("expected advisory warning, got %+v", state)
	}
	if !strings.Contains(strings.Join(state.Warnings, "\n"), "slides: 2") {
		t.Fatalf("expected slide 2 warning, got %+v", state.Warnings)
	}
	if state.RenderReview == nil || len(state.RenderReview.WarningSlideNumbers) != 1 || state.RenderReview.WarningSlideNumbers[0] != 2 {
		t.Fatalf("expected warning slide summary, got %+v", state.RenderReview)
	}
}

func writeSlideRenderReviewTestFile(t *testing.T, artifactDirectoryPath string, passed bool) {
	t.Helper()
	reviewDirectoryPath := filepath.Join(artifactDirectoryPath, "review")
	if errorValue := os.MkdirAll(reviewDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	slideTwoPassed := "true"
	slideTwoSafeMargin := "true"
	slideTwoEdgeOverflow := "true"
	if !passed {
		slideTwoPassed = "false"
		slideTwoSafeMargin = "false"
		slideTwoEdgeOverflow = "false"
	}
	content := `{
  "passed": ` + slideTwoPassed + `,
  "slideCount": 2,
  "design": {
    "layout.margin": "68px",
    "typography.display": "Paperlogy, Freesentation",
    "typography.body": "Freesentation, Pretendard"
  },
  "contactSheets": [
    {"filename": "contact-sheet-01.png", "slideNumbers": [1, 2]}
  ],
  "slides": [
    {
      "index": 1,
      "filename": "deck.001.png",
      "passed": true,
      "checks": {"nonblank": true, "safeMargin": true, "edgeOverflow": true},
      "warnings": []
    },
    {
      "index": 2,
      "filename": "deck.002.png",
      "passed": ` + slideTwoPassed + `,
      "checks": {"nonblank": true, "safeMargin": ` + slideTwoSafeMargin + `, "edgeOverflow": ` + slideTwoEdgeOverflow + `},
      "warnings": []
    }
  ]
}
`
	if errorValue := os.WriteFile(filepath.Join(reviewDirectoryPath, "slide-review.json"), []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}
