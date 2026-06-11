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
