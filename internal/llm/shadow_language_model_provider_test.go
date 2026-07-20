package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShadowProviderReturnsPrimaryResponseAfterComparingLLMD(t *testing.T) {
	primaryProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM", Content: `{"result":"primary"}`}}
	shadowCalled := make(chan struct{}, 1)
	shadowProvider := &llmdTestLanguageModel{
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
	primaryProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{Content: `{"ok":true}`}}
	shadowProvider := &llmdTestLanguageModel{structuredError: errors.New("shadow unavailable")}
	provider := ShadowLanguageModelProvider{PrimaryProvider: primaryProvider, ShadowProvider: shadowProvider}

	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil || response.Content != `{"ok":true}` {
		t.Fatalf("expected shadow failure not to affect primary response, got %+v, %v", response, errorValue)
	}
}

func TestShadowProviderDoesNotShadowTextGeneration(t *testing.T) {
	primaryProvider := &llmdTestLanguageModel{textResponse: "primary"}
	shadowProvider := &llmdTestLanguageModel{textResponse: "shadow"}
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
	primaryProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{Content: `{"ok":true}`}}
	shadowProvider := &llmdTestLanguageModel{}
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

type shadowRecoveryChatTestProvider struct {
	recoveryResponse ChatCompletionResponse
}

func (provider shadowRecoveryChatTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider shadowRecoveryChatTestProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, nil
}

func (provider shadowRecoveryChatTestProvider) GenerateRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return provider.recoveryResponse, nil
}

func TestShadowRecoveryChatCapabilitiesMatchPrimaryProvider(t *testing.T) {
	recoveryResponse := ChatCompletionResponse{
		FinishReason: "stop",
		Message:      ChatCompletionMessage{Role: "assistant", Content: "recovered"},
	}
	withRecovery := withShadowRecoveryChatCapabilities(ShadowLanguageModelProvider{
		PrimaryProvider: shadowRecoveryChatTestProvider{recoveryResponse: recoveryResponse},
	})
	withoutRecovery := withShadowRecoveryChatCapabilities(ShadowLanguageModelProvider{
		PrimaryProvider: resolverLanguageModelProviderWithoutChat{},
	})

	recoveryProvider, hasRecovery := withRecovery.(RecoveryChatCompleter)
	if !hasRecovery {
		t.Fatal("expected recovery chat capability to be preserved")
	}
	response, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "recovered" {
		t.Fatalf("expected delegated recovery chat, got %+v, %v", response, errorValue)
	}
	if _, hasLocalRecovery := withRecovery.(LocalRecoveryChatCompleter); hasLocalRecovery {
		t.Fatal("expected local recovery chat capability to remain absent")
	}
	if _, hasRecovery := withoutRecovery.(RecoveryChatCompleter); hasRecovery {
		t.Fatal("expected recovery chat capability to remain absent")
	}
	if _, hasLocalRecovery := withoutRecovery.(LocalRecoveryChatCompleter); hasLocalRecovery {
		t.Fatal("expected local recovery chat capability to remain absent")
	}
}
