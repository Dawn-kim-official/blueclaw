package provider

import (
	"context"
	"fmt"
)

type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoningContent,omitempty"`
	ToolCalls        []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID       string     `json:"toolCallId,omitempty"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Request struct {
	SystemPrompt    string
	Messages        []Message
	ToolDefinitions []ToolDefinition
	Model           string
	JSONMode        bool
}

type Response struct {
	Message             Message
	ToolCalls           []ToolCall
	IntermediateContent []string
}

type LLMProvider interface {
	Name() string
	SendMessage(context context.Context, request Request) (Response, error)
}

func NewProvider(providerName string, apiKey string, model string, endpointOverride string, debug bool) (LLMProvider, error) {
	switch providerName {
	case "anthropic":
		return NewAnthropicProvider(apiKey, model, debug), nil
	case "openai", "gemini", "deepseek", "glm":
		return NewOpenAICompatibleProvider(providerName, apiKey, model, endpointOverride, debug), nil
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", providerName)
	}
}
