package llm

import (
	"context"
	"testing"
)

func newVisionFallbackProviderForTest() VisionFallbackProvider {
	return VisionFallbackProvider{
		TextOnlyModel: staticLanguageModelProvider{response: StructuredResponse{ModelName: "text-only"}},
		VisionModel:   staticLanguageModelProvider{response: StructuredResponse{ModelName: "vision"}},
	}
}

func TestVisionFallbackProviderRoutesTextOnlyRequestToTextOnlyModel(t *testing.T) {
	provider := newVisionFallbackProviderForTest()

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "refactor this function"}},
	})

	if errorValue != nil {
		t.Fatalf("unexpected error: %v", errorValue)
	}
	if response.ModelName != "text-only" {
		t.Fatalf("expected text-only model, got %q", response.ModelName)
	}
}

func TestVisionFallbackProviderRoutesImageRequestToVisionModel(t *testing.T) {
	provider := newVisionFallbackProviderForTest()

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{
			{Role: "user", Content: "look at this"},
			{Role: "user", Parts: []MessagePart{{Type: "image", MimeType: "image/png", DataBase64: "abc"}}},
		},
	})

	if errorValue != nil {
		t.Fatalf("unexpected error: %v", errorValue)
	}
	if response.ModelName != "vision" {
		t.Fatalf("expected vision model, got %q", response.ModelName)
	}
}
