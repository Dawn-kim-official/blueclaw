package bluecollar

import (
	"context"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type recoveryChatNoticeProvider struct {
	chatReply        string
	chatReplies      []string
	chatFinishReason string
	chatError        error
	legacyReply      string
	legacyError      error
	chatCalls        int
	legacyCalls      int
	chatRequests     []model.ChatCompletionRequest
}

type recoveryChatNoticeAccessor struct {
	provider model.LanguageModelProvider
}

func (accessor recoveryChatNoticeAccessor) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return accessor.provider.GenerateResponse(ctx, prompt)
}

func (accessor recoveryChatNoticeAccessor) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return accessor.provider.GenerateStructuredResponse(ctx, request)
}

func (accessor recoveryChatNoticeAccessor) RecoveryChatCompleter() (model.RecoveryChatCompleter, bool) {
	return model.ResolveRecoveryChatCompleter(accessor.provider)
}

func (accessor recoveryChatNoticeAccessor) LocalRecoveryChatCompleter() (model.LocalRecoveryChatCompleter, bool) {
	return model.ResolveLocalRecoveryChatCompleter(accessor.provider)
}

func (provider *recoveryChatNoticeProvider) GenerateResponse(context.Context, string) (string, error) {
	provider.legacyCalls++
	return provider.legacyReply, provider.legacyError
}

func (provider *recoveryChatNoticeProvider) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func (provider *recoveryChatNoticeProvider) GenerateRecoveryResponse(context.Context, string) (string, error) {
	provider.legacyCalls++
	return provider.legacyReply, provider.legacyError
}

func (provider *recoveryChatNoticeProvider) GenerateLocalRecoveryResponse(context.Context, string) (string, error) {
	provider.legacyCalls++
	return provider.legacyReply, provider.legacyError
}

func (provider *recoveryChatNoticeProvider) GenerateRecoveryChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	provider.chatRequests = append(provider.chatRequests, request)
	reply := provider.chatReply
	if provider.chatCalls < len(provider.chatReplies) {
		reply = provider.chatReplies[provider.chatCalls]
	}
	provider.chatCalls++
	finishReason := provider.chatFinishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	return model.ChatCompletionResponse{
		FinishReason:    finishReason,
		SelectedBackend: "remote",
		Message:         model.ChatCompletionMessage{Role: "assistant", Content: reply},
	}, provider.chatError
}

func (provider *recoveryChatNoticeProvider) GenerateLocalRecoveryChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	provider.chatRequests = append(provider.chatRequests, request)
	reply := provider.chatReply
	if provider.chatCalls < len(provider.chatReplies) {
		reply = provider.chatReplies[provider.chatCalls]
	}
	provider.chatCalls++
	finishReason := provider.chatFinishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	return model.ChatCompletionResponse{
		FinishReason:    finishReason,
		SelectedBackend: "device",
		Message:         model.ChatCompletionMessage{Role: "assistant", Content: reply},
	}, provider.chatError
}
