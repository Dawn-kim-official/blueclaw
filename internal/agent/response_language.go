package agent

import "strings"

const (
	ResponseLanguageKorean             = "ko"
	ResponseLanguageEnglish            = "en"
	ResponseLanguageSameAsConversation = "same_as_conversation"
)

func DefaultResponseLanguage() string {
	return ResponseLanguageKorean
}

func ResolveResponseLanguage(values ...string) string {
	for _, value := range values {
		normalizedValue := NormalizeResponseLanguage(value)
		switch normalizedValue {
		case ResponseLanguageKorean, ResponseLanguageEnglish:
			return normalizedValue
		}
	}
	return DefaultResponseLanguage()
}

func NormalizeResponseLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ResponseLanguageKorean, "kor", "korean":
		return ResponseLanguageKorean
	case ResponseLanguageEnglish, "eng", "english":
		return ResponseLanguageEnglish
	case ResponseLanguageSameAsConversation:
		return ResponseLanguageSameAsConversation
	default:
		return ""
	}
}

func responseLanguageInstruction(responseLanguage string) string {
	switch ResolveResponseLanguage(responseLanguage) {
	case ResponseLanguageEnglish:
		return "Write every user-facing reply, approval question, and recovery message in English."
	default:
		return "Write every user-facing reply, approval question, and recovery message in Korean."
	}
}
