package toolcontract

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
