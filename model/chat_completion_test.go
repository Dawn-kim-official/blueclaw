package model

import (
	"testing"
)

func TestRecoveryChatCompletionTextRequiresUsableAssistantStop(t *testing.T) {
	testCases := []struct {
		name         string
		finishReason string
		message      ChatCompletionMessage
		isValid      bool
	}{
		{name: "stop", finishReason: "stop", message: ChatCompletionMessage{Role: "assistant", Content: "done"}, isValid: true},
		{name: "length", finishReason: "length", message: ChatCompletionMessage{Role: "assistant", Content: "partial"}},
		{name: "content filter", finishReason: "content_filter", message: ChatCompletionMessage{Role: "assistant", Content: "filtered"}},
		{name: "error", finishReason: "error", message: ChatCompletionMessage{Role: "assistant", Content: "failed"}},
		{name: "tool calls", finishReason: "stop", message: ChatCompletionMessage{Role: "assistant", Content: "done", ToolCalls: []ChatCompletionToolCall{{ID: "call-1"}}}},
		{name: "blank", finishReason: "stop", message: ChatCompletionMessage{Role: "assistant", Content: "  "}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			text, errorValue := RecoveryChatCompletionText(ChatCompletionResponse{
				FinishReason: testCase.finishReason,
				Message:      testCase.message,
			})
			if testCase.isValid {
				if errorValue != nil || text != "done" {
					t.Fatalf("expected usable recovery text, got %q and %v", text, errorValue)
				}
				return
			}
			if errorValue == nil {
				t.Fatalf("expected unusable recovery response to fail, got %q", text)
			}
		})
	}
}
