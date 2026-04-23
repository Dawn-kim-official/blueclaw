package llm

import (
	"context"
	"errors"
	"testing"
)

type staticLanguageModelProvider struct {
	response StructuredResponse
	error    error
}

func (staticLanguageModelProvider staticLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	if staticLanguageModelProvider.error != nil {
		return "", staticLanguageModelProvider.error
	}

	return staticLanguageModelProvider.response.Content, nil
}

func (staticLanguageModelProvider staticLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	if staticLanguageModelProvider.error != nil {
		return StructuredResponse{}, staticLanguageModelProvider.error
	}

	return staticLanguageModelProvider.response, nil
}

func TestFallbackLanguageModelProviderUsesFallbackAfterPrimaryFailure(t *testing.T) {
	fallbackLanguageModelProvider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{
			error: errors.New("primary failed"),
		},
		FallbackProvider: staticLanguageModelProvider{
			response: StructuredResponse{
				ProviderName: "litert-lm",
				ModelName:    "/models/gemma-3n.litertlm",
				Content:      `{"answer":"fallback"}`,
			},
		},
	}

	structuredResponse, errorValue := fallbackLanguageModelProvider.GenerateStructuredResponse(
		context.Background(),
		StructuredResponseRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: "hello",
				},
			},
			StructuredOutputSchema: StructuredOutputSchema{
				Name:               "response",
				Document:           `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected fallback provider to succeed: %v", errorValue)
	}
	if !structuredResponse.UsedFallback {
		t.Fatal("expected fallback provider to mark used fallback")
	}
	if structuredResponse.ProviderName != "litert-lm" {
		t.Fatalf("expected fallback provider name, got %q", structuredResponse.ProviderName)
	}
}
