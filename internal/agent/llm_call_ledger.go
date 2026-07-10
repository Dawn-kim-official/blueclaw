package agent

import (
	"context"
	"strings"
	"time"

	"blueclaw/internal/llm"
)

const llmCallErrorMaximumCharacters = 300

type llmCallRecord struct {
	Kind                  string  `json:"kind"`
	SchemaName            string  `json:"schemaName,omitempty"`
	Provider              string  `json:"provider,omitempty"`
	Model                 string  `json:"model,omitempty"`
	ModelTier             string  `json:"modelTier,omitempty"`
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
	switch {
	case hasRecovery && hasLocalRecovery:
		return observedRecoveryLanguageModel{base}
	case hasRecovery:
		return observedRemoteRecoveryLanguageModel{base}
	case hasLocalRecovery:
		return observedLocalRecoveryLanguageModel{base}
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

type observedRecoveryLanguageModel struct {
	observedLanguageModel
}

func (model observedRecoveryLanguageModel) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return model.recoveryResponse(ctx, prompt)
}

func (model observedRecoveryLanguageModel) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return model.localRecoveryResponse(ctx, prompt)
}

type observedRemoteRecoveryLanguageModel struct {
	observedLanguageModel
}

func (model observedRemoteRecoveryLanguageModel) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return model.recoveryResponse(ctx, prompt)
}

type observedLocalRecoveryLanguageModel struct {
	observedLanguageModel
}

func (model observedLocalRecoveryLanguageModel) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return model.localRecoveryResponse(ctx, prompt)
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
