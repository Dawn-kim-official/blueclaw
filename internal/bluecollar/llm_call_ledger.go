package bluecollar

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
)

const llmCallErrorMaximumCharacters = 300
const turnRouterSchemaName = "blueclaw_turn_router"
const agentActionSchemaName = "blueclaw_agent_turn_action"

type llmCallRecord struct {
	Kind                   string                                 `json:"kind"`
	Transport              string                                 `json:"transport,omitempty"`
	SchemaName             string                                 `json:"schemaName,omitempty"`
	Provider               string                                 `json:"provider,omitempty"`
	Model                  string                                 `json:"model,omitempty"`
	ModelTier              string                                 `json:"modelTier,omitempty"`
	SelectedBackend        string                                 `json:"selectedBackend,omitempty"`
	FinishReason           string                                 `json:"finishReason,omitempty"`
	LatencyMS              int64                                  `json:"latencyMs"`
	PromptBytes            int                                    `json:"promptBytes"`
	SchemaBytes            int                                    `json:"schemaBytes,omitempty"`
	ToolCount              int                                    `json:"toolCount,omitempty"`
	ToolBytes              int                                    `json:"toolBytes,omitempty"`
	ContentBytes           int                                    `json:"contentBytes"`
	UsedFallback           bool                                   `json:"usedFallback,omitempty"`
	FallbackReason         string                                 `json:"fallbackReason,omitempty"`
	PromptTokens           int64                                  `json:"promptTokens,omitempty"`
	CompletionTokens       int64                                  `json:"completionTokens,omitempty"`
	TotalTokens            int64                                  `json:"totalTokens,omitempty"`
	CachedPromptTokens     int64                                  `json:"cachedPromptTokens,omitempty"`
	CacheWriteTokens       int64                                  `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens        int64                                  `json:"reasoningTokens,omitempty"`
	CostUSD                float64                                `json:"costUSD,omitempty"`
	UpstreamInferenceCost  float64                                `json:"upstreamInferenceCostUSD,omitempty"`
	IsError                bool                                   `json:"isError,omitempty"`
	Error                  string                                 `json:"error,omitempty"`
	DiagnosticCategory     llm.StructuredOutputDiagnosticCategory `json:"diagnosticCategory,omitempty"`
	DiagnosticFinishReason llm.StructuredOutputFinishReason       `json:"diagnosticFinishReason,omitempty"`
	DiagnosticToolName     string                                 `json:"diagnosticToolName,omitempty"`
	DiagnosticIssues       []llm.StructuredOutputValidationIssue  `json:"diagnosticIssues,omitempty"`
	DiagnosticRepairStatus llm.StructuredOutputRepairStatus       `json:"diagnosticRepairStatus,omitempty"`
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
	if hasRecovery && hasLocalRecovery {
		return observedRecoveryCapabilities{base, observedRecoveryCapability{base}, observedLocalRecoveryCapability{base}}
	}
	if hasRecovery {
		return struct {
			observedLanguageModel
			observedRecoveryCapability
		}{base, observedRecoveryCapability{base}}
	}
	if hasLocalRecovery {
		return struct {
			observedLanguageModel
			observedLocalRecoveryCapability
		}{base, observedLocalRecoveryCapability{base}}
	}
	return base
}

func (model observedLanguageModel) observedInnerProvider() llm.LanguageModelProvider {
	return model.provider
}

func (model observedLanguageModel) TextChatCompleter() (llm.ChatCompleter, bool) {
	completer, isAvailable := llm.ResolveTextChatCompleter(model.provider)
	if !isAvailable {
		return nil, false
	}
	return observedChatCompleter{model: model, delegate: completer}, true
}

func (model observedLanguageModel) RecoveryChatCompleter() (llm.RecoveryChatCompleter, bool) {
	completer, isAvailable := llm.ResolveRecoveryChatCompleter(model.provider)
	if !isAvailable {
		return nil, false
	}
	return observedRecoveryChatCapability{model: model, delegate: completer}, true
}

func (model observedLanguageModel) LocalRecoveryChatCompleter() (llm.LocalRecoveryChatCompleter, bool) {
	completer, isAvailable := llm.ResolveLocalRecoveryChatCompleter(model.provider)
	if !isAvailable {
		return nil, false
	}
	return observedLocalRecoveryChatCapability{model: model, delegate: completer}, true
}

type observedChatCompleter struct {
	model    observedLanguageModel
	delegate llm.ChatCompleter
}

func (completer observedChatCompleter) GenerateChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := completer.delegate.GenerateChatCompletion(ctx, request)
	completer.model.observe(chatCallRecord("chat", request, response, startedAt, errorValue))
	return response, errorValue
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
	promptBytes := structuredRequestByteCount(request)
	schemaBytes := len(request.StructuredOutputSchema.Document)
	record := llmCallRecord{
		Kind:                  "structured",
		Transport:             response.Transport,
		SchemaName:            strings.TrimSpace(request.StructuredOutputSchema.Name),
		Provider:              response.ProviderName,
		Model:                 response.ModelName,
		ModelTier:             response.ModelTier,
		SelectedBackend:       response.SelectedBackend,
		FinishReason:          response.FinishReason,
		LatencyMS:             time.Since(startedAt).Milliseconds(),
		PromptBytes:           promptBytes,
		SchemaBytes:           schemaBytes,
		ContentBytes:          len(response.Content),
		UsedFallback:          response.UsedFallback,
		FallbackReason:        truncateText(compactWhitespace(response.FallbackReason), llmCallErrorMaximumCharacters),
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
		applyLLMCallError(&record, errorValue)
	}
	model.observe(record)
	return response, errorValue
}

type observedRecoveryCapability struct{ model observedLanguageModel }
type observedLocalRecoveryCapability struct{ model observedLanguageModel }
type observedRecoveryChatCapability struct {
	model    observedLanguageModel
	delegate llm.RecoveryChatCompleter
}
type observedLocalRecoveryChatCapability struct {
	model    observedLanguageModel
	delegate llm.LocalRecoveryChatCompleter
}

type observedRecoveryCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
}

func (capability observedRecoveryCapability) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.model.recoveryResponse(ctx, prompt)
}

func (capability observedLocalRecoveryCapability) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.model.localRecoveryResponse(ctx, prompt)
}

func (capability observedRecoveryChatCapability) GenerateRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := capability.delegate.GenerateRecoveryChatCompletion(ctx, request)
	capability.model.observe(chatCallRecord("recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (capability observedLocalRecoveryChatCapability) GenerateLocalRecoveryChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := capability.delegate.GenerateLocalRecoveryChatCompletion(ctx, request)
	capability.model.observe(chatCallRecord("local_recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
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
		applyLLMCallError(&record, errorValue)
	}
	return record
}

func applyLLMCallError(record *llmCallRecord, errorValue error) {
	record.IsError = true
	diagnostic, hasDiagnostic := llm.StructuredOutputDiagnosticFromError(errorValue)
	if !hasDiagnostic {
		record.Error = truncateText(compactWhitespace(errorValue.Error()), llmCallErrorMaximumCharacters)
		return
	}
	record.DiagnosticCategory = diagnostic.Category
	record.DiagnosticFinishReason = diagnostic.FinishReason
	record.DiagnosticToolName = diagnostic.ToolName
	record.DiagnosticIssues = append([]llm.StructuredOutputValidationIssue{}, diagnostic.ValidationIssues...)
	record.DiagnosticRepairStatus = diagnostic.RepairStatus
}

func chatCallRecord(kind string, request llm.ChatCompletionRequest, response llm.ChatCompletionResponse, startedAt time.Time, errorValue error) llmCallRecord {
	record := llmCallRecord{
		Kind:                  kind,
		Transport:             response.Transport,
		SchemaName:            chatRequestSchemaName(request),
		Provider:              response.ProviderName,
		Model:                 response.ModelName,
		ModelTier:             response.ModelTier,
		SelectedBackend:       response.SelectedBackend,
		FinishReason:          response.FinishReason,
		LatencyMS:             time.Since(startedAt).Milliseconds(),
		PromptBytes:           chatRequestByteCount(request),
		ToolCount:             len(request.Tools),
		ToolBytes:             chatRequestToolByteCount(request),
		ContentBytes:          len(response.Message.Content),
		UsedFallback:          response.UsedFallback,
		FallbackReason:        truncateText(compactWhitespace(response.FallbackReason), llmCallErrorMaximumCharacters),
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
		applyLLMCallError(&record, errorValue)
	}
	return record
}

func chatRequestSchemaName(request llm.ChatCompletionRequest) string {
	return strings.TrimSpace(request.SchemaName)
}

func chatRequestByteCount(request llm.ChatCompletionRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
	}
	return byteCount
}

func chatRequestToolByteCount(request llm.ChatCompletionRequest) int {
	byteCount := 0
	for _, tool := range request.Tools {
		document, errorValue := json.Marshal(tool)
		if errorValue != nil {
			continue
		}
		byteCount += len(document)
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
