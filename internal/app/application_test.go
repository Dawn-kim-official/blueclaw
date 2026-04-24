package app

import (
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/llm"
)

func TestResolveLanguageModelProviderDefaultsToOpenRouterWhenConfigured(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.OpenRouter.ModelName = "openai/gpt-4.1-mini"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected openrouter provider to be inferred from configuration")
	}
	if _, isOpenRouterProvider := languageModelProvider.(llm.OpenRouterClient); !isOpenRouterProvider {
		t.Fatalf("expected openrouter provider, got %T", languageModelProvider)
	}
}

func TestResolveLanguageModelProviderAddsLiteRTLMFallbackWhenConfigured(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.OpenRouter.ModelName = "openai/gpt-4.1-mini"
	runtimeConfiguration.LanguageModel.LiteRTLM.WrapperPath = "/usr/local/bin/blueclaw-litert-wrapper"

	languageModelProvider := resolveLanguageModelProvider(runtimeConfiguration)
	if languageModelProvider == nil {
		t.Fatal("expected language model provider to be inferred from configuration")
	}
	if _, isFallbackProvider := languageModelProvider.(llm.FallbackLanguageModelProvider); !isFallbackProvider {
		t.Fatalf("expected fallback language model provider, got %T", languageModelProvider)
	}
}
