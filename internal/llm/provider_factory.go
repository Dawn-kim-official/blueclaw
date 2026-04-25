package llm

import (
	"errors"
	"strings"

	"blueclaw/internal/capability"
	"blueclaw/internal/config"
)

func NewConfiguredLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) (LanguageModelProvider, error) {
	capabilityClient := CapabilityClient{
		Client:                capability.NewClient(runtimeConfiguration.Capability.Transport, runtimeConfiguration.Capability.SocketPath, runtimeConfiguration.Capability.Endpoint, runtimeConfiguration.Capability.VSockCID, runtimeConfiguration.Capability.VSockPort),
		ModelName:             strings.TrimSpace(runtimeConfiguration.LanguageModel.Capability.Model),
		ExecutionMode:         runtimeConfiguration.LanguageModel.Capability.ExecutionMode,
		RequireParameters:     runtimeConfiguration.LanguageModel.Capability.RequireParameters,
		EnableResponseHealing: runtimeConfiguration.LanguageModel.Capability.EnableResponseHealing,
	}

	defaultProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.DefaultProvider, capabilityClient)
	if errorValue != nil {
		return nil, errorValue
	}

	if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" {
		return defaultProvider, nil
	}

	fallbackProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.FallbackProvider, capabilityClient)
	if errorValue != nil {
		return nil, errorValue
	}

	return FallbackLanguageModelProvider{
		PrimaryProvider:  defaultProvider,
		FallbackProvider: fallbackProvider,
	}, nil
}

func providerByName(providerName string, capabilityClient CapabilityClient) (LanguageModelProvider, error) {
	switch strings.TrimSpace(providerName) {
	case "capability":
		return capabilityClient, nil
	default:
		return nil, errors.New("language model provider is not supported")
	}
}
