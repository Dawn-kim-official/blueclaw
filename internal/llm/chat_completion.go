package llm

import (
	"context"
	"encoding/json"
)

type ChatCompletionRequest struct {
	Messages          []ChatCompletionMessage `json:"messages"`
	Tools             []ChatCompletionTool    `json:"tools,omitempty"`
	ToolChoice        json.RawMessage         `json:"toolChoice,omitempty"`
	ParallelToolCalls bool                    `json:"parallelToolCalls"`
}

type ChatCompletionResponse struct {
	FinishReason     string                `json:"finishReason"`
	ProviderName     string                `json:"provider"`
	ModelName        string                `json:"model"`
	SelectedBackend  string                `json:"selectedBackend"`
	ProviderMetadata json.RawMessage       `json:"providerMetadata,omitempty"`
	Message          ChatCompletionMessage `json:"message"`
	Usage            Usage                 `json:"usage"`
	UsedFallback     bool                  `json:"-"`
}

type ChatCompletionMessage struct {
	Role       string                   `json:"role"`
	Content    string                   `json:"content,omitempty"`
	ToolCallID string                   `json:"toolCallId,omitempty"`
	ToolCalls  []ChatCompletionToolCall `json:"toolCalls,omitempty"`
}

type ChatCompletionTool struct {
	Type     string                 `json:"type"`
	Function ChatCompletionFunction `json:"function"`
}

type ChatCompletionFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ChatCompletionToolCall struct {
	ID       string                         `json:"id"`
	Type     string                         `json:"type"`
	Function ChatCompletionToolCallFunction `json:"function"`
}

type ChatCompletionToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatCompleter interface {
	GenerateChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error)
}
