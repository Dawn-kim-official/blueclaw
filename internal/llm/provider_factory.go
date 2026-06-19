package llm

import (
	"errors"
	"strings"
	"time"

	"blueclaw/internal/capability"
	"blueclaw/internal/config"
)

func NewConfiguredLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) (LanguageModelProvider, error) {
	defaultProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.DefaultProvider, runtimeConfiguration)
	if errorValue != nil {
		return nil, errorValue
	}

	if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" {
		return defaultProvider, nil
	}

	return nil, errors.New("language model fallback is owned by InternKim capability runtime")
}

func providerByName(providerName string, runtimeConfiguration config.RuntimeConfiguration) (LanguageModelProvider, error) {
	switch strings.TrimSpace(providerName) {
	case "capabilityLLM", "capability", "":
		return newCapabilityLLMClient(runtimeConfiguration), nil
	default:
		return nil, errors.New("language model provider is not supported")
	}
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
	defaultHighModelName   = "google/gemini-3.5-flash"
	defaultMediumModelName = "x-ai/grok-4.3"
	defaultLowModelName    = "google/gemini-3.1-flash-lite"
	defaultXLowModelName   = "xiaomi/mimo-v2.5"
	defaultCodingModelName = "z-ai/glm-5.2"
)

type ModelTierNames struct {
	High   string
	Medium string
	Low    string
	XLow   string
	Coding string
}

func ResolveModelTierNames(runtimeConfiguration config.RuntimeConfiguration) ModelTierNames {
	capabilityConfiguration := runtimeConfiguration.LanguageModel.Capability
	return ModelTierNames{
		High:   firstNonEmptyModelName(capabilityConfiguration.HighModel, capabilityConfiguration.Model, defaultHighModelName),
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
