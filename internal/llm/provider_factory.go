package llm

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"blueclaw/internal/capability"
	"blueclaw/internal/config"
)

func NewConfiguredLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) (LanguageModelProvider, error) {
	return NewConfiguredLanguageModelProviderForModel(runtimeConfiguration, capabilityModelName(runtimeConfiguration))
}

func NewConfiguredLanguageModelProviderForModel(runtimeConfiguration config.RuntimeConfiguration, modelName string) (LanguageModelProvider, error) {
	defaultProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.DefaultProvider, runtimeConfiguration, modelName)
	if errorValue != nil {
		return nil, errorValue
	}

	if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" {
		return withConfiguredSDKDShadow(defaultProvider, runtimeConfiguration, modelName)
	}

	return nil, errors.New("language model fallback is owned by InternKim capability runtime")
}

func providerByName(providerName string, runtimeConfiguration config.RuntimeConfiguration, modelName string) (LanguageModelProvider, error) {
	switch strings.TrimSpace(providerName) {
	case "capabilityLLM", "capability", "":
		return NewCapabilityLLMClientForModel(runtimeConfiguration, modelName), nil
	case "sdkd":
		return newSDKDClient(runtimeConfiguration, modelName)
	default:
		return nil, errors.New("language model provider is not supported")
	}
}

func withConfiguredSDKDShadow(primaryProvider LanguageModelProvider, runtimeConfiguration config.RuntimeConfiguration, modelName string) (LanguageModelProvider, error) {
	if !runtimeConfiguration.LanguageModel.SDKD.ShadowEnabled || strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) == "sdkd" {
		return primaryProvider, nil
	}
	shadowProvider, errorValue := newSDKDClient(runtimeConfiguration, modelName)
	if errorValue != nil {
		return nil, errorValue
	}
	shadowProvider.StructuredFallbackProvider = nil
	return withShadowRecoveryChatCapabilities(ShadowLanguageModelProvider{
		PrimaryProvider:       primaryProvider,
		ShadowProvider:        shadowProvider,
		Logger:                slog.Default(),
		StructuredSchemaNames: configuredSDKDSchemaNames(runtimeConfiguration),
	}), nil
}

func newSDKDClient(runtimeConfiguration config.RuntimeConfiguration, modelName string) (SDKDClient, error) {
	sdkdConfiguration := runtimeConfiguration.LanguageModel.SDKD
	authKey := ""
	authKeyPath := strings.TrimSpace(sdkdConfiguration.AuthKeyPath)
	if authKeyPath == "" && strings.TrimRight(strings.TrimSpace(sdkdConfiguration.Endpoint), "/") != sdkdLoopbackBridgeEndpoint {
		return SDKDClient{}, errors.New("sdkd auth key path is not configured")
	}
	if authKeyPath != "" {
		authKeyDocument, errorValue := os.ReadFile(authKeyPath)
		if errorValue != nil {
			return SDKDClient{}, errorValue
		}
		authKey = strings.TrimSpace(string(authKeyDocument))
		if authKey == "" {
			return SDKDClient{}, errors.New("sdkd auth key is empty")
		}
	}
	capabilityProvider := NewCapabilityLLMClientForModel(runtimeConfiguration, modelName)
	return NewSDKDClient(SDKDClientConfiguration{
		Endpoint:                        sdkdConfiguration.Endpoint,
		UnixSocketPath:                  sdkdConfiguration.UnixSocketPath,
		AuthKey:                         authKey,
		ModelName:                       modelName,
		ExecutionMode:                   firstNonEmptyModelName(sdkdConfiguration.ExecutionMode, runtimeConfiguration.LanguageModel.Capability.ExecutionMode),
		LocalOnly:                       sdkdConfiguration.LocalOnly,
		TextProvider:                    capabilityProvider,
		StructuredFallbackProvider:      capabilityProvider,
		StructuredSchemaNames:           configuredSDKDSchemaNames(runtimeConfiguration),
		IsStructuredOutputAuthoritative: strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) == "sdkd",
	}), nil
}

func configuredSDKDSchemaNames(runtimeConfiguration config.RuntimeConfiguration) []string {
	configuredSchemaNames := runtimeConfiguration.LanguageModel.SDKD.StructuredSchemaNames
	if len(configuredSchemaNames) == 0 {
		return []string{"blueclaw_agent_turn_action"}
	}
	return append([]string{}, configuredSchemaNames...)
}

func newCapabilityLLMClient(runtimeConfiguration config.RuntimeConfiguration) CapabilityLLMClient {
	return NewCapabilityLLMClientForModel(runtimeConfiguration, capabilityModelName(runtimeConfiguration))
}

func NewCapabilityLLMClientForModel(runtimeConfiguration config.RuntimeConfiguration, modelName string) CapabilityLLMClient {
	return CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
			Transport:      runtimeConfiguration.Capabilities.Transport,
			UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
			VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
			VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
			Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
		}),
		ModelName:     strings.TrimSpace(modelName),
		ExecutionMode: runtimeConfiguration.LanguageModel.Capability.ExecutionMode,
	}
}

func capabilityModelName(runtimeConfiguration config.RuntimeConfiguration) string {
	return strings.TrimSpace(runtimeConfiguration.LanguageModel.Capability.Model)
}

const (
	defaultMaxModelName    = "google/gemini-3.5-flash"
	defaultXHighModelName  = "openai/gpt-5.6-luna"
	defaultHighModelName   = "google/gemini-3-flash-preview"
	defaultMediumModelName = "google/gemini-3.1-flash-lite"
	defaultLowModelName    = "xiaomi/mimo-v2.5"
	defaultXLowModelName   = "deepseek/deepseek-v4-flash"
	defaultCodingModelName = "z-ai/glm-5.2"
)

type ModelTierNames struct {
	Max    string
	XHigh  string
	High   string
	Medium string
	Low    string
	XLow   string
	Coding string
}

func ResolveModelTierNames(runtimeConfiguration config.RuntimeConfiguration) ModelTierNames {
	capabilityConfiguration := runtimeConfiguration.LanguageModel.Capability
	return ModelTierNames{
		Max:    firstNonEmptyModelName(capabilityConfiguration.MaxModel, defaultMaxModelName),
		XHigh:  firstNonEmptyModelName(capabilityConfiguration.XHighModel, defaultXHighModelName),
		High:   firstNonEmptyModelName(capabilityConfiguration.HighModel, defaultHighModelName),
		Medium: firstNonEmptyModelName(capabilityConfiguration.MediumModel, defaultMediumModelName),
		Low:    firstNonEmptyModelName(capabilityConfiguration.LowModel, defaultLowModelName),
		XLow:   firstNonEmptyModelName(capabilityConfiguration.XLowModel, defaultXLowModelName),
		Coding: firstNonEmptyModelName(capabilityConfiguration.CodingModel, defaultCodingModelName),
	}
}

func firstNonEmptyModelName(candidates ...string) string {
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
