package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
)

func standaloneLLMDConfiguration(t *testing.T) config.RuntimeConfiguration {
	t.Helper()
	credentialPath := filepath.Join(t.TempDir(), "llmd.credential")
	if errorValue := os.WriteFile(credentialPath, []byte("installation-key\n"), 0o600); errorValue != nil {
		t.Fatalf("expected llmd credential fixture: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
	runtimeConfiguration.LanguageModel.LLMD.AuthKeyPath = credentialPath
	runtimeConfiguration.LanguageModel.LLMD.UnixSocketPath = "/run/blueclaw/llmd.sock"
	return runtimeConfiguration
}

func TestLLMDKeepsCapabilityProvidersWhenTheApplianceSuppliesThem(t *testing.T) {
	runtimeConfiguration := standaloneLLMDConfiguration(t)
	runtimeConfiguration.Capabilities.Transport = "vsock"
	runtimeConfiguration.Capabilities.VSockCID = 2
	runtimeConfiguration.Capabilities.VSockPort = 7000

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected llmd provider: %v", errorValue)
	}
	llmdClient, isLLMDClient := languageModelProvider.(LLMDClient)
	if !isLLMDClient {
		t.Fatalf("expected llmd provider, got %T", languageModelProvider)
	}
	if llmdClient.TextProvider == nil || llmdClient.StructuredFallbackProvider == nil {
		t.Fatal("expected the capability service to keep owning text and unlisted structured schemas")
	}
	if len(llmdClient.StructuredSchemaNames) == 0 {
		t.Fatal("expected the default structured schema routing to stay in place")
	}
}

func TestLLMDOwnsEveryCallWithoutACapabilityService(t *testing.T) {
	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(standaloneLLMDConfiguration(t))
	if errorValue != nil {
		t.Fatalf("expected a standalone llmd provider: %v", errorValue)
	}
	llmdClient, isLLMDClient := languageModelProvider.(LLMDClient)
	if !isLLMDClient {
		t.Fatalf("expected llmd provider, got %T", languageModelProvider)
	}
	if llmdClient.TextProvider != nil || llmdClient.StructuredFallbackProvider != nil {
		t.Fatal("expected no capability provider when no capability service is configured")
	}
}

func TestHasCapabilityEndpointRecognizesEveryTransport(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		configure  func(*config.RuntimeConfiguration)
		isExpected bool
	}{
		{"nothing configured", func(*config.RuntimeConfiguration) {}, false},
		{"http endpoint", func(c *config.RuntimeConfiguration) { c.Capabilities.Endpoint = "http://internkim-capability" }, true},
		{"unix socket", func(c *config.RuntimeConfiguration) { c.Capabilities.UnixSocketPath = "/run/internkim/capability.sock" }, true},
		{"vsock", func(c *config.RuntimeConfiguration) { c.Capabilities.VSockCID, c.Capabilities.VSockPort = 2, 7000 }, true},
		{"vsock without a port", func(c *config.RuntimeConfiguration) { c.Capabilities.VSockCID = 2 }, false},
	} {
		runtimeConfiguration := config.RuntimeConfiguration{}
		testCase.configure(&runtimeConfiguration)
		if HasCapabilityEndpoint(runtimeConfiguration) != testCase.isExpected {
			t.Fatalf("%s: expected %v", testCase.name, testCase.isExpected)
		}
	}
}
