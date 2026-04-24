package llm

import (
	"errors"
	"net/http"
	"strings"

	"blueclaw/internal/config"
)

func NewConfiguredLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration, apiKey string) (LanguageModelProvider, error) {
	openRouterClient := OpenRouterClient{
		BaseURL:               runtimeConfiguration.LanguageModel.OpenRouter.BaseURL,
		ModelName:             runtimeConfiguration.LanguageModel.OpenRouter.ModelName,
		APIKey:                apiKey,
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

	defaultProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.DefaultProvider, openRouterClient, liteRTLMClient)
	if errorValue != nil {
		return nil, errorValue
	}

	if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" {
		return defaultProvider, nil
	}

	fallbackProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.FallbackProvider, openRouterClient, liteRTLMClient)
	if errorValue != nil {
		return nil, errorValue
	}

	return FallbackLanguageModelProvider{
		PrimaryProvider:  defaultProvider,
		FallbackProvider: fallbackProvider,
	}, nil
}

func providerByName(providerName string, openRouterClient OpenRouterClient, liteRTLMClient LiteRTLMClient) (LanguageModelProvider, error) {
	switch strings.TrimSpace(providerName) {
	case "openRouter":
		return openRouterClient, nil
	case "liteRTLM":
		return liteRTLMClient, nil
	default:
		return nil, errors.New("language model provider is not supported")
	}
}
