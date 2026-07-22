package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
)

func TestObserveLanguageModelRecordsStructuredCalls(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(staticReplyProvider{content: `{"reply":"ok"}`}, func(record llmCallRecord) {
		records = append(records, record)
	})

	_, errorValue := observed.GenerateStructuredResponse(context.Background(), llm.StructuredResponseRequest{
		Messages:               []llm.Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: llm.StructuredOutputSchema{Name: "blueclaw_agent_turn_action"},
	})
	if errorValue != nil {
		t.Fatalf("expected structured call: %v", errorValue)
	}
	if len(records) != 1 {
		t.Fatalf("expected one call record, got %d", len(records))
	}
	if records[0].Kind != "structured" || records[0].SchemaName != "blueclaw_agent_turn_action" {
		t.Fatalf("expected structured record with schema, got %+v", records[0])
	}
	if records[0].PromptBytes != len("hello") || records[0].ContentBytes == 0 {
		t.Fatalf("expected byte counts, got %+v", records[0])
	}
}

func TestTurnRouterCallLedgerPreservesMissingModelTier(t *testing.T) {
	ledger := &turnRouterCallLedger{}
	ledger.observe(llmCallRecord{SchemaName: turnRouterSchemaName})

	if len(ledger.records) != 1 || ledger.records[0].IsError || ledger.records[0].ModelTier != "" {
		t.Fatalf("expected missing router tier to remain observational, got %+v", ledger.records)
	}
}

func TestObserveLanguageModelRecordsSafeLLMDDiagnosticsAndRequestSizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"structured_output_invalid","allowLegacyFallback":false,"message":"PRIVATE_GENERATED_CONTENT","diagnostic":{"category":"finish_reason","finishReason":"length"}}}`))
	}))
	defer server.Close()
	records := []llmCallRecord{}
	client := llm.NewLLMDClient(llm.LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key"})
	observed := observeLanguageModel(client, func(record llmCallRecord) {
		records = append(records, record)
	})
	request := llm.StructuredResponseRequest{
		Messages:               []llm.Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: llm.StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	}

	_, errorValue := observed.GenerateStructuredResponse(context.Background(), request)
	if errorValue == nil {
		t.Fatal("expected LLMD structured error")
	}
	if len(records) != 1 {
		t.Fatalf("expected one call record, got %+v", records)
	}
	record := records[0]
	if record.DiagnosticCategory != "finish_reason" || record.DiagnosticFinishReason != "length" || record.Error != "" {
		t.Fatalf("expected content-free diagnostic fields, got %+v", record)
	}
	if record.PromptBytes != len("hello") || record.SchemaBytes != len(request.StructuredOutputSchema.Document) {
		t.Fatalf("expected prompt and schema sizes, got %+v", record)
	}
}

func TestObserveLanguageModelRecordsSafeChatToolDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusBadGateway)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_response_invalid","allowLegacyFallback":false,"message":"PRIVATE_GENERATED_CONTENT","diagnostic":{"category":"schema_validation","toolName":"task.add","validationIssues":[{"fieldPath":"/prompt","code":"required"}],"repairStatus":"failed"}}}`))
	}))
	defer server.Close()
	records := []llmCallRecord{}
	client := llm.NewLLMDClient(llm.LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key"})
	observed := observeLanguageModel(client, func(record llmCallRecord) {
		records = append(records, record)
	})
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected observed LLMD Chat capability")
	}

	_, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), nativeActionChatRequest())

	if errorValue == nil || len(records) != 1 {
		t.Fatalf("expected one failed Chat record, error=%v records=%+v", errorValue, records)
	}
	record := records[0]
	if record.DiagnosticCategory != llm.StructuredOutputDiagnosticSchemaValidation ||
		record.DiagnosticToolName != "task.add" ||
		record.DiagnosticRepairStatus != llm.StructuredOutputRepairFailed ||
		len(record.DiagnosticIssues) != 1 ||
		record.DiagnosticIssues[0].FieldPath != "/prompt" ||
		record.DiagnosticIssues[0].Code != llm.StructuredOutputValidationRequired ||
		record.Error != "" {
		t.Fatalf("expected content-free Chat diagnostic fields, got %+v", record)
	}
	document, _ := json.Marshal(record)
	if strings.Contains(string(document), "PRIVATE_GENERATED_CONTENT") {
		t.Fatalf("expected generated content to stay out of the ledger, got %s", document)
	}
}

func TestObserveLanguageModelRecordsExplicitChatSchemaOnly(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(recoveryCapableTestModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected observed chat capability")
	}
	if _, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), nativeActionChatRequest()); errorValue != nil {
		t.Fatalf("expected action chat completion: %v", errorValue)
	}
	if _, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{}); errorValue != nil {
		t.Fatalf("expected plain chat completion: %v", errorValue)
	}
	if len(records) != 2 {
		t.Fatalf("expected action and plain chat records, got %+v", records)
	}
	if records[0].SchemaName != "blueclaw_agent_turn_action" || records[1].SchemaName != "" {
		t.Fatalf("expected only explicitly identified action chat to carry schema, got %+v", records)
	}
}

func nativeActionChatRequest() llm.ChatCompletionRequest {
	return llm.ChatCompletionRequest{
		SchemaName: agentActionSchemaName,
		Tools: []llm.ChatCompletionTool{{
			Type:     "function",
			Function: llm.ChatCompletionFunction{Name: "blueclaw_agent_turn_action"},
		}},
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"blueclaw_agent_turn_action"}}`),
	}
}

func TestChatCallRecordPreservesActionRoutingMetadata(t *testing.T) {
	record := chatCallRecord("chat", nativeActionChatRequest(), llm.ChatCompletionResponse{
		ProviderName:    "llmd",
		ModelName:       "low-model",
		ModelTier:       "low",
		SelectedBackend: "device",
		FinishReason:    "tool_calls",
		UsedFallback:    true,
	}, time.Now(), nil)
	if record.SchemaName != "blueclaw_agent_turn_action" || record.Provider != "llmd" || record.Model != "low-model" || record.ModelTier != "low" || record.SelectedBackend != "device" || record.FinishReason != "tool_calls" || !record.UsedFallback {
		t.Fatalf("expected action routing metadata, got %+v", record)
	}
}

func TestObserveLanguageModelRecordsTokenCounts(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(tokenReportingProvider{}, func(record llmCallRecord) {
		records = append(records, record)
	})

	_, errorValue := observed.GenerateStructuredResponse(context.Background(), llm.StructuredResponseRequest{
		Messages:               []llm.Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: llm.StructuredOutputSchema{Name: "test_schema"},
	})
	if errorValue != nil {
		t.Fatalf("expected structured call: %v", errorValue)
	}
	if len(records) != 1 {
		t.Fatalf("expected one call record, got %d", len(records))
	}
	if records[0].PromptTokens != 10 {
		t.Fatalf("expected prompt tokens 10, got %d", records[0].PromptTokens)
	}
	if records[0].CompletionTokens != 5 {
		t.Fatalf("expected completion tokens 5, got %d", records[0].CompletionTokens)
	}
	if records[0].TotalTokens != 15 {
		t.Fatalf("expected total tokens 15, got %d", records[0].TotalTokens)
	}
}

type tokenReportingProvider struct{}

func (tokenReportingProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (tokenReportingProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{
		Content: "{}",
		Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func TestObserveLanguageModelRecordsErrors(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(failingLanguageModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})

	_, errorValue := observed.GenerateResponse(context.Background(), "prompt")
	if errorValue == nil {
		t.Fatal("expected text call error")
	}
	if len(records) != 1 || !records[0].IsError || records[0].Error != "model failed" {
		t.Fatalf("expected error record, got %+v", records)
	}
}

func TestObserveLanguageModelProvidesObservedChatCapability(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(recoveryCapableTestModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})
	if _, isDirectChat := observed.(llm.ChatCompleter); isDirectChat {
		t.Fatal("expected observer to expose ChatCompleter only through the optional accessor")
	}
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected observed chat capability")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{
		Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "reply"}},
	})
	if errorValue != nil || response.Message.Content != "chat reply" {
		t.Fatalf("expected chat response, got %+v %v", response, errorValue)
	}
	if len(records) != 1 || records[0].Kind != "chat" || records[0].Provider != "chat-provider" || records[0].Model != "chat-model" || records[0].SelectedBackend != "remote" || records[0].FinishReason != "stop" || records[0].UsedFallback {
		t.Fatalf("expected exact chat metadata, got %+v", records)
	}
}

func TestObserveLanguageModelResolvesNestedChatAccessors(t *testing.T) {
	inner := nestedChatAccessorTestModel{provider: recoveryCapableTestModel{}}
	observed := observeLanguageModel(inner, func(llmCallRecord) {})
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected nested observed chat capability")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "chat reply" {
		t.Fatalf("expected nested chat response, got %+v %v", response, errorValue)
	}
}

func TestObserveLanguageModelRecordsChatErrorsAndMetadata(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(chatErrorTestModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected observed chat capability")
	}
	_, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{
		Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "reply"}},
	})
	if errorValue == nil {
		t.Fatal("expected chat error")
	}
	if len(records) != 1 || !records[0].IsError || records[0].Error != "chat failed" || records[0].Provider != "chat-provider" || records[0].Model != "chat-model" || records[0].SelectedBackend != "device" || records[0].FinishReason != "error" || !records[0].UsedFallback {
		t.Fatalf("expected exact chat error metadata, got %+v", records)
	}
}

func TestObserveLanguageModelRecordsRecoveryChatErrorsAndMetadata(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(chatErrorTestModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})
	recoveryProvider, isAvailable := llm.ResolveRecoveryChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected observed recovery chat capability")
	}
	_, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(context.Background(), llm.ChatCompletionRequest{})
	if errorValue == nil {
		t.Fatal("expected recovery chat error")
	}
	if len(records) != 1 || records[0].Kind != "recovery_chat" || !records[0].IsError || records[0].Error != "recovery chat failed" || records[0].Provider != "recovery-provider" || records[0].Model != "recovery-model" || records[0].SelectedBackend != "remote" || records[0].FinishReason != "error" || !records[0].UsedFallback {
		t.Fatalf("expected exact recovery chat error metadata, got %+v", records)
	}
}

func TestObserveLanguageModelPreservesMissingRecoveryCapability(t *testing.T) {
	observed := observeLanguageModel(staticReplyProvider{content: "ok"}, func(llmCallRecord) {})

	if _, hasRecovery := observed.(llm.RecoveryResponder); hasRecovery {
		t.Fatal("expected wrapper without recovery capability for plain provider")
	}
	if _, hasLocalRecovery := observed.(llm.LocalRecoveryResponder); hasLocalRecovery {
		t.Fatal("expected wrapper without local recovery capability for plain provider")
	}
	if _, hasRecoveryChat := observed.(llm.RecoveryChatCompleter); hasRecoveryChat {
		t.Fatal("expected wrapper without recovery chat capability for plain provider")
	}
	if _, hasLocalRecoveryChat := observed.(llm.LocalRecoveryChatCompleter); hasLocalRecoveryChat {
		t.Fatal("expected wrapper without local recovery chat capability for plain provider")
	}
	if _, isAvailable := llm.ResolveRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected recovery chat resolver to report unavailable")
	}
	if _, isAvailable := llm.ResolveLocalRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected local recovery chat resolver to report unavailable")
	}
}

type legacyRecoveryTestModel struct{ staticReplyProvider }

func (legacyRecoveryTestModel) GenerateRecoveryResponse(context.Context, string) (string, error) {
	return "recovered", nil
}

func (legacyRecoveryTestModel) GenerateLocalRecoveryResponse(context.Context, string) (string, error) {
	return "locally recovered", nil
}

func TestObserveLanguageModelDoesNotInventChatCapability(t *testing.T) {
	observed := observeLanguageModel(legacyRecoveryTestModel{}, func(llmCallRecord) {})

	if _, hasRecovery := observed.(llm.RecoveryResponder); !hasRecovery {
		t.Fatal("expected recovery capability to be preserved")
	}
	if _, hasLocalRecovery := observed.(llm.LocalRecoveryResponder); !hasLocalRecovery {
		t.Fatal("expected local recovery capability to be preserved")
	}
	if _, hasRecoveryChat := observed.(llm.RecoveryChatCompleter); hasRecoveryChat {
		t.Fatal("expected wrapper not to invent recovery chat capability")
	}
	if _, hasLocalRecoveryChat := observed.(llm.LocalRecoveryChatCompleter); hasLocalRecoveryChat {
		t.Fatal("expected wrapper not to invent local recovery chat capability")
	}
	if _, isAvailable := llm.ResolveRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected recovery chat resolver to report unavailable")
	}
	if _, isAvailable := llm.ResolveLocalRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected local recovery chat resolver to report unavailable")
	}
}

type recoveryCapableTestModel struct {
	staticReplyProvider
}

func (recoveryCapableTestModel) GenerateRecoveryResponse(context.Context, string) (string, error) {
	return "recovered", nil
}

func (recoveryCapableTestModel) GenerateLocalRecoveryResponse(context.Context, string) (string, error) {
	return "", errors.New("local recovery unavailable")
}

func (recoveryCapableTestModel) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "chat-provider",
		ModelName:       "chat-model",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "chat recovered"},
		Usage:           llm.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
	}, nil
}

func (recoveryCapableTestModel) GenerateLocalRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "local-chat-provider",
		ModelName:       "local-chat-model",
		SelectedBackend: "device",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "local chat recovered"},
	}, nil
}

func (recoveryCapableTestModel) GenerateChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "chat-provider",
		ModelName:       "chat-model",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "chat reply"},
	}, nil
}

type chatErrorTestModel struct{}

func (chatErrorTestModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (chatErrorTestModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

func (chatErrorTestModel) GenerateChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "error",
		ProviderName:    "chat-provider",
		ModelName:       "chat-model",
		SelectedBackend: "device",
		UsedFallback:    true,
	}, errors.New("chat failed")
}

func (chatErrorTestModel) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "error",
		ProviderName:    "recovery-provider",
		ModelName:       "recovery-model",
		SelectedBackend: "remote",
		UsedFallback:    true,
	}, errors.New("recovery chat failed")
}

type nestedChatAccessorTestModel struct {
	provider llm.LanguageModelProvider
}

func (model nestedChatAccessorTestModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	return model.provider.GenerateResponse(ctx, prompt)
}

func (model nestedChatAccessorTestModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return model.provider.GenerateStructuredResponse(ctx, request)
}

func (model nestedChatAccessorTestModel) TextChatCompleter() (llm.ChatCompleter, bool) {
	return llm.ResolveTextChatCompleter(model.provider)
}

func TestObserveLanguageModelKeepsRecoveryCapabilityAndRecords(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(recoveryCapableTestModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})

	recoveryProvider, hasRecovery := observed.(llm.RecoveryResponder)
	if !hasRecovery {
		t.Fatal("expected recovery capability to be preserved")
	}
	reply, errorValue := recoveryProvider.GenerateRecoveryResponse(context.Background(), "prompt")
	if errorValue != nil || reply != "recovered" {
		t.Fatalf("expected recovery passthrough, got %q %v", reply, errorValue)
	}
	if len(records) != 1 || records[0].Kind != "recovery_text" {
		t.Fatalf("expected recovery call record, got %+v", records)
	}
	rewrapped := observeLanguageModel(observed, func(llmCallRecord) {
		t.Fatal("expected double wrapping to be skipped")
	})
	if _, errorValue := rewrapped.GenerateResponse(context.Background(), "prompt"); errorValue != nil {
		t.Fatalf("expected rewrapped call to pass through: %v", errorValue)
	}
	if len(records) != 2 {
		t.Fatalf("expected original observer to keep recording, got %d records", len(records))
	}
}

func TestObserveLanguageModelKeepsRecoveryChatCapabilityAndRecords(t *testing.T) {
	records := []llmCallRecord{}
	observed := observeLanguageModel(recoveryCapableTestModel{}, func(record llmCallRecord) {
		records = append(records, record)
	})

	recoveryProvider, hasRecoveryChat := llm.ResolveRecoveryChatCompleter(observed)
	if !hasRecoveryChat {
		t.Fatal("expected recovery chat capability to be preserved")
	}
	response, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(context.Background(), llm.ChatCompletionRequest{
		Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "prompt"}},
	})
	if errorValue != nil || response.Message.Content != "chat recovered" {
		t.Fatalf("expected recovery chat passthrough, got %+v %v", response, errorValue)
	}
	if len(records) != 1 || records[0].Kind != "recovery_chat" || records[0].Provider != "chat-provider" || records[0].PromptBytes != len("prompt") {
		t.Fatalf("expected recovery chat call record, got %+v", records)
	}
	if records[0].SelectedBackend != "remote" || records[0].FinishReason != "stop" {
		t.Fatalf("expected recovery routing metadata, got %+v", records[0])
	}
}

func TestObserveLanguageModelPreservesNestedRecoveryChatCapabilities(t *testing.T) {
	innerRecords := []llmCallRecord{}
	outerRecords := []llmCallRecord{}
	inner := observeLanguageModel(recoveryCapableTestModel{}, func(record llmCallRecord) {
		innerRecords = append(innerRecords, record)
	})
	outer := observedLanguageModel{provider: inner, observe: func(record llmCallRecord) {
		outerRecords = append(outerRecords, record)
	}}

	recoveryProvider, hasRecoveryChat := llm.ResolveRecoveryChatCompleter(outer)
	if !hasRecoveryChat {
		t.Fatal("expected nested recovery chat capability")
	}
	localRecoveryProvider, hasLocalRecoveryChat := llm.ResolveLocalRecoveryChatCompleter(outer)
	if !hasLocalRecoveryChat {
		t.Fatal("expected nested local recovery chat capability")
	}
	request := llm.ChatCompletionRequest{Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "prompt"}}}
	if _, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(context.Background(), request); errorValue != nil {
		t.Fatalf("expected nested recovery chat response: %v", errorValue)
	}
	if _, errorValue := localRecoveryProvider.GenerateLocalRecoveryChatCompletion(context.Background(), request); errorValue != nil {
		t.Fatalf("expected nested local recovery chat response: %v", errorValue)
	}
	assertRecoveryChatRecords(t, innerRecords, "nested inner")
	assertRecoveryChatRecords(t, outerRecords, "nested outer")
}

func assertRecoveryChatRecords(t *testing.T, records []llmCallRecord, label string) {
	t.Helper()
	if len(records) != 2 {
		t.Fatalf("expected two %s recovery chat records, got %+v", label, records)
	}
	for _, record := range records {
		if record.Kind != "recovery_chat" && record.Kind != "local_recovery_chat" {
			t.Fatalf("expected %s recovery chat record, got %+v", label, record)
		}
		if record.Provider == "" || record.Model == "" || record.SelectedBackend == "" || record.FinishReason == "" {
			t.Fatalf("expected %s routing metadata, got %+v", label, record)
		}
	}
}

func TestChatCallRecordCountsNativeToolDefinitionBytes(t *testing.T) {
	request := llm.ChatCompletionRequest{
		Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "hello"}},
		Tools: []llm.ChatCompletionTool{
			{Type: "function", Function: llm.ChatCompletionFunction{Name: "calendar.update", Parameters: json.RawMessage(`{"type":"object"}`)}},
			{Type: "function", Function: llm.ChatCompletionFunction{Name: "task.add", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
	}

	record := chatCallRecord("chat", request, llm.ChatCompletionResponse{}, time.Now(), nil)

	if record.ToolCount != 2 {
		t.Fatalf("expected two native tools recorded, got %d", record.ToolCount)
	}
	if record.ToolBytes <= 0 {
		t.Fatalf("expected positive tool definition bytes, got %d", record.ToolBytes)
	}
}
