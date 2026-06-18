package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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
				ModelName:    "/models/gemma-4-E4B-it.litertlm",
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

func TestFallbackLanguageModelProviderLogsAndDescendsThroughTiers(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelWarn}))

	chain := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: errors.New("high tier unavailable")},
		FallbackProvider: FallbackLanguageModelProvider{
			PrimaryProvider:  staticLanguageModelProvider{error: errors.New("medium tier unavailable")},
			FallbackProvider: staticLanguageModelProvider{response: StructuredResponse{ModelName: "low-model"}},
			PrimaryLabel:     "medium",
			FallbackLabel:    "low",
			Logger:           logger,
		},
		PrimaryLabel:  "high",
		FallbackLabel: "medium",
		Logger:        logger,
	}

	structuredResponse, errorValue := chain.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil {
		t.Fatalf("expected descent to reach a working tier: %v", errorValue)
	}
	if structuredResponse.ModelName != "low-model" {
		t.Fatalf("expected lowest tier to answer, got %q", structuredResponse.ModelName)
	}
	if !structuredResponse.UsedFallback {
		t.Fatal("expected response to be marked as used fallback")
	}

	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "failedTier=high") || !strings.Contains(logOutput, "failedTier=medium") {
		t.Fatalf("expected a log line per failed tier, got: %s", logOutput)
	}
}
