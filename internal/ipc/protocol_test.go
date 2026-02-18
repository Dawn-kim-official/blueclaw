package ipc

import (
	"encoding/json"
	"testing"

	"github.com/blueclaw/blueclaw/internal/provider"
)

func TestAgentRequestRoundTrip(t *testing.T) {
	original := AgentRequest{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []provider.Message{
			{Role: "user", Content: "hello"},
		},
		Model: "claude-sonnet-4-6",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded AgentRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.SystemPrompt != original.SystemPrompt {
		t.Errorf("SystemPrompt: got %q, want %q", decoded.SystemPrompt, original.SystemPrompt)
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0].Content != "hello" {
		t.Errorf("Messages mismatch: got %v", decoded.Messages)
	}
	if decoded.Model != original.Model {
		t.Errorf("Model: got %q, want %q", decoded.Model, original.Model)
	}
}

func TestAgentResponseRoundTrip(t *testing.T) {
	original := AgentResponse{
		Message: provider.Message{Role: "assistant", Content: "Hello!"},
		ToolCalls: []provider.ToolCall{
			{ID: "call-1", Name: "remember", Arguments: map[string]any{"subject": "test"}},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded AgentResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Message.Content != "Hello!" {
		t.Errorf("Message.Content: got %q, want %q", decoded.Message.Content, "Hello!")
	}
	if len(decoded.ToolCalls) != 1 || decoded.ToolCalls[0].Name != "remember" {
		t.Errorf("ToolCalls mismatch: got %v", decoded.ToolCalls)
	}
}

func TestAgentResponseWithError(t *testing.T) {
	original := AgentResponse{Error: "something went wrong"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded AgentResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Error != "something went wrong" {
		t.Errorf("Error: got %q, want %q", decoded.Error, "something went wrong")
	}
}

func TestStdioOutboundRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		outbound StdioOutbound
	}{
		{
			name: "llm_request",
			outbound: StdioOutbound{
				Type: "llm_request",
				LLMRequest: &provider.Request{
					SystemPrompt: "test",
					Messages:     []provider.Message{{Role: "user", Content: "hi"}},
				},
			},
		},
		{
			name: "schedule_request",
			outbound: StdioOutbound{
				Type:            "schedule_request",
				ScheduleRequest: &ScheduleCreate{CronExpression: "0 9 * * 1", Prompt: "weekly check"},
			},
		},
		{
			name: "done",
			outbound: StdioOutbound{
				Type: "done",
				DoneResponse: &provider.Response{
					Message: provider.Message{Role: "assistant", Content: "done"},
				},
			},
		},
		{
			name:     "error",
			outbound: StdioOutbound{Type: "error", ErrorMessage: "something failed"},
		},
		{
			name:     "notify",
			outbound: StdioOutbound{Type: "notify", Notification: "I'll research Indian culture."},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.outbound)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			var decoded StdioOutbound
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if decoded.Type != test.outbound.Type {
				t.Errorf("Type: got %q, want %q", decoded.Type, test.outbound.Type)
			}
		})
	}
}

func TestStdioInboundRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		inbound StdioInbound
	}{
		{
			name: "llm_response",
			inbound: StdioInbound{
				Type: "llm_response",
				LLMResponse: &provider.Response{
					Message: provider.Message{Role: "assistant", Content: "hello"},
				},
			},
		},
		{
			name:    "schedule_response",
			inbound: StdioInbound{Type: "schedule_response", ScheduleID: "job_123", ScheduleNext: "2026-02-19T09:00:00Z"},
		},
		{
			name:    "error_response",
			inbound: StdioInbound{Type: "llm_response", ErrorMessage: "provider failed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.inbound)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			var decoded StdioInbound
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if decoded.Type != test.inbound.Type {
				t.Errorf("Type: got %q, want %q", decoded.Type, test.inbound.Type)
			}
		})
	}
}
