package llm

import (
	"errors"
	"net/http"
	"strings"

	"blueclaw/internal/capability"
	"blueclaw/internal/config"
)

func NewConfiguredLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) (LanguageModelProvider, error) {
	capabilityClient := CapabilityClient{
		Client:                capability.NewClient(runtimeConfiguration.Capability.SocketPath, runtimeConfiguration.Capability.Endpoint),
		ModelName:             runtimeConfiguration.LanguageModel.Capability.ModelName,
		RequireParameters:     runtimeConfiguration.LanguageModel.Capability.RequireParameters,
		EnableResponseHealing: runtimeConfiguration.LanguageModel.Capability.EnableResponseHealing,
	}
	openRouterClient := OpenRouterClient{
		BaseURL:               runtimeConfiguration.LanguageModel.OpenRouter.BaseURL,
		ModelName:             runtimeConfiguration.LanguageModel.OpenRouter.ModelName,
		RequireParameters:     runtimeConfiguration.LanguageModel.OpenRouter.RequireParameters,
		EnableResponseHealing: runtimeConfiguration.LanguageModel.OpenRouter.EnableResponseHealing,
		HTTPClient:            http.DefaultClient,
	}

	liteRTLMClient := LiteRTLMClient{
		WrapperPath:        runtimeConfiguration.LanguageModel.LiteRTLM.WrapperPath,
		WrapperArguments:   append([]string{}, runtimeConfiguration.LanguageModel.LiteRTLM.WrapperArguments...),
		ModelPath:          runtimeConfiguration.LanguageModel.LiteRTLM.ModelPath,
		Backend:            runtimeConfiguration.LanguageModel.LiteRTLM.Backend,
		ConstraintProvider: runtimeConfiguration.LanguageModel.LiteRTLM.ConstraintProvider,
	}

	defaultProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.DefaultProvider, capabilityClient, openRouterClient, liteRTLMClient)
	if errorValue != nil {
		return nil, errorValue
	}

	if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" {
		return defaultProvider, nil
	}

	fallbackProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.FallbackProvider, capabilityClient, openRouterClient, liteRTLMClient)
	if errorValue != nil {
		return nil, errorValue
	}

	return FallbackLanguageModelProvider{
		PrimaryProvider:  defaultProvider,
		FallbackProvider: fallbackProvider,
	}, nil
}

func providerByName(providerName string, capabilityClient CapabilityClient, openRouterClient OpenRouterClient, liteRTLMClient LiteRTLMClient) (LanguageModelProvider, error) {
	switch strings.TrimSpace(providerName) {
	case "capability":
		return capabilityClient, nil
	case "openRouter":
		return openRouterClient, nil
	case "liteRTLM":
		return liteRTLMClient, nil
	default:
		return nil, errors.New("language model provider is not supported")
	}
}
