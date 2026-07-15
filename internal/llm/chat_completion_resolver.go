package llm

func ResolveTextChatCompleter(provider LanguageModelProvider) (ChatCompleter, bool) {
	if provider == nil {
		return nil, false
	}
	if completer, isAvailable := provider.(ChatCompleter); isAvailable {
		return completer, true
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
