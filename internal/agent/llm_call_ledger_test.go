package agent

import (
	"context"
	"errors"
	"testing"

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

	recoveryProvider, hasRecoveryChat := observed.(llm.RecoveryChatCompleter)
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
