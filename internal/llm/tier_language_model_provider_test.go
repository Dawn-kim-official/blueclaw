package llm

import (
	"context"
	"errors"
	"testing"
)

type tierTestLanguageModelProvider struct {
	structuredResponse StructuredResponse
	chatResponse       ChatCompletionResponse
	recoveryResponse   ChatCompletionResponse
	localResponse      ChatCompletionResponse
}

func (provider tierTestLanguageModelProvider) GenerateResponse(context.Context, string) (string, error) {
	return "reply", nil
}

func (provider tierTestLanguageModelProvider) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	return provider.structuredResponse, nil
}

func (provider tierTestLanguageModelProvider) GenerateChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return provider.chatResponse, nil
}

func (provider tierTestLanguageModelProvider) GenerateRecoveryResponse(context.Context, string) (string, error) {
	return "recovered", nil
}

func (provider tierTestLanguageModelProvider) GenerateLocalRecoveryResponse(context.Context, string) (string, error) {
	return "locally recovered", nil
}

func (provider tierTestLanguageModelProvider) GenerateRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return provider.recoveryResponse, nil
}

func (provider tierTestLanguageModelProvider) GenerateLocalRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	return provider.localResponse, nil
}

func TestWithModelTierAnnotatesStructuredAndChatResponses(t *testing.T) {
	provider := WithModelTier(tierTestLanguageModelProvider{
		structuredResponse: StructuredResponse{ModelName: "high-model"},
		chatResponse:       ChatCompletionResponse{ModelName: "high-model"},
		recoveryResponse:   ChatCompletionResponse{ModelName: "high-model"},
		localResponse:      ChatCompletionResponse{ModelName: "high-model"},
	}, "high")

	structuredResponse, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil || structuredResponse.ModelTier != "high" {
		t.Fatalf("expected structured response tier, got %+v and %v", structuredResponse, errorValue)
	}
	chatCompleter, isAvailable := ResolveTextChatCompleter(provider)
	if !isAvailable {
		t.Fatal("expected chat capability")
	}
	chatResponse, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || chatResponse.ModelTier != "high" {
		t.Fatalf("expected chat response tier, got %+v and %v", chatResponse, errorValue)
	}
	recoveryCompleter, isAvailable := ResolveRecoveryChatCompleter(provider)
	if !isAvailable {
		t.Fatal("expected recovery chat capability")
	}
	recoveryResponse, errorValue := recoveryCompleter.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || recoveryResponse.ModelTier != "high" {
		t.Fatalf("expected recovery response tier, got %+v and %v", recoveryResponse, errorValue)
	}
	localCompleter, isAvailable := ResolveLocalRecoveryChatCompleter(provider)
	if !isAvailable {
		t.Fatal("expected local recovery chat capability")
	}
	localResponse, errorValue := localCompleter.GenerateLocalRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || localResponse.ModelTier != "high" {
		t.Fatalf("expected local recovery response tier, got %+v and %v", localResponse, errorValue)
	}
}

func TestWithModelTierPreservesResponseTierAndMissingCapabilities(t *testing.T) {
	provider := WithModelTier(staticLanguageModelProvider{
		response: StructuredResponse{ModelTier: "low"},
	}, "high")
	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil || response.ModelTier != "low" {
		t.Fatalf("expected existing response tier to remain authoritative, got %+v and %v", response, errorValue)
	}
	if _, isAvailable := ResolveTextChatCompleter(provider); isAvailable {
		t.Fatal("expected missing chat capability to remain unavailable")
	}
	if _, isAvailable := ResolveRecoveryChatCompleter(provider); isAvailable {
		t.Fatal("expected missing recovery chat capability to remain unavailable")
	}
	if _, isAvailable := ResolveLocalRecoveryChatCompleter(provider); isAvailable {
		t.Fatal("expected missing local recovery chat capability to remain unavailable")
	}
}

func TestWithModelTierPreservesFallbackTier(t *testing.T) {
	provider := FallbackLanguageModelProvider{
		PrimaryProvider: WithModelTier(staticLanguageModelProvider{error: errors.New("high unavailable")}, "high"),
		FallbackProvider: WithModelTier(staticLanguageModelProvider{
			response: StructuredResponse{Content: "fallback"},
		}, "medium"),
	}
	response, errorValue := provider.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{})
	if errorValue != nil || response.ModelTier != "medium" || !response.UsedFallback {
		t.Fatalf("expected fallback tier metadata, got %+v and %v", response, errorValue)
	}
}
