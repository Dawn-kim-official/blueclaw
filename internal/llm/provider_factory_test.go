package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
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

func TestConfiguredProviderLeavesCapabilityModelUnsetByDefault(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Capabilities.Endpoint = "http://127.0.0.1:7781"
	runtimeConfiguration.LanguageModel.Capability.ExecutionMode = "auto"

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected provider to be created: %v", errorValue)
	}

	capabilityLLMClient, isCapabilityLLMProvider := languageModelProvider.(CapabilityLLMClient)
	if !isCapabilityLLMProvider {
		t.Fatalf("expected capability llm provider, got %T", languageModelProvider)
	}
	if capabilityLLMClient.ModelName != "" {
		t.Fatalf("expected no default model override, got %q", capabilityLLMClient.ModelName)
	}
}

func TestResolveModelTierNamesUsesBuiltInDefaults(t *testing.T) {
	tierNames := ResolveModelTierNames(config.RuntimeConfiguration{})
	if tierNames.High != defaultHighModelName {
		t.Fatalf("expected high default, got %q", tierNames.High)
	}
	if tierNames.Medium != defaultMediumModelName {
		t.Fatalf("expected medium default, got %q", tierNames.Medium)
	}
	if tierNames.Low != defaultLowModelName {
		t.Fatalf("expected low default, got %q", tierNames.Low)
	}
	if tierNames.XLow != defaultXLowModelName {
		t.Fatalf("expected xlow default, got %q", tierNames.XLow)
	}
}

func TestResolveModelTierNamesIgnoresUntieredModelForTiers(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.Model = "google/custom-base"

	tierNames := ResolveModelTierNames(runtimeConfiguration)
	if tierNames.XHigh != defaultXHighModelName ||
		tierNames.Max != defaultMaxModelName ||
		tierNames.High != defaultHighModelName ||
		tierNames.Medium != defaultMediumModelName ||
		tierNames.Low != defaultLowModelName ||
		tierNames.XLow != defaultXLowModelName ||
		tierNames.Coding != defaultCodingModelName {
		t.Fatalf("expected each tier to keep its own default and ignore the untiered model, got %+v", tierNames)
	}
}

func TestResolveModelTierNamesHonorsExplicitTierOverrides(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.Capability.Model = "google/custom-base"
	runtimeConfiguration.LanguageModel.Capability.HighModel = "vendor/high"
	runtimeConfiguration.LanguageModel.Capability.MediumModel = "vendor/medium"
	runtimeConfiguration.LanguageModel.Capability.LowModel = "vendor/low"

	tierNames := ResolveModelTierNames(runtimeConfiguration)
	if tierNames.High != "vendor/high" || tierNames.Medium != "vendor/medium" || tierNames.Low != "vendor/low" {
		t.Fatalf("expected explicit tier overrides, got %+v", tierNames)
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

func TestConfiguredProviderCreatesLLMDOnlyWhenSelected(t *testing.T) {
	authKeyPath := filepath.Join(t.TempDir(), "llmd.key")
	if errorValue := os.WriteFile(authKeyPath, []byte("installation-key\n"), 0o600); errorValue != nil {
		t.Fatalf("expected auth key fixture: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
	runtimeConfiguration.LanguageModel.Capability.Model = "deepseek/deepseek-v4-flash"
	runtimeConfiguration.LanguageModel.LLMD.AuthKeyPath = authKeyPath
	runtimeConfiguration.LanguageModel.LLMD.UnixSocketPath = "/run/blueclaw/llmd.sock"
	runtimeConfiguration.LanguageModel.LLMD.StructuredSchemaNames = []string{"blueclaw_turn_router", "blueclaw_agent_turn_action"}

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected llmd provider: %v", errorValue)
	}
	llmdClient, isLLMDClient := languageModelProvider.(LLMDClient)
	if !isLLMDClient {
		t.Fatalf("expected llmd provider, got %T", languageModelProvider)
	}
	if llmdClient.AuthKey != "installation-key" || llmdClient.ModelName != "deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected llmd client configuration: %+v", llmdClient)
	}
	if !llmdClient.IsStructuredOutputAuthoritative {
		t.Fatal("expected llmd default provider to make structured fallback authoritative")
	}
	if len(llmdClient.StructuredSchemaNames) != 2 || llmdClient.StructuredSchemaNames[0] != "blueclaw_turn_router" || llmdClient.StructuredSchemaNames[1] != "blueclaw_agent_turn_action" {
		t.Fatalf("expected configured LLMD schemas, got %v", llmdClient.StructuredSchemaNames)
	}
}

func TestConfiguredProviderCreatesLLMDBridgeWithoutGuestCredential(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
	runtimeConfiguration.LanguageModel.Capability.Model = "deepseek/deepseek-v4-flash"
	runtimeConfiguration.LanguageModel.LLMD.Endpoint = llmdLoopbackBridgeEndpoint

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected LLMD bridge provider: %v", errorValue)
	}
	llmdClient, isLLMDClient := languageModelProvider.(LLMDClient)
	if !isLLMDClient || llmdClient.AuthKey != "" || llmdClient.Endpoint != runtimeConfiguration.LanguageModel.LLMD.Endpoint {
		t.Fatalf("unexpected LLMD bridge client: %+v", languageModelProvider)
	}
}

func TestConfiguredProviderCreatesUnixLLMDBridgeWithoutCredential(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
	runtimeConfiguration.LanguageModel.Capability.Model = "xiaomi/mimo-v2.5"
	runtimeConfiguration.LanguageModel.LLMD.Endpoint = "http://internkim/_internkim/llmd"
	runtimeConfiguration.LanguageModel.LLMD.UnixSocketPath = "/run/internkim/capability.sock"

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected Unix LLMD bridge provider: %v", errorValue)
	}
	llmdClient, isLLMDClient := languageModelProvider.(LLMDClient)
	if !isLLMDClient || llmdClient.AuthKey != "" {
		t.Fatalf("unexpected Unix LLMD bridge client: %+v", languageModelProvider)
	}
}

func TestConfiguredProviderRejectsUnauthenticatedRemoteLLMDBridgePath(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
	runtimeConfiguration.LanguageModel.LLMD.Endpoint = "https://llmd.example.com/_internkim/llmd"

	if _, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration); errorValue == nil {
		t.Fatal("expected remote LLMD bridge path without a Unix socket to require authentication")
	}
}
