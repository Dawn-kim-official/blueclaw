package llm

import "context"

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) TextChatCompleter() (ChatCompleter, bool) {
	_, hasPrimary := ResolveTextChatCompleter(fallbackLanguageModelProvider.PrimaryProvider)
	_, hasFallback := ResolveTextChatCompleter(fallbackLanguageModelProvider.FallbackProvider)
	if !hasPrimary && !hasFallback {
		return nil, false
	}
	return fallbackLanguageModelProvider, true
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) RecoveryChatCompleter() (RecoveryChatCompleter, bool) {
	primaryProvider, hasPrimary := adaptRecoveryChatProvider(fallbackLanguageModelProvider.PrimaryProvider)
	fallbackProvider, hasFallback := adaptRecoveryChatProvider(fallbackLanguageModelProvider.FallbackProvider)
	if !hasPrimary && !hasFallback {
		return nil, false
	}
	fallbackLanguageModelProvider.PrimaryProvider = primaryProvider
	fallbackLanguageModelProvider.FallbackProvider = fallbackProvider
	return fallbackLanguageModelProvider, true
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) LocalRecoveryChatCompleter() (LocalRecoveryChatCompleter, bool) {
	primaryProvider, hasPrimary := adaptLocalRecoveryChatProvider(fallbackLanguageModelProvider.PrimaryProvider)
	fallbackProvider, hasFallback := adaptLocalRecoveryChatProvider(fallbackLanguageModelProvider.FallbackProvider)
	if !hasPrimary && !hasFallback {
		return nil, false
	}
	fallbackLanguageModelProvider.PrimaryProvider = primaryProvider
	fallbackLanguageModelProvider.FallbackProvider = fallbackProvider
	return fallbackLanguageModelProvider, true
}

func (provider VisionFallbackProvider) TextChatCompleter() (ChatCompleter, bool) {
	textOnlyCompleter, hasTextOnlyCompleter := ResolveTextChatCompleter(provider.TextOnlyModel)
	visionCompleter, hasVisionCompleter := ResolveTextChatCompleter(provider.VisionModel)
	if !hasTextOnlyCompleter {
		return visionCompleter, hasVisionCompleter
	}
	if !hasVisionCompleter {
		return textOnlyCompleter, true
	}
	return visionChatCompleter{textOnlyCompleter: textOnlyCompleter, visionCompleter: visionCompleter}, true
}

func (provider VisionFallbackProvider) RecoveryChatCompleter() (RecoveryChatCompleter, bool) {
	if completer, isAvailable := ResolveRecoveryChatCompleter(provider.TextOnlyModel); isAvailable {
		return completer, true
	}
	return ResolveRecoveryChatCompleter(provider.VisionModel)
}

func (provider VisionFallbackProvider) LocalRecoveryChatCompleter() (LocalRecoveryChatCompleter, bool) {
	if completer, isAvailable := ResolveLocalRecoveryChatCompleter(provider.TextOnlyModel); isAvailable {
		return completer, true
	}
	return ResolveLocalRecoveryChatCompleter(provider.VisionModel)
}

type visionChatCompleter struct {
	textOnlyCompleter ChatCompleter
	visionCompleter   ChatCompleter
}

func (completer visionChatCompleter) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	if chatRequestContainsImage(request) {
		return completer.visionCompleter.GenerateChatCompletion(responseContext, request)
	}
	return completer.textOnlyCompleter.GenerateChatCompletion(responseContext, request)
}

type recoveryChatProviderAdapter struct {
	provider LanguageModelProvider
	delegate RecoveryChatCompleter
}

func (adapter recoveryChatProviderAdapter) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return adapter.provider.GenerateResponse(ctx, prompt)
}

func (adapter recoveryChatProviderAdapter) GenerateStructuredResponse(ctx context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	return adapter.provider.GenerateStructuredResponse(ctx, request)
}

func (adapter recoveryChatProviderAdapter) GenerateRecoveryChatCompletion(ctx context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return adapter.delegate.GenerateRecoveryChatCompletion(ctx, request)
}

type localRecoveryChatProviderAdapter struct {
	provider LanguageModelProvider
	delegate LocalRecoveryChatCompleter
}

func (adapter localRecoveryChatProviderAdapter) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return adapter.provider.GenerateResponse(ctx, prompt)
}

func (adapter localRecoveryChatProviderAdapter) GenerateStructuredResponse(ctx context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	return adapter.provider.GenerateStructuredResponse(ctx, request)
}

func (adapter localRecoveryChatProviderAdapter) GenerateLocalRecoveryChatCompletion(ctx context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return adapter.delegate.GenerateLocalRecoveryChatCompletion(ctx, request)
}

func adaptRecoveryChatProvider(provider LanguageModelProvider) (LanguageModelProvider, bool) {
	if provider == nil {
		return nil, false
	}
	completer, isAvailable := ResolveRecoveryChatCompleter(provider)
	if !isAvailable {
		return provider, false
	}
	return recoveryChatProviderAdapter{provider: provider, delegate: completer}, true
}

func adaptLocalRecoveryChatProvider(provider LanguageModelProvider) (LanguageModelProvider, bool) {
	if provider == nil {
		return nil, false
	}
	completer, isAvailable := ResolveLocalRecoveryChatCompleter(provider)
	if !isAvailable {
		return provider, false
	}
	return localRecoveryChatProviderAdapter{provider: provider, delegate: completer}, true
}
