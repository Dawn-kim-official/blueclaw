package llm

import (
	"context"
	"testing"
)

func TestResolveTextChatCompleterPrefersAuthoritativeTextProvider(t *testing.T) {
	primaryProvider := &resolverLanguageModelProvider{}
	fallbackProvider := &resolverLanguageModelProvider{}
	visionProvider := &resolverLanguageModelProvider{}
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
			expected: FallbackLanguageModelProvider{
				PrimaryProvider:  primaryProvider,
				FallbackProvider: fallbackProvider,
			},
		},
		{
			name: "fallback provider",
			provider: FallbackLanguageModelProvider{
				PrimaryProvider:  resolverLanguageModelProviderWithoutChat{},
				FallbackProvider: fallbackProvider,
			},
			expected: FallbackLanguageModelProvider{
				PrimaryProvider:  resolverLanguageModelProviderWithoutChat{},
				FallbackProvider: fallbackProvider,
			},
		},
		{
			name: "vision fallback provider",
			provider: VisionFallbackProvider{
				TextOnlyModel: resolverLanguageModelProviderWithoutChat{},
				VisionModel:   visionProvider,
			},
			expected: visionProvider,
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

func TestResolveTextChatCompleterRoutesImageChatRequestsToTheVisionModel(t *testing.T) {
	textOnlyModel := &countingChatProvider{name: "text-only"}
	visionModel := &countingChatProvider{name: "vision"}
	completer, isAvailable := ResolveTextChatCompleter(VisionFallbackProvider{
		TextOnlyModel: textOnlyModel,
		VisionModel:   visionModel,
	})
	if !isAvailable {
		t.Fatal("expected a chat completer for the vision fallback provider")
	}

	textRequest := ChatCompletionRequest{Messages: []ChatCompletionMessage{{Role: "user", Content: "hello"}}}
	imageRequest := ChatCompletionRequest{Messages: []ChatCompletionMessage{{
		Role:  "user",
		Parts: []MessagePart{{Type: "image", MimeType: "image/png", DataBase64: "aGVsbG8="}},
	}}}
	if _, errorValue := completer.GenerateChatCompletion(context.Background(), textRequest); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := completer.GenerateChatCompletion(context.Background(), imageRequest); errorValue != nil {
		t.Fatal(errorValue)
	}

	if textOnlyModel.chatCalls != 1 || visionModel.chatCalls != 1 {
		t.Fatalf("expected the text request on the text-only model and the image request on the vision model, got text-only=%d vision=%d", textOnlyModel.chatCalls, visionModel.chatCalls)
	}
}

type countingChatProvider struct {
	name      string
	chatCalls int
}

func (provider *countingChatProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider *countingChatProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, nil
}

func (provider *countingChatProvider) GenerateChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	provider.chatCalls++
	return ChatCompletionResponse{ModelName: provider.name}, nil
}

func TestResolveTextChatCompleterReportsUnavailableProvider(t *testing.T) {
	completer, isAvailable := ResolveTextChatCompleter(resolverLanguageModelProviderWithoutChat{})
	if isAvailable || completer != nil {
		t.Fatalf("expected chat completer to be unavailable, got %T, available=%v", completer, isAvailable)
	}
}

func TestResolveTextChatCompleterUsesNestedOptionalAccessor(t *testing.T) {
	provider := &resolverLanguageModelProvider{}
	nestedProvider := resolverChatCompleterAccessor{provider: resolverChatCompleterAccessor{provider: provider}}

	completer, isAvailable := ResolveTextChatCompleter(nestedProvider)
	if !isAvailable || completer == nil {
		t.Fatal("expected nested chat completer to be available")
	}
	var languageModelProvider LanguageModelProvider = nestedProvider
	if _, isDirectProvider := languageModelProvider.(ChatCompleter); isDirectProvider {
		t.Fatal("optional accessor must not make wrapper a ChatCompleter")
	}
}

func TestResolveTextChatCompleterDoesNotInventCapabilityThroughAccessor(t *testing.T) {
	provider := resolverChatCompleterAccessor{provider: resolverLanguageModelProviderWithoutChat{}}
	completer, isAvailable := ResolveTextChatCompleter(provider)
	if isAvailable || completer != nil {
		t.Fatalf("expected unavailable nested chat completer, got %T available=%v", completer, isAvailable)
	}
}

func TestResolveRecoveryChatCompletersThroughNestedAccessors(t *testing.T) {
	provider := resolverRecoveryChatProvider{}
	accessor := resolverRecoveryChatAccessor{provider: resolverRecoveryChatAccessor{provider: provider}}

	recoveryCompleter, hasRecovery := ResolveRecoveryChatCompleter(accessor)
	if !hasRecovery || recoveryCompleter == nil {
		t.Fatal("expected nested recovery chat completer")
	}
	localCompleter, hasLocalRecovery := ResolveLocalRecoveryChatCompleter(accessor)
	if !hasLocalRecovery || localCompleter == nil {
		t.Fatal("expected nested local recovery chat completer")
	}
}

func TestResolveRecoveryChatCompletersDoesNotInventCapabilities(t *testing.T) {
	provider := resolverRecoveryChatAccessor{provider: resolverLanguageModelProviderWithoutChat{}}
	if _, isAvailable := ResolveRecoveryChatCompleter(provider); isAvailable {
		t.Fatal("expected recovery chat completer to be unavailable")
	}
	if _, isAvailable := ResolveLocalRecoveryChatCompleter(provider); isAvailable {
		t.Fatal("expected local recovery chat completer to be unavailable")
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

type resolverRecoveryChatProvider struct{}

func (resolverRecoveryChatProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (resolverRecoveryChatProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return StructuredResponse{}, nil
}

func (resolverRecoveryChatProvider) GenerateRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return ChatCompletionResponse{}, nil
}

func (resolverRecoveryChatProvider) GenerateLocalRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return ChatCompletionResponse{}, nil
}

type resolverChatCompleterAccessor struct {
	provider LanguageModelProvider
}

func (accessor resolverChatCompleterAccessor) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return accessor.provider.GenerateResponse(ctx, prompt)
}

func (accessor resolverChatCompleterAccessor) GenerateStructuredResponse(ctx context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	return accessor.provider.GenerateStructuredResponse(ctx, request)
}

func (accessor resolverChatCompleterAccessor) TextChatCompleter() (ChatCompleter, bool) {
	return ResolveTextChatCompleter(accessor.provider)
}

type resolverRecoveryChatAccessor struct {
	provider LanguageModelProvider
}

func (accessor resolverRecoveryChatAccessor) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return accessor.provider.GenerateResponse(ctx, prompt)
}

func (accessor resolverRecoveryChatAccessor) GenerateStructuredResponse(ctx context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	return accessor.provider.GenerateStructuredResponse(ctx, request)
}

func (accessor resolverRecoveryChatAccessor) RecoveryChatCompleter() (RecoveryChatCompleter, bool) {
	return ResolveRecoveryChatCompleter(accessor.provider)
}

func (accessor resolverRecoveryChatAccessor) LocalRecoveryChatCompleter() (LocalRecoveryChatCompleter, bool) {
	return ResolveLocalRecoveryChatCompleter(accessor.provider)
}
