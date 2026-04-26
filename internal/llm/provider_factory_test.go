package llm

import (
	"testing"

	"blueclaw/internal/config"
)

func TestConfiguredProviderUsesCapabilityLLMByDefault(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Capabilities.Endpoint = "http://127.0.0.1:7781"
	runtimeConfiguration.LanguageModel.Capability.Model = "gemma-4-E4B-it"
	runtimeConfiguration.LanguageModel.Capability.ExecutionMode = "auto"

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected provider to be created: %v", errorValue)
	}

	capabilityLLMClient, isCapabilityLLMProvider := languageModelProvider.(CapabilityLLMClient)
	if !isCapabilityLLMProvider {
		t.Fatalf("expected capability llm provider, got %T", languageModelProvider)
	}
	if capabilityLLMClient.ModelName != "gemma-4-E4B-it" {
		t.Fatalf("expected capability model, got %q", capabilityLLMClient.ModelName)
	}
	if capabilityLLMClient.ExecutionMode != "auto" {
		t.Fatalf("expected capability execution mode, got %q", capabilityLLMClient.ExecutionMode)
	}
}

func TestConfiguredProviderRejectsDirectOpenRouterProductPath(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "openRouter"

	_, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue == nil {
		t.Fatal("expected direct openrouter provider to be unsupported")
	}
}

func TestConfiguredProviderRejectsProductFallback(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "capabilityLLM"
	runtimeConfiguration.LanguageModel.FallbackProvider = "liteRTLM"

	_, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue == nil {
		t.Fatal("expected product fallback provider to be unsupported")
	}
}
