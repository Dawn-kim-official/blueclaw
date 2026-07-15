package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"blueclaw/internal/llm"
)

const llmCallErrorMaximumCharacters = 300
const turnRouterSchemaName = "blueclaw_turn_router"

type llmCallRecord struct {
	Kind                  string  `json:"kind"`
	SchemaName            string  `json:"schemaName,omitempty"`
	Provider              string  `json:"provider,omitempty"`
	Model                 string  `json:"model,omitempty"`
	ModelTier             string  `json:"modelTier,omitempty"`
	SelectedBackend       string  `json:"selectedBackend,omitempty"`
	FinishReason          string  `json:"finishReason,omitempty"`
	LatencyMS             int64   `json:"latencyMs"`
	PromptBytes           int     `json:"promptBytes"`
	ContentBytes          int     `json:"contentBytes"`
	UsedFallback          bool    `json:"usedFallback,omitempty"`
	PromptTokens          int64   `json:"promptTokens,omitempty"`
	CompletionTokens      int64   `json:"completionTokens,omitempty"`
	TotalTokens           int64   `json:"totalTokens,omitempty"`
	CachedPromptTokens    int64   `json:"cachedPromptTokens,omitempty"`
	CacheWriteTokens      int64   `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens       int64   `json:"reasoningTokens,omitempty"`
	CostUSD               float64 `json:"costUSD,omitempty"`
	UpstreamInferenceCost float64 `json:"upstreamInferenceCostUSD,omitempty"`
	IsError               bool    `json:"isError,omitempty"`
	Error                 string  `json:"error,omitempty"`
}

type llmCallObserver func(record llmCallRecord)

type turnRouterCallLedger struct {
	records []llmCallRecord
}

func (ledger *turnRouterCallLedger) observe(record llmCallRecord) {
	if record.SchemaName != turnRouterSchemaName {
		return
	}
	ledger.records = append(ledger.records, record)
}

func (ledger *turnRouterCallLedger) languageModel(provider llm.LanguageModelProvider) llm.LanguageModelProvider {
	return observeLanguageModel(provider, ledger.observe)
}

type observedLanguageModel struct {
	provider llm.LanguageModelProvider
	observe  llmCallObserver
}

func observeLanguageModel(provider llm.LanguageModelProvider, observe llmCallObserver) llm.LanguageModelProvider {
	if provider == nil || observe == nil {
		return provider
	}
	if _, isObserved := provider.(interface {
		observedInnerProvider() llm.LanguageModelProvider
	}); isObserved {
		return provider
	}
	base := observedLanguageModel{provider: provider, observe: observe}
	_, hasRecovery := provider.(llm.RecoveryResponder)
	_, hasLocalRecovery := provider.(llm.LocalRecoveryResponder)
	_, hasRecoveryChat := provider.(llm.RecoveryChatCompleter)
	_, hasLocalRecoveryChat := provider.(llm.LocalRecoveryChatCompleter)
	switch {
	case hasRecovery && hasLocalRecovery && hasRecoveryChat && hasLocalRecoveryChat:
		return observedAllRecoveryCapabilities{
			base,
			observedRecoveryCapability{base},
			observedLocalRecoveryCapability{base},
			observedRecoveryChatCapability{base},
			observedLocalRecoveryChatCapability{base},
		}
	case hasRecovery && hasLocalRecovery && hasRecoveryChat:
		return observedRecoveryAndLocalAndChatCapabilities{
			base,
			observedRecoveryCapability{base},
			observedLocalRecoveryCapability{base},
			observedRecoveryChatCapability{base},
		}
	case hasRecovery && hasLocalRecovery && hasLocalRecoveryChat:
		return observedRecoveryAndLocalAndLocalChatCapabilities{
			base,
			observedRecoveryCapability{base},
			observedLocalRecoveryCapability{base},
			observedLocalRecoveryChatCapability{base},
		}
	case hasRecovery && hasRecoveryChat && hasLocalRecoveryChat:
		return observedRecoveryAndChatCapabilities{
			base,
			observedRecoveryCapability{base},
			observedRecoveryChatCapability{base},
			observedLocalRecoveryChatCapability{base},
		}
	case hasLocalRecovery && hasRecoveryChat && hasLocalRecoveryChat:
		return observedLocalAndChatCapabilities{
			base,
			observedLocalRecoveryCapability{base},
			observedRecoveryChatCapability{base},
			observedLocalRecoveryChatCapability{base},
		}
	case hasRecovery && hasLocalRecovery:
		return observedRecoveryCapabilities{base, observedRecoveryCapability{base}, observedLocalRecoveryCapability{base}}
	case hasRecoveryChat && hasLocalRecoveryChat:
		return observedChatCapabilities{base, observedRecoveryChatCapability{base}, observedLocalRecoveryChatCapability{base}}
	case hasRecovery && hasRecoveryChat:
		return observedRemoteRecoveryCapabilities{base, observedRecoveryCapability{base}, observedRecoveryChatCapability{base}}
	case hasRecovery && hasLocalRecoveryChat:
		return observedRecoveryAndLocalChatCapabilities{base, observedRecoveryCapability{base}, observedLocalRecoveryChatCapability{base}}
	case hasLocalRecovery && hasRecoveryChat:
		return observedLocalAndRecoveryChatCapabilities{base, observedLocalRecoveryCapability{base}, observedRecoveryChatCapability{base}}
	case hasLocalRecovery && hasLocalRecoveryChat:
		return observedLocalRecoveryCapabilities{base, observedLocalRecoveryCapability{base}, observedLocalRecoveryChatCapability{base}}
	case hasRecovery:
		return struct {
			observedLanguageModel
			observedRecoveryCapability
		}{base, observedRecoveryCapability{base}}
	case hasLocalRecovery:
		return struct {
			observedLanguageModel
			observedLocalRecoveryCapability
		}{base, observedLocalRecoveryCapability{base}}
	case hasRecoveryChat:
		return struct {
			observedLanguageModel
			observedRecoveryChatCapability
		}{base, observedRecoveryChatCapability{base}}
	case hasLocalRecoveryChat:
		return struct {
			observedLanguageModel
			observedLocalRecoveryChatCapability
		}{base, observedLocalRecoveryChatCapability{base}}
	default:
		return base
	}
}

func (model observedLanguageModel) observedInnerProvider() llm.LanguageModelProvider {
	return model.provider
}

func (model observedLanguageModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	startedAt := time.Now()
	reply, errorValue := model.provider.GenerateResponse(ctx, prompt)
	model.observe(textCallRecord("text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (model observedLanguageModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	startedAt := time.Now()
	response, errorValue := model.provider.GenerateStructuredResponse(ctx, request)
	record := llmCallRecord{
		Kind:                  "structured",
		SchemaName:            strings.TrimSpace(request.StructuredOutputSchema.Name),
		Provider:              response.ProviderName,
		Model:                 response.ModelName,
		SelectedBackend:       response.SelectedBackend,
		FinishReason:          response.FinishReason,
		LatencyMS:             time.Since(startedAt).Milliseconds(),
		PromptBytes:           structuredRequestByteCount(request),
		ContentBytes:          len(response.Content),
		UsedFallback:          response.UsedFallback,
		PromptTokens:          response.Usage.PromptTokens,
		CompletionTokens:      response.Usage.CompletionTokens,
		TotalTokens:           response.Usage.TotalTokens,
		CachedPromptTokens:    response.Usage.CachedPromptTokens,
		CacheWriteTokens:      response.Usage.CacheWriteTokens,
		ReasoningTokens:       response.Usage.ReasoningTokens,
		CostUSD:               response.Usage.CostUSD,
		UpstreamInferenceCost: response.Usage.UpstreamInferenceCost,
	}
	if errorValue != nil {
		record.IsError = true
		record.Error = truncateText(compactWhitespace(errorValue.Error()), llmCallErrorMaximumCharacters)
	}
	model.observe(record)
	return response, errorValue
}

type observedRecoveryCapability struct{ model observedLanguageModel }
type observedLocalRecoveryCapability struct{ model observedLanguageModel }
type observedRecoveryChatCapability struct{ model observedLanguageModel }
type observedLocalRecoveryChatCapability struct{ model observedLanguageModel }

type observedAllRecoveryCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
	observedRecoveryChatCapability
	observedLocalRecoveryChatCapability
}

type observedRecoveryAndLocalAndChatCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
	observedRecoveryChatCapability
}

type observedRecoveryAndLocalAndLocalChatCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
	observedLocalRecoveryChatCapability
}

type observedRecoveryAndChatCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedRecoveryChatCapability
	observedLocalRecoveryChatCapability
}

type observedLocalAndChatCapabilities struct {
	observedLanguageModel
	observedLocalRecoveryCapability
	observedRecoveryChatCapability
	observedLocalRecoveryChatCapability
}

type observedRecoveryCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
}

type observedChatCapabilities struct {
	observedLanguageModel
	observedRecoveryChatCapability
	observedLocalRecoveryChatCapability
}

type observedRemoteRecoveryCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedRecoveryChatCapability
}

type observedRecoveryAndLocalChatCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryChatCapability
}

type observedLocalAndRecoveryChatCapabilities struct {
	observedLanguageModel
	observedLocalRecoveryCapability
	observedRecoveryChatCapability
}

type observedLocalRecoveryCapabilities struct {
	observedLanguageModel
	observedLocalRecoveryCapability
	observedLocalRecoveryChatCapability
}

func (capability observedRecoveryCapability) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.model.recoveryResponse(ctx, prompt)
}

func (capability observedLocalRecoveryCapability) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.model.localRecoveryResponse(ctx, prompt)
}

func (capability observedRecoveryChatCapability) GenerateRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return capability.model.recoveryChatCompletion(ctx, request)
}

func (capability observedLocalRecoveryChatCapability) GenerateLocalRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return capability.model.localRecoveryChatCompletion(ctx, request)
}

func (model observedLanguageModel) recoveryResponse(ctx context.Context, prompt string) (string, error) {
	recoveryProvider, isRecoveryProvider := model.provider.(llm.RecoveryResponder)
	if !isRecoveryProvider {
		return model.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := recoveryProvider.GenerateRecoveryResponse(ctx, prompt)
	model.observe(textCallRecord("recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (model observedLanguageModel) localRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	localRecoveryProvider, isLocalRecoveryProvider := model.provider.(llm.LocalRecoveryResponder)
	if !isLocalRecoveryProvider {
		return model.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := localRecoveryProvider.GenerateLocalRecoveryResponse(ctx, prompt)
	model.observe(textCallRecord("local_recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (model observedLanguageModel) recoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	recoveryProvider, isRecoveryProvider := model.provider.(llm.RecoveryChatCompleter)
	if !isRecoveryProvider {
		return llm.ChatCompletionResponse{}, errors.New("recovery chat provider unavailable")
	}
	startedAt := time.Now()
	response, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(ctx, request)
	model.observe(chatCallRecord("recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (model observedLanguageModel) localRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	localRecoveryProvider, isLocalRecoveryProvider := model.provider.(llm.LocalRecoveryChatCompleter)
	if !isLocalRecoveryProvider {
		return llm.ChatCompletionResponse{}, errors.New("local recovery chat provider unavailable")
	}
	startedAt := time.Now()
	response, errorValue := localRecoveryProvider.GenerateLocalRecoveryChatCompletion(ctx, request)
	model.observe(chatCallRecord("local_recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func textCallRecord(kind string, prompt string, reply string, startedAt time.Time, errorValue error) llmCallRecord {
	record := llmCallRecord{
		Kind:         kind,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		PromptBytes:  len(prompt),
		ContentBytes: len(reply),
	}
	if errorValue != nil {
		record.IsError = true
		record.Error = truncateText(compactWhitespace(errorValue.Error()), llmCallErrorMaximumCharacters)
	}
	return record
}

func chatCallRecord(kind string, request llm.ChatCompletionRequest, response llm.ChatCompletionResponse, startedAt time.Time, errorValue error) llmCallRecord {
	record := llmCallRecord{
		Kind:                  kind,
		Provider:              response.ProviderName,
		Model:                 response.ModelName,
		SelectedBackend:       response.SelectedBackend,
		FinishReason:          response.FinishReason,
		LatencyMS:             time.Since(startedAt).Milliseconds(),
		PromptBytes:           chatRequestByteCount(request),
		ContentBytes:          len(response.Message.Content),
		UsedFallback:          response.UsedFallback,
		PromptTokens:          response.Usage.PromptTokens,
		CompletionTokens:      response.Usage.CompletionTokens,
		TotalTokens:           response.Usage.TotalTokens,
		CachedPromptTokens:    response.Usage.CachedPromptTokens,
		CacheWriteTokens:      response.Usage.CacheWriteTokens,
		ReasoningTokens:       response.Usage.ReasoningTokens,
		CostUSD:               response.Usage.CostUSD,
		UpstreamInferenceCost: response.Usage.UpstreamInferenceCost,
	}
	if errorValue != nil {
		record.IsError = true
		record.Error = truncateText(compactWhitespace(errorValue.Error()), llmCallErrorMaximumCharacters)
	}
	return record
}

func chatRequestByteCount(request llm.ChatCompletionRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
	}
	return byteCount
}

func structuredRequestByteCount(request llm.StructuredResponseRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
		for _, part := range message.Parts {
			byteCount += len(part.Text) + len(part.DataBase64)
		}
	}
	return byteCount
}
