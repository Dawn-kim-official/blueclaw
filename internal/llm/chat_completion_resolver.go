package llm

import "context"

type ChatCompleterAccessor interface {
	TextChatCompleter() (ChatCompleter, bool)
}

type RecoveryChatCompleterAccessor interface {
	RecoveryChatCompleter() (RecoveryChatCompleter, bool)
}

type LocalRecoveryChatCompleterAccessor interface {
	LocalRecoveryChatCompleter() (LocalRecoveryChatCompleter, bool)
}

func ResolveTextChatCompleter(provider LanguageModelProvider) (ChatCompleter, bool) {
	if provider == nil {
		return nil, false
	}
	if completer, isAvailable := provider.(ChatCompleter); isAvailable {
		return completer, true
	}
	if accessor, isAvailable := provider.(ChatCompleterAccessor); isAvailable {
		completer, isAvailable := accessor.TextChatCompleter()
		if !isAvailable || completer == nil {
			return nil, false
		}
		return completer, true
	}
	if wrappedProvider, isWrapped := provider.(interface {
		primaryLanguageModelProvider() LanguageModelProvider
	}); isWrapped {
		return ResolveTextChatCompleter(wrappedProvider.primaryLanguageModelProvider())
	}

	switch provider := provider.(type) {
	case FallbackLanguageModelProvider:
		return resolveFallbackTextChatCompleter(provider.PrimaryProvider, provider.FallbackProvider)
	case *FallbackLanguageModelProvider:
		if provider == nil {
			return nil, false
		}
		return resolveFallbackTextChatCompleter(provider.PrimaryProvider, provider.FallbackProvider)
	case VisionFallbackProvider:
		return resolveVisionTextChatCompleter(provider.TextOnlyModel, provider.VisionModel)
	case *VisionFallbackProvider:
		if provider == nil {
			return nil, false
		}
		return resolveVisionTextChatCompleter(provider.TextOnlyModel, provider.VisionModel)
	case ShadowLanguageModelProvider:
		return ResolveTextChatCompleter(provider.PrimaryProvider)
	case *ShadowLanguageModelProvider:
		if provider == nil {
			return nil, false
		}
		return ResolveTextChatCompleter(provider.PrimaryProvider)
	default:
		return nil, false
	}
}

func ResolveRecoveryChatCompleter(provider LanguageModelProvider) (RecoveryChatCompleter, bool) {
	if provider == nil {
		return nil, false
	}
	if fallbackProvider, isFallback := recoveryFallbackProvider(provider); isFallback {
		return resolveFallbackRecoveryChatCompleter(fallbackProvider)
	}
	if completer, isAvailable := provider.(RecoveryChatCompleter); isAvailable {
		return completer, true
	}
	if accessor, isAvailable := provider.(RecoveryChatCompleterAccessor); isAvailable {
		completer, isAvailable := accessor.RecoveryChatCompleter()
		if !isAvailable || completer == nil {
			return nil, false
		}
		return completer, true
	}
	if wrappedProvider, isWrapped := provider.(interface {
		primaryLanguageModelProvider() LanguageModelProvider
	}); isWrapped {
		return ResolveRecoveryChatCompleter(wrappedProvider.primaryLanguageModelProvider())
	}

	switch provider := provider.(type) {
	case FallbackLanguageModelProvider:
		return resolveFallbackRecoveryChatCompleter(provider)
	case *FallbackLanguageModelProvider:
		if provider == nil {
			return nil, false
		}
		return resolveFallbackRecoveryChatCompleter(*provider)
	case VisionFallbackProvider:
		return resolveVisionRecoveryChatCompleter(provider.TextOnlyModel, provider.VisionModel)
	case *VisionFallbackProvider:
		if provider == nil {
			return nil, false
		}
		return resolveVisionRecoveryChatCompleter(provider.TextOnlyModel, provider.VisionModel)
	case ShadowLanguageModelProvider:
		return ResolveRecoveryChatCompleter(provider.PrimaryProvider)
	case *ShadowLanguageModelProvider:
		if provider == nil {
			return nil, false
		}
		return ResolveRecoveryChatCompleter(provider.PrimaryProvider)
	default:
		return nil, false
	}
}

func ResolveLocalRecoveryChatCompleter(provider LanguageModelProvider) (LocalRecoveryChatCompleter, bool) {
	if provider == nil {
		return nil, false
	}
	if fallbackProvider, isFallback := recoveryFallbackProvider(provider); isFallback {
		return resolveFallbackLocalRecoveryChatCompleter(fallbackProvider)
	}
	if completer, isAvailable := provider.(LocalRecoveryChatCompleter); isAvailable {
		return completer, true
	}
	if accessor, isAvailable := provider.(LocalRecoveryChatCompleterAccessor); isAvailable {
		completer, isAvailable := accessor.LocalRecoveryChatCompleter()
		if !isAvailable || completer == nil {
			return nil, false
		}
		return completer, true
	}
	if wrappedProvider, isWrapped := provider.(interface {
		primaryLanguageModelProvider() LanguageModelProvider
	}); isWrapped {
		return ResolveLocalRecoveryChatCompleter(wrappedProvider.primaryLanguageModelProvider())
	}

	switch provider := provider.(type) {
	case FallbackLanguageModelProvider:
		return resolveFallbackLocalRecoveryChatCompleter(provider)
	case *FallbackLanguageModelProvider:
		if provider == nil {
			return nil, false
		}
		return resolveFallbackLocalRecoveryChatCompleter(*provider)
	case VisionFallbackProvider:
		return resolveVisionLocalRecoveryChatCompleter(provider.TextOnlyModel, provider.VisionModel)
	case *VisionFallbackProvider:
		if provider == nil {
			return nil, false
		}
		return resolveVisionLocalRecoveryChatCompleter(provider.TextOnlyModel, provider.VisionModel)
	case ShadowLanguageModelProvider:
		return ResolveLocalRecoveryChatCompleter(provider.PrimaryProvider)
	case *ShadowLanguageModelProvider:
		if provider == nil {
			return nil, false
		}
		return ResolveLocalRecoveryChatCompleter(provider.PrimaryProvider)
	default:
		return nil, false
	}
}

func resolveFallbackTextChatCompleter(primaryProvider LanguageModelProvider, fallbackProvider LanguageModelProvider) (ChatCompleter, bool) {
	if completer, isAvailable := ResolveTextChatCompleter(primaryProvider); isAvailable {
		return completer, true
	}
	return ResolveTextChatCompleter(fallbackProvider)
}

func resolveVisionTextChatCompleter(textOnlyModel LanguageModelProvider, visionModel LanguageModelProvider) (ChatCompleter, bool) {
	if completer, isAvailable := ResolveTextChatCompleter(textOnlyModel); isAvailable {
		return completer, true
	}
	return ResolveTextChatCompleter(visionModel)
}

func resolveFallbackRecoveryChatCompleter(provider FallbackLanguageModelProvider) (RecoveryChatCompleter, bool) {
	primaryProvider, hasPrimary := adaptRecoveryChatProvider(provider.PrimaryProvider)
	fallbackProvider, hasFallback := adaptRecoveryChatProvider(provider.FallbackProvider)
	if !hasPrimary && !hasFallback {
		return nil, false
	}
	provider.PrimaryProvider = primaryProvider
	provider.FallbackProvider = fallbackProvider
	return provider, true
}

func resolveFallbackLocalRecoveryChatCompleter(provider FallbackLanguageModelProvider) (LocalRecoveryChatCompleter, bool) {
	primaryProvider, hasPrimary := adaptLocalRecoveryChatProvider(provider.PrimaryProvider)
	fallbackProvider, hasFallback := adaptLocalRecoveryChatProvider(provider.FallbackProvider)
	if !hasPrimary && !hasFallback {
		return nil, false
	}
	provider.PrimaryProvider = primaryProvider
	provider.FallbackProvider = fallbackProvider
	return provider, true
}

func resolveVisionRecoveryChatCompleter(textOnlyModel LanguageModelProvider, visionModel LanguageModelProvider) (RecoveryChatCompleter, bool) {
	if completer, isAvailable := ResolveRecoveryChatCompleter(textOnlyModel); isAvailable {
		return completer, true
	}
	return ResolveRecoveryChatCompleter(visionModel)
}

func resolveVisionLocalRecoveryChatCompleter(textOnlyModel LanguageModelProvider, visionModel LanguageModelProvider) (LocalRecoveryChatCompleter, bool) {
	if completer, isAvailable := ResolveLocalRecoveryChatCompleter(textOnlyModel); isAvailable {
		return completer, true
	}
	return ResolveLocalRecoveryChatCompleter(visionModel)
}

func recoveryFallbackProvider(provider LanguageModelProvider) (FallbackLanguageModelProvider, bool) {
	switch provider := provider.(type) {
	case FallbackLanguageModelProvider:
		return provider, true
	case *FallbackLanguageModelProvider:
		if provider == nil {
			return FallbackLanguageModelProvider{}, false
		}
		return *provider, true
	default:
		return FallbackLanguageModelProvider{}, false
	}
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
