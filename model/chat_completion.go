package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type ChatCompletionRequest struct {
	SchemaName        string                  `json:"-"`
	ModelName         string                  `json:"model,omitempty"`
	Messages          []ChatCompletionMessage `json:"messages"`
	Tools             []ChatCompletionTool    `json:"tools,omitempty"`
	ToolChoice        json.RawMessage         `json:"toolChoice,omitempty"`
	ParallelToolCalls bool                    `json:"parallelToolCalls"`
	GenerationOptions GenerationOptions       `json:"generationOptions,omitempty"`
}

type ChatCompletionResponse struct {
	Transport        string                `json:"-"`
	FinishReason     string                `json:"finishReason"`
	ProviderName     string                `json:"provider"`
	ModelName        string                `json:"model"`
	ModelTier        string                `json:"-"`
	SelectedBackend  string                `json:"selectedBackend"`
	ProviderMetadata json.RawMessage       `json:"providerMetadata,omitempty"`
	Message          ChatCompletionMessage `json:"message"`
	Usage            Usage                 `json:"usage"`
	UsedFallback     bool                  `json:"-"`
	FallbackReason   string                `json:"-"`
}

type ChatCompletionMessage struct {
	Role       string                   `json:"role"`
	Content    string                   `json:"content,omitempty"`
	Parts      []MessagePart            `json:"parts,omitempty"`
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

type RecoveryChatCompleter interface {
	GenerateRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error)
}

type LocalRecoveryChatCompleter interface {
	GenerateLocalRecoveryChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error)
}

func ChatCompletionText(response ChatCompletionResponse) (string, error) {
	if response.FinishReason != "stop" {
		return "", errors.New("chat completion did not stop normally")
	}
	if response.Message.Role != "assistant" {
		return "", errors.New("chat completion message must be assistant")
	}
	if len(response.Message.ToolCalls) > 0 {
		return "", errors.New("chat completion must not call tools")
	}
	reply := strings.TrimSpace(response.Message.Content)
	if reply == "" {
		return "", errors.New("chat completion is empty")
	}
	return reply, nil
}

func RecoveryChatCompletionText(response ChatCompletionResponse) (string, error) {
	reply, errorValue := ChatCompletionText(response)
	if errorValue != nil {
		return "", errors.New("recovery " + errorValue.Error())
	}
	return reply, nil
}
