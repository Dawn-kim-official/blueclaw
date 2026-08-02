package bluecollar

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/model"
)

const llmCallErrorMaximumCharacters = 300
const turnRouterSchemaName = "blueclaw_turn_router"
const agentActionSchemaName = "blueclaw_agent_turn_action"

type llmCallRecord struct {
	Kind                   string                                   `json:"kind"`
	Transport              string                                   `json:"transport,omitempty"`
	SchemaName             string                                   `json:"schemaName,omitempty"`
	Provider               string                                   `json:"provider,omitempty"`
	Model                  string                                   `json:"model,omitempty"`
	ModelTier              string                                   `json:"modelTier,omitempty"`
	SelectedBackend        string                                   `json:"selectedBackend,omitempty"`
	FinishReason           string                                   `json:"finishReason,omitempty"`
	LatencyMS              int64                                    `json:"latencyMs"`
	PromptBytes            int                                      `json:"promptBytes"`
	SchemaBytes            int                                      `json:"schemaBytes,omitempty"`
	ToolCount              int                                      `json:"toolCount,omitempty"`
	ToolBytes              int                                      `json:"toolBytes,omitempty"`
	ContentBytes           int                                      `json:"contentBytes"`
	UsedFallback           bool                                     `json:"usedFallback,omitempty"`
	FallbackReason         string                                   `json:"fallbackReason,omitempty"`
	PromptTokens           int64                                    `json:"promptTokens,omitempty"`
	CompletionTokens       int64                                    `json:"completionTokens,omitempty"`
	TotalTokens            int64                                    `json:"totalTokens,omitempty"`
	CachedPromptTokens     int64                                    `json:"cachedPromptTokens,omitempty"`
	CacheWriteTokens       int64                                    `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens        int64                                    `json:"reasoningTokens,omitempty"`
	CostUSD                float64                                  `json:"costUSD,omitempty"`
	UpstreamInferenceCost  float64                                  `json:"upstreamInferenceCostUSD,omitempty"`
	IsError                bool                                     `json:"isError,omitempty"`
	Error                  string                                   `json:"error,omitempty"`
	DiagnosticCategory     model.StructuredOutputDiagnosticCategory `json:"diagnosticCategory,omitempty"`
	DiagnosticFinishReason model.StructuredOutputFinishReason       `json:"diagnosticFinishReason,omitempty"`
	DiagnosticToolName     string                                   `json:"diagnosticToolName,omitempty"`
	DiagnosticIssues       []model.StructuredOutputValidationIssue  `json:"diagnosticIssues,omitempty"`
	DiagnosticRepairStatus model.StructuredOutputRepairStatus       `json:"diagnosticRepairStatus,omitempty"`
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

func (ledger *turnRouterCallLedger) languageModel(provider model.LanguageModelProvider) model.LanguageModelProvider {
	return observeLanguageModel(provider, ledger.observe)
}

type observedLanguageModel struct {
	provider model.LanguageModelProvider
	observe  llmCallObserver
}

func observeLanguageModel(provider model.LanguageModelProvider, observe llmCallObserver) model.LanguageModelProvider {
	if provider == nil || observe == nil {
		return provider
	}
	if _, isObserved := provider.(interface {
		observedInnerProvider() model.LanguageModelProvider
	}); isObserved {
		return provider
	}
	base := observedLanguageModel{provider: provider, observe: observe}
	_, hasRecovery := provider.(model.RecoveryResponder)
	_, hasLocalRecovery := provider.(model.LocalRecoveryResponder)
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

func (observedModel observedLanguageModel) observedInnerProvider() model.LanguageModelProvider {
	return observedModel.provider
}

func (observedModel observedLanguageModel) TextChatCompleter() (model.ChatCompleter, bool) {
	completer, isAvailable := model.ResolveTextChatCompleter(observedModel.provider)
	if !isAvailable {
		return nil, false
	}
	return observedChatCompleter{observedModel: observedModel, delegate: completer}, true
}

func (observedModel observedLanguageModel) RecoveryChatCompleter() (model.RecoveryChatCompleter, bool) {
	completer, isAvailable := model.ResolveRecoveryChatCompleter(observedModel.provider)
	if !isAvailable {
		return nil, false
	}
	return observedRecoveryChatCapability{observedModel: observedModel, delegate: completer}, true
}

func (observedModel observedLanguageModel) LocalRecoveryChatCompleter() (model.LocalRecoveryChatCompleter, bool) {
	completer, isAvailable := model.ResolveLocalRecoveryChatCompleter(observedModel.provider)
	if !isAvailable {
		return nil, false
	}
	return observedLocalRecoveryChatCapability{observedModel: observedModel, delegate: completer}, true
}

type observedChatCompleter struct {
	observedModel observedLanguageModel
	delegate      model.ChatCompleter
}

func (completer observedChatCompleter) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := completer.delegate.GenerateChatCompletion(ctx, request)
	completer.observedModel.observe(chatCallRecord("chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (observedModel observedLanguageModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	startedAt := time.Now()
	reply, errorValue := observedModel.provider.GenerateResponse(ctx, prompt)
	observedModel.observe(textCallRecord("text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (observedModel observedLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	startedAt := time.Now()
	response, errorValue := observedModel.provider.GenerateStructuredResponse(ctx, request)
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
	observedModel.observe(record)
	return response, errorValue
}

type observedRecoveryCapability struct{ observedModel observedLanguageModel }
type observedLocalRecoveryCapability struct{ observedModel observedLanguageModel }
type observedRecoveryChatCapability struct {
	observedModel observedLanguageModel
	delegate      model.RecoveryChatCompleter
}
type observedLocalRecoveryChatCapability struct {
	observedModel observedLanguageModel
	delegate      model.LocalRecoveryChatCompleter
}

type observedRecoveryCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
}

func (capability observedRecoveryCapability) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.observedModel.recoveryResponse(ctx, prompt)
}

func (capability observedLocalRecoveryCapability) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.observedModel.localRecoveryResponse(ctx, prompt)
}

func (capability observedRecoveryChatCapability) GenerateRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := capability.delegate.GenerateRecoveryChatCompletion(ctx, request)
	capability.observedModel.observe(chatCallRecord("recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (capability observedLocalRecoveryChatCapability) GenerateLocalRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := capability.delegate.GenerateLocalRecoveryChatCompletion(ctx, request)
	capability.observedModel.observe(chatCallRecord("local_recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (observedModel observedLanguageModel) recoveryResponse(ctx context.Context, prompt string) (string, error) {
	recoveryProvider, isRecoveryProvider := observedModel.provider.(model.RecoveryResponder)
	if !isRecoveryProvider {
		return observedModel.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := recoveryProvider.GenerateRecoveryResponse(ctx, prompt)
	observedModel.observe(textCallRecord("recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (observedModel observedLanguageModel) localRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	localRecoveryProvider, isLocalRecoveryProvider := observedModel.provider.(model.LocalRecoveryResponder)
	if !isLocalRecoveryProvider {
		return observedModel.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := localRecoveryProvider.GenerateLocalRecoveryResponse(ctx, prompt)
	observedModel.observe(textCallRecord("local_recovery_text", prompt, reply, startedAt, errorValue))
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
	diagnostic, hasDiagnostic := model.StructuredOutputDiagnosticFromError(errorValue)
	if !hasDiagnostic {
		record.Error = truncateText(compactWhitespace(errorValue.Error()), llmCallErrorMaximumCharacters)
		return
	}
	record.DiagnosticCategory = diagnostic.Category
	record.DiagnosticFinishReason = diagnostic.FinishReason
	record.DiagnosticToolName = diagnostic.ToolName
	record.DiagnosticIssues = append([]model.StructuredOutputValidationIssue{}, diagnostic.ValidationIssues...)
	record.DiagnosticRepairStatus = diagnostic.RepairStatus
}

func chatCallRecord(kind string, request model.ChatCompletionRequest, response model.ChatCompletionResponse, startedAt time.Time, errorValue error) llmCallRecord {
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

func chatRequestSchemaName(request model.ChatCompletionRequest) string {
	return strings.TrimSpace(request.SchemaName)
}

func chatRequestByteCount(request model.ChatCompletionRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
	}
	return byteCount
}

func chatRequestToolByteCount(request model.ChatCompletionRequest) int {
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

func structuredRequestByteCount(request model.StructuredResponseRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += len(message.Content)
		for _, part := range message.Parts {
			byteCount += len(part.Text) + len(part.DataBase64)
		}
	}
	return byteCount
}
