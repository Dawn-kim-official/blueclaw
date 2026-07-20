package llm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
)

type ShadowLanguageModelProvider struct {
	PrimaryProvider       LanguageModelProvider
	ShadowProvider        LanguageModelProvider
	Logger                *slog.Logger
	StructuredSchemaNames []string
}

func (provider ShadowLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	if provider.PrimaryProvider == nil {
		return "", errors.New("primary provider is not configured")
	}
	return provider.PrimaryProvider.GenerateResponse(responseContext, prompt)
}

func (provider ShadowLanguageModelProvider) GenerateRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	recoveryProvider, isRecoveryProvider := provider.PrimaryProvider.(RecoveryResponder)
	if !isRecoveryProvider {
		return provider.GenerateResponse(responseContext, prompt)
	}
	return recoveryProvider.GenerateRecoveryResponse(responseContext, prompt)
}

func (provider ShadowLanguageModelProvider) GenerateLocalRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	localRecoveryProvider, isLocalRecoveryProvider := provider.PrimaryProvider.(LocalRecoveryResponder)
	if !isLocalRecoveryProvider {
		return provider.GenerateResponse(responseContext, prompt)
	}
	return localRecoveryProvider.GenerateLocalRecoveryResponse(responseContext, prompt)
}

type shadowRecoveryChatCapability struct{ provider ShadowLanguageModelProvider }
type shadowLocalRecoveryChatCapability struct{ provider ShadowLanguageModelProvider }

type shadowRecoveryChatProvider struct {
	ShadowLanguageModelProvider
	shadowRecoveryChatCapability
}

type shadowLocalRecoveryChatProvider struct {
	ShadowLanguageModelProvider
	shadowLocalRecoveryChatCapability
}

type shadowAllRecoveryChatProvider struct {
	ShadowLanguageModelProvider
	shadowRecoveryChatCapability
	shadowLocalRecoveryChatCapability
}

func withShadowRecoveryChatCapabilities(provider ShadowLanguageModelProvider) LanguageModelProvider {
	_, hasRecoveryChat := provider.PrimaryProvider.(RecoveryChatCompleter)
	_, hasLocalRecoveryChat := provider.PrimaryProvider.(LocalRecoveryChatCompleter)
	switch {
	case hasRecoveryChat && hasLocalRecoveryChat:
		return shadowAllRecoveryChatProvider{
			provider,
			shadowRecoveryChatCapability{provider},
			shadowLocalRecoveryChatCapability{provider},
		}
	case hasRecoveryChat:
		return shadowRecoveryChatProvider{provider, shadowRecoveryChatCapability{provider}}
	case hasLocalRecoveryChat:
		return shadowLocalRecoveryChatProvider{provider, shadowLocalRecoveryChatCapability{provider}}
	default:
		return provider
	}
}

func (capability shadowRecoveryChatCapability) GenerateRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return capability.provider.PrimaryProvider.(RecoveryChatCompleter).GenerateRecoveryChatCompletion(responseContext, request)
}

func (capability shadowLocalRecoveryChatCapability) GenerateLocalRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return capability.provider.PrimaryProvider.(LocalRecoveryChatCompleter).GenerateLocalRecoveryChatCompletion(responseContext, request)
}

func (provider ShadowLanguageModelProvider) primaryLanguageModelProvider() LanguageModelProvider {
	return provider.PrimaryProvider
}

func (provider ShadowLanguageModelProvider) shadowLanguageModelProvider() ShadowLanguageModelProvider {
	return provider
}

func (provider ShadowLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	if provider.PrimaryProvider == nil {
		return StructuredResponse{}, errors.New("primary provider is not configured")
	}
	if provider.ShadowProvider == nil {
		return provider.PrimaryProvider.GenerateStructuredResponse(responseContext, request)
	}
	if !matchesStructuredSchemaName(provider.StructuredSchemaNames, request.StructuredOutputSchema.Name) {
		return provider.PrimaryProvider.GenerateStructuredResponse(responseContext, request)
	}
	primaryResponse, primaryError := provider.PrimaryProvider.GenerateStructuredResponse(responseContext, request)
	shadowContext := context.WithoutCancel(responseContext)
	go provider.generateAndLogShadowResponse(shadowContext, request, primaryResponse, primaryError)
	return primaryResponse, primaryError
}

func matchesStructuredSchemaName(configuredSchemaNames []string, schemaName string) bool {
	if len(configuredSchemaNames) == 0 {
		return true
	}
	for _, configuredSchemaName := range configuredSchemaNames {
		if configuredSchemaName == schemaName {
			return true
		}
	}
	return false
}

func (provider ShadowLanguageModelProvider) generateAndLogShadowResponse(
	responseContext context.Context,
	request StructuredResponseRequest,
	primaryResponse StructuredResponse,
	primaryError error,
) {
	shadowResponse, shadowError := provider.ShadowProvider.GenerateStructuredResponse(responseContext, request)
	provider.logComparison(primaryResponse, primaryError, shadowResponse, shadowError)
}

func (provider ShadowLanguageModelProvider) logComparison(
	primaryResponse StructuredResponse,
	primaryError error,
	shadowResponse StructuredResponse,
	shadowError error,
) {
	if provider.Logger == nil {
		return
	}
	if shadowError != nil {
		provider.Logger.Warn("llmd shadow structured response failed", "error", shadowError.Error())
		return
	}
	provider.Logger.Info(
		"llmd shadow structured response compared",
		"primaryFailed", primaryError != nil,
		"contentMatches", primaryError == nil && structuredContentMatches(primaryResponse.Content, shadowResponse.Content),
		"shadowProvider", shadowResponse.ProviderName,
		"shadowModel", shadowResponse.ModelName,
		"shadowPromptTokens", shadowResponse.Usage.PromptTokens,
		"shadowCompletionTokens", shadowResponse.Usage.CompletionTokens,
	)
}

func structuredContentMatches(primaryContent string, shadowContent string) bool {
	var primaryDocument any
	var shadowDocument any
	if json.Unmarshal([]byte(primaryContent), &primaryDocument) != nil || json.Unmarshal([]byte(shadowContent), &shadowDocument) != nil {
		return primaryContent == shadowContent
	}
	return reflect.DeepEqual(primaryDocument, shadowDocument)
}
