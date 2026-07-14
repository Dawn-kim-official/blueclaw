package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShadowProviderReturnsPrimaryResponseAfterComparingSDKD(t *testing.T) {
	primaryProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM", Content: `{"result":"primary"}`}}
	shadowCalled := make(chan struct{}, 1)
	shadowProvider := &sdkdTestLanguageModel{
		structuredResponse: StructuredResponse{ProviderName: "openrouter", Content: `{"result":"shadow"}`},
		structuredCalled:   shadowCalled,
	}
	provider := ShadowLanguageModelProvider{PrimaryProvider: primaryProvider, ShadowProvider: shadowProvider}

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil {
		t.Fatalf("expected primary response: %v", errorValue)
	}
	if response.ProviderName != "capabilityLLM" || response.Content != `{"result":"primary"}` {
		t.Fatalf("expected authoritative primary response, got %+v", response)
	}
	select {
	case <-shadowCalled:
	case <-time.After(time.Second):
		t.Fatal("expected shadow provider call")
	}
	if primaryProvider.structuredCallCount != 1 {
		t.Fatalf("expected one primary call, got %d", primaryProvider.structuredCallCount)
	}
}

func TestStructuredContentMatchesIgnoresJSONPropertyOrder(t *testing.T) {
	if !structuredContentMatches(`{"first":1,"second":2}`, `{"second":2,"first":1}`) {
		t.Fatal("expected normalized JSON documents to match")
	}
}

func TestShadowProviderIgnoresShadowFailure(t *testing.T) {
	primaryProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{Content: `{"ok":true}`}}
	shadowProvider := &sdkdTestLanguageModel{structuredError: errors.New("shadow unavailable")}
	provider := ShadowLanguageModelProvider{PrimaryProvider: primaryProvider, ShadowProvider: shadowProvider}

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil || response.Content != `{"ok":true}` {
		t.Fatalf("expected shadow failure not to affect primary response, got %+v, %v", response, errorValue)
	}
}

func TestShadowProviderDoesNotShadowTextGeneration(t *testing.T) {
	primaryProvider := &sdkdTestLanguageModel{textResponse: "primary"}
	shadowProvider := &sdkdTestLanguageModel{textResponse: "shadow"}
	provider := ShadowLanguageModelProvider{PrimaryProvider: primaryProvider, ShadowProvider: shadowProvider}

	response, errorValue := provider.GenerateResponse(context.Background(), "hello")
	if errorValue != nil || response != "primary" {
		t.Fatalf("expected primary text response, got %q, %v", response, errorValue)
	}
	if primaryProvider.textCallCount != 1 || shadowProvider.textCallCount != 0 {
		t.Fatalf("expected only the primary text provider to run, got %d and %d", primaryProvider.textCallCount, shadowProvider.textCallCount)
	}
}

func TestShadowProviderObservesOnlyConfiguredSchemas(t *testing.T) {
	primaryProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{Content: `{"ok":true}`}}
	shadowProvider := &sdkdTestLanguageModel{}
	provider := ShadowLanguageModelProvider{
		PrimaryProvider:       primaryProvider,
		ShadowProvider:        shadowProvider,
		StructuredSchemaNames: []string{"blueclaw_agent_turn_action"},
	}

	_, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_turn_router"},
	})
	if errorValue != nil {
		t.Fatalf("expected primary response: %v", errorValue)
	}
	if shadowProvider.structuredCallCount != 0 {
		t.Fatalf("expected non-authoritative schema not to be shadowed, got %d calls", shadowProvider.structuredCallCount)
	}
}
