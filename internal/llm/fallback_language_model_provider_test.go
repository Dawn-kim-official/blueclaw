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
	response                StructuredResponse
	error                   error
	responseCalls           *int
	structuredResponseCalls *int
}

func (staticLanguageModelProvider staticLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	if staticLanguageModelProvider.responseCalls != nil {
		(*staticLanguageModelProvider.responseCalls)++
	}
	if staticLanguageModelProvider.error != nil {
		return "", staticLanguageModelProvider.error
	}

	return staticLanguageModelProvider.response.Content, nil
}

func (staticLanguageModelProvider staticLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	if staticLanguageModelProvider.structuredResponseCalls != nil {
		(*staticLanguageModelProvider.structuredResponseCalls)++
	}
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

func TestFallbackLanguageModelProviderDoesNotFallbackAfterCancellation(t *testing.T) {
	responseContext, cancel := context.WithCancel(context.Background())
	cancel()
	var responseCalls int
	var structuredResponseCalls int
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: context.Canceled},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			responseCalls:           &responseCalls,
			structuredResponseCalls: &structuredResponseCalls,
		},
	}

	response, errorValue := provider.GenerateResponse(responseContext, "hello")
	if response != "" || !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation without fallback response, got %q and %v", response, errorValue)
	}
	structuredResponse, errorValue := provider.GenerateStructuredResponse(responseContext, StructuredResponseRequest{})
	if structuredResponse.Content != "" || !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation without fallback structured response, got %#v and %v", structuredResponse, errorValue)
	}
	if responseCalls != 0 || structuredResponseCalls != 0 {
		t.Fatalf("expected no fallback calls after cancellation, got response=%d structured=%d", responseCalls, structuredResponseCalls)
	}
}

func TestFallbackLanguageModelProviderDoesNotFallbackAfterDeadline(t *testing.T) {
	responseContext, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	var responseCalls int
	var structuredResponseCalls int
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: staticLanguageModelProvider{error: context.DeadlineExceeded},
		FallbackProvider: staticLanguageModelProvider{
			response:                StructuredResponse{Content: "fallback"},
			responseCalls:           &responseCalls,
			structuredResponseCalls: &structuredResponseCalls,
		},
	}

	response, errorValue := provider.GenerateResponse(responseContext, "hello")
	if response != "" || !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("expected deadline without fallback response, got %q and %v", response, errorValue)
	}
	structuredResponse, errorValue := provider.GenerateStructuredResponse(responseContext, StructuredResponseRequest{})
	if structuredResponse.Content != "" || !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("expected deadline without fallback structured response, got %#v and %v", structuredResponse, errorValue)
	}
	if responseCalls != 0 || structuredResponseCalls != 0 {
		t.Fatalf("expected no fallback calls after deadline, got response=%d structured=%d", responseCalls, structuredResponseCalls)
	}
}
