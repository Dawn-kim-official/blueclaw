package llm

import "context"

type tierLanguageModelProvider struct {
	provider  LanguageModelProvider
	modelTier string
}

func WithModelTier(provider LanguageModelProvider, modelTier string) LanguageModelProvider {
	if provider == nil || modelTier == "" {
		return provider
	}
	base := tierLanguageModelProvider{provider: provider, modelTier: modelTier}
	_, hasRecovery := provider.(RecoveryResponder)
	_, hasLocalRecovery := provider.(LocalRecoveryResponder)
	if hasRecovery && hasLocalRecovery {
		return tierRecoveryCapabilities{base, tierRecoveryResponder{base}, tierLocalRecoveryResponder{base}}
	}
	if hasRecovery {
		return struct {
			tierLanguageModelProvider
			tierRecoveryResponder
		}{base, tierRecoveryResponder{base}}
	}
	if hasLocalRecovery {
		return struct {
			tierLanguageModelProvider
			tierLocalRecoveryResponder
		}{base, tierLocalRecoveryResponder{base}}
	}
	return base
}

func (provider tierLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	return provider.provider.GenerateResponse(responseContext, prompt)
}

func (provider tierLanguageModelProvider) UnderlyingProvider() LanguageModelProvider {
	return provider.provider
}

func (provider tierLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	response, errorValue := provider.provider.GenerateStructuredResponse(responseContext, request)
	if response.ModelTier == "" {
		response.ModelTier = provider.modelTier
	}
	return response, errorValue
}

func (provider tierLanguageModelProvider) TextChatCompleter() (ChatCompleter, bool) {
	completer, isAvailable := ResolveTextChatCompleter(provider.provider)
	if !isAvailable {
		return nil, false
	}
	return tierChatCompleter{provider: provider, delegate: completer}, true
}

func (provider tierLanguageModelProvider) RecoveryChatCompleter() (RecoveryChatCompleter, bool) {
	completer, isAvailable := ResolveRecoveryChatCompleter(provider.provider)
	if !isAvailable {
		return nil, false
	}
	return tierRecoveryChatCompleter{provider: provider, delegate: completer}, true
}

func (provider tierLanguageModelProvider) LocalRecoveryChatCompleter() (LocalRecoveryChatCompleter, bool) {
	completer, isAvailable := ResolveLocalRecoveryChatCompleter(provider.provider)
	if !isAvailable {
		return nil, false
	}
	return tierLocalRecoveryChatCompleter{provider: provider, delegate: completer}, true
}

type tierRecoveryResponder struct {
	provider tierLanguageModelProvider
}

func (responder tierRecoveryResponder) GenerateRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	return responder.provider.provider.(RecoveryResponder).GenerateRecoveryResponse(responseContext, prompt)
}

type tierLocalRecoveryResponder struct {
	provider tierLanguageModelProvider
}

func (responder tierLocalRecoveryResponder) GenerateLocalRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	return responder.provider.provider.(LocalRecoveryResponder).GenerateLocalRecoveryResponse(responseContext, prompt)
}

type tierRecoveryCapabilities struct {
	tierLanguageModelProvider
	tierRecoveryResponder
	tierLocalRecoveryResponder
}

type tierChatCompleter struct {
	provider tierLanguageModelProvider
	delegate ChatCompleter
}

func (completer tierChatCompleter) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	response, errorValue := completer.delegate.GenerateChatCompletion(responseContext, request)
	if response.ModelTier == "" {
		response.ModelTier = completer.provider.modelTier
	}
	return response, errorValue
}

type tierRecoveryChatCompleter struct {
	provider tierLanguageModelProvider
	delegate RecoveryChatCompleter
}

func (completer tierRecoveryChatCompleter) GenerateRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	response, errorValue := completer.delegate.GenerateRecoveryChatCompletion(responseContext, request)
	if response.ModelTier == "" {
		response.ModelTier = completer.provider.modelTier
	}
	return response, errorValue
}

type tierLocalRecoveryChatCompleter struct {
	provider tierLanguageModelProvider
	delegate LocalRecoveryChatCompleter
}

func (completer tierLocalRecoveryChatCompleter) GenerateLocalRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	response, errorValue := completer.delegate.GenerateLocalRecoveryChatCompletion(responseContext, request)
	if response.ModelTier == "" {
		response.ModelTier = completer.provider.modelTier
	}
	return response, errorValue
}
