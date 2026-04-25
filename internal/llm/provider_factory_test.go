package llm

import (
	"testing"

	"blueclaw/internal/config"
)

func TestConfiguredProviderUsesCapabilityBoundary(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Capability.Transport = "unix"
	runtimeConfiguration.Capability.SocketPath = "/run/internkim/capability.sock"
	runtimeConfiguration.Capability.Endpoint = "http://internkim"
	runtimeConfiguration.LanguageModel.DefaultProvider = "capability"
	runtimeConfiguration.LanguageModel.Capability.Model = "openai/gpt-4.1-mini"
	runtimeConfiguration.LanguageModel.Capability.ExecutionMode = "auto"

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected provider to be created: %v", errorValue)
	}

	capabilityClient, isCapabilityProvider := languageModelProvider.(CapabilityClient)
	if !isCapabilityProvider {
		t.Fatalf("expected capability provider, got %T", languageModelProvider)
	}
	if capabilityClient.ModelName != "openai/gpt-4.1-mini" {
		t.Fatalf("expected capability model, got %q", capabilityClient.ModelName)
	}
	if capabilityClient.ExecutionMode != "auto" {
		t.Fatalf("expected capability execution mode, got %q", capabilityClient.ExecutionMode)
	}
}

func TestConfiguredProviderRejectsDirectSecretProviders(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "openRouter"

	_, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue == nil {
		t.Fatal("expected direct provider to be rejected")
	}
}
