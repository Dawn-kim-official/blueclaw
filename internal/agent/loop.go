package agent

import (
	"context"
	"fmt"

	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type Loop struct {
	llmProvider  provider.LLMProvider
	toolRegistry *tool.Registry
}

func NewLoop(llmProvider provider.LLMProvider, toolRegistry *tool.Registry) *Loop {
	return &Loop{
		llmProvider:  llmProvider,
		toolRegistry: toolRegistry,
	}
}

func (loop *Loop) Run(loopContext context.Context, request provider.Request) (provider.Response, error) {
	messages := make([]provider.Message, len(request.Messages))
	copy(messages, request.Messages)
	for {
		currentRequest := provider.Request{
			SystemPrompt:    request.SystemPrompt,
			Messages:        messages,
			ToolDefinitions: request.ToolDefinitions,
			Model:           request.Model,
		}
		response, err := loop.llmProvider.SendMessage(loopContext, currentRequest)
		if err != nil {
			return partialResponse(), nil
		}
		if len(response.ToolCalls) == 0 {
			return response, nil
		}
		messages = append(messages, response.Message)
		messages = append(messages, loop.executeToolCalls(loopContext, response.ToolCalls)...)
	}
}

func (loop *Loop) executeToolCalls(executionContext context.Context, toolCalls []provider.ToolCall) []provider.Message {
	resultMessages := make([]provider.Message, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		resultMessages = append(resultMessages, loop.executeToolCall(executionContext, toolCall))
	}
	return resultMessages
}

func (loop *Loop) executeToolCall(executionContext context.Context, toolCall provider.ToolCall) provider.Message {
	registeredTool, err := loop.toolRegistry.Get(toolCall.Name)
	if err != nil {
		return provider.Message{Role: "tool", Content: fmt.Sprintf("error: unknown tool %q", toolCall.Name), ToolCallID: toolCall.ID}
	}
	result, err := registeredTool.Execute(executionContext, toolCall.Arguments)
	if err != nil {
		return provider.Message{Role: "tool", Content: fmt.Sprintf("error: %v", err), ToolCallID: toolCall.ID}
	}
	content := result.Output
	if result.Error != "" {
		content = fmt.Sprintf("error: %s", result.Error)
	}
	return provider.Message{Role: "tool", Content: content, ToolCallID: toolCall.ID}
}

func partialResponse() provider.Response {
	return provider.Response{Message: provider.Message{Role: "assistant", Content: "I ran out of time before finishing."}}
}
