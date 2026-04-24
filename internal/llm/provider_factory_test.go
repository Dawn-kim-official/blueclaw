package llm

import (
	"net/http"
	"testing"

	"blueclaw/internal/config"
)

func TestConfiguredOpenRouterProviderUsesDefaultHTTPClient(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "openRouter"
	runtimeConfiguration.LanguageModel.OpenRouter.ModelName = "openai/gpt-4.1-mini"

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration, "test-key")
	if errorValue != nil {
		t.Fatalf("expected provider to be created: %v", errorValue)
	}

	openRouterClient, isOpenRouterProvider := languageModelProvider.(OpenRouterClient)
	if !isOpenRouterProvider {
		t.Fatalf("expected openrouter provider, got %T", languageModelProvider)
	}
	if openRouterClient.HTTPClient != http.DefaultClient {
		t.Fatal("expected openrouter provider to use the default http client")
	}
}
