package model

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
	if accessor, isAccessor := provider.(ChatCompleterAccessor); isAccessor {
		completer, isAvailable := accessor.TextChatCompleter()
		if !isAvailable || completer == nil {
			return nil, false
		}
		return completer, true
	}
	completer, isAvailable := provider.(ChatCompleter)
	return completer, isAvailable
}

func ResolveRecoveryChatCompleter(provider LanguageModelProvider) (RecoveryChatCompleter, bool) {
	if provider == nil {
		return nil, false
	}
	if accessor, isAccessor := provider.(RecoveryChatCompleterAccessor); isAccessor {
		completer, isAvailable := accessor.RecoveryChatCompleter()
		if !isAvailable || completer == nil {
			return nil, false
		}
		return completer, true
	}
	completer, isAvailable := provider.(RecoveryChatCompleter)
	return completer, isAvailable
}

func ResolveLocalRecoveryChatCompleter(provider LanguageModelProvider) (LocalRecoveryChatCompleter, bool) {
	if provider == nil {
		return nil, false
	}
	if accessor, isAccessor := provider.(LocalRecoveryChatCompleterAccessor); isAccessor {
		completer, isAvailable := accessor.LocalRecoveryChatCompleter()
		if !isAvailable || completer == nil {
			return nil, false
		}
		return completer, true
	}
	completer, isAvailable := provider.(LocalRecoveryChatCompleter)
	return completer, isAvailable
}
