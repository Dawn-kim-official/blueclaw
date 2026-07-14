package llm

import (
	"os"
	"path/filepath"
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

func TestConfiguredProviderCreatesSDKDOnlyWhenSelected(t *testing.T) {
	authKeyPath := filepath.Join(t.TempDir(), "sdkd.key")
	if errorValue := os.WriteFile(authKeyPath, []byte("installation-key\n"), 0o600); errorValue != nil {
		t.Fatalf("expected auth key fixture: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "sdkd"
	runtimeConfiguration.LanguageModel.Capability.Model = "deepseek/deepseek-v4-flash"
	runtimeConfiguration.LanguageModel.SDKD.AuthKeyPath = authKeyPath
	runtimeConfiguration.LanguageModel.SDKD.UnixSocketPath = "/run/blueclaw/sdkd.sock"

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected sdkd provider: %v", errorValue)
	}
	sdkdClient, isSDKDClient := languageModelProvider.(SDKDClient)
	if !isSDKDClient {
		t.Fatalf("expected sdkd provider, got %T", languageModelProvider)
	}
	if sdkdClient.AuthKey != "installation-key" || sdkdClient.ModelName != "deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected sdkd client configuration: %+v", sdkdClient)
	}
}

func TestConfiguredProviderCreatesSDKDBridgeWithoutGuestCredential(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "sdkd"
	runtimeConfiguration.LanguageModel.Capability.Model = "deepseek/deepseek-v4-flash"
	runtimeConfiguration.LanguageModel.SDKD.Endpoint = sdkdLoopbackBridgeEndpoint

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected SDKD bridge provider: %v", errorValue)
	}
	sdkdClient, isSDKDClient := languageModelProvider.(SDKDClient)
	if !isSDKDClient || sdkdClient.AuthKey != "" || sdkdClient.Endpoint != runtimeConfiguration.LanguageModel.SDKD.Endpoint {
		t.Fatalf("unexpected SDKD bridge client: %+v", languageModelProvider)
	}
}

func TestConfiguredProviderWrapsCapabilityWithOptionalSDKDShadow(t *testing.T) {
	authKeyPath := filepath.Join(t.TempDir(), "sdkd.key")
	if errorValue := os.WriteFile(authKeyPath, []byte("installation-key"), 0o600); errorValue != nil {
		t.Fatalf("expected auth key fixture: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "capabilityLLM"
	runtimeConfiguration.LanguageModel.SDKD.AuthKeyPath = authKeyPath
	runtimeConfiguration.LanguageModel.SDKD.ShadowEnabled = true

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected shadow provider: %v", errorValue)
	}
	shadowProvider, isShadowProvider := languageModelProvider.(ShadowLanguageModelProvider)
	if !isShadowProvider {
		t.Fatalf("expected optional sdkd shadow provider, got %T", languageModelProvider)
	}
	sdkdClient, isSDKDClient := shadowProvider.ShadowProvider.(SDKDClient)
	if !isSDKDClient || sdkdClient.StructuredFallbackProvider != nil {
		t.Fatalf("expected shadow SDKD without legacy fallback, got %+v", shadowProvider.ShadowProvider)
	}
}
