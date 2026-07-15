package llm

import (
	"context"
	"testing"
)

func TestResolveTextChatCompleterPrefersAuthoritativeTextProvider(t *testing.T) {
	primaryProvider := &resolverLanguageModelProvider{}
	fallbackProvider := &resolverLanguageModelProvider{}
	visionProvider := &resolverLanguageModelProvider{}
	shadowProvider := &resolverLanguageModelProvider{}
	tests := []struct {
		name     string
		provider LanguageModelProvider
		expected ChatCompleter
	}{
		{
			name:     "direct provider",
			provider: primaryProvider,
			expected: primaryProvider,
		},
		{
			name: "fallback primary",
			provider: FallbackLanguageModelProvider{
				PrimaryProvider:  primaryProvider,
				FallbackProvider: fallbackProvider,
			},
			expected: primaryProvider,
		},
		{
			name: "fallback provider",
			provider: FallbackLanguageModelProvider{
				PrimaryProvider:  resolverLanguageModelProviderWithoutChat{},
				FallbackProvider: fallbackProvider,
			},
			expected: fallbackProvider,
		},
		{
			name: "vision text provider",
			provider: VisionFallbackProvider{
				TextOnlyModel: primaryProvider,
				VisionModel:   visionProvider,
			},
			expected: primaryProvider,
		},
		{
			name: "vision fallback provider",
			provider: VisionFallbackProvider{
				TextOnlyModel: resolverLanguageModelProviderWithoutChat{},
				VisionModel:   visionProvider,
			},
			expected: visionProvider,
		},
		{
			name: "shadow primary provider",
			provider: ShadowLanguageModelProvider{
				PrimaryProvider: primaryProvider,
				ShadowProvider:  shadowProvider,
			},
			expected: primaryProvider,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			completer, isAvailable := ResolveTextChatCompleter(test.provider)
			if !isAvailable || completer != test.expected {
				t.Fatalf("expected %T, got %T, available=%v", test.expected, completer, isAvailable)
			}
		})
	}
}

func TestResolveTextChatCompleterReportsUnavailableProvider(t *testing.T) {
	completer, isAvailable := ResolveTextChatCompleter(resolverLanguageModelProviderWithoutChat{})
	if isAvailable || completer != nil {
		t.Fatalf("expected chat completer to be unavailable, got %T, available=%v", completer, isAvailable)
	}
}

type resolverLanguageModelProvider struct{}

func (resolverLanguageModelProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (resolverLanguageModelProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, nil
}

func (resolverLanguageModelProvider) GenerateChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return ChatCompletionResponse{}, nil
}

type resolverLanguageModelProviderWithoutChat struct{}

func (resolverLanguageModelProviderWithoutChat) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (resolverLanguageModelProviderWithoutChat) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, nil
}
