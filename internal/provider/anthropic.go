package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

const (
	anthropicAPIURL         = "https://api.anthropic.com/v1/messages"
	anthropicDefaultModel   = "claude-sonnet-4-6"
	anthropicAPIVersion     = "2023-06-01"
	anthropicMaxTokens      = 4096
)

type AnthropicProvider struct {
	apiKey string
	model  string
	debug  bool
	client *http.Client
}

func NewAnthropicProvider(apiKey string, model string, debug bool) *AnthropicProvider {
	if model == "" {
		model = anthropicDefaultModel
	}
	return &AnthropicProvider{
		apiKey: apiKey,
		model:  model,
		debug:  debug,
		client: &http.Client{},
	}
}

func (provider *AnthropicProvider) Name() string { return "anthropic" }

func (provider *AnthropicProvider) SendMessage(requestContext context.Context, request Request) (Response, error) {
	anthropicRequest := buildAnthropicRequest(provider.model, request)
	requestBody, err := json.Marshal(anthropicRequest)
	if err != nil {
		return Response{}, fmt.Errorf("marshaling Anthropic request: %w", err)
	}
	if provider.debug {
		var indented bytes.Buffer
		json.Indent(&indented, requestBody, "", "  ")
		log.Printf("[debug] anthropic request: endpoint=%s model=%s messages=%d tools=%d\n%s\n",
			anthropicAPIURL, provider.model, len(anthropicRequest.Messages), len(anthropicRequest.Tools), indented.String())
	}
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, anthropicAPIURL, bytes.NewReader(requestBody))
	if err != nil {
		return Response{}, fmt.Errorf("creating Anthropic HTTP request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("x-api-key", provider.apiKey)
	httpRequest.Header.Set("anthropic-version", anthropicAPIVersion)
	httpResponse, err := provider.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("sending Anthropic request: %w", err)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return Response{}, fmt.Errorf("reading Anthropic response: %w", err)
	}
	if provider.debug {
		var indented bytes.Buffer
		json.Indent(&indented, body, "", "  ")
		log.Printf("[debug] anthropic response: status=%d\n%s\n", httpResponse.StatusCode, indented.String())
	}
	if httpResponse.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("Anthropic API error (status %d): %s", httpResponse.StatusCode, string(body))
	}
	return parseAnthropicResponse(body)
}

type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Tools     []anthropicTool     `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content  []anthropicContentBlock `json:"content"`
	StopReason string                `json:"stop_reason"`
}

type anthropicContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

func buildAnthropicRequest(model string, request Request) anthropicRequest {
	messages := make([]anthropicMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "tool" {
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": message.ToolCallID,
						"content":     message.Content,
					},
				},
			})
			continue
		}
		if len(message.ToolCalls) > 0 {
			contentBlocks := make([]map[string]any, 0)
			if message.Content != "" {
				contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": message.Content})
			}
			for _, toolCall := range message.ToolCalls {
				contentBlocks = append(contentBlocks, map[string]any{
					"type":  "tool_use",
					"id":    toolCall.ID,
					"name":  toolCall.Name,
					"input": toolCall.Arguments,
				})
			}
			messages = append(messages, anthropicMessage{Role: "assistant", Content: contentBlocks})
			continue
		}
		messages = append(messages, anthropicMessage{Role: message.Role, Content: message.Content})
	}
	tools := make([]anthropicTool, 0, len(request.ToolDefinitions))
	for _, definition := range request.ToolDefinitions {
		tools = append(tools, anthropicTool{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: definition.Parameters,
		})
	}
	return anthropicRequest{
		Model:     model,
		MaxTokens: anthropicMaxTokens,
		System:    request.SystemPrompt,
		Messages:  messages,
		Tools:     tools,
	}
}

func parseAnthropicResponse(body []byte) (Response, error) {
	var anthropicResponse anthropicResponse
	if err := json.Unmarshal(body, &anthropicResponse); err != nil {
		return Response{}, fmt.Errorf("parsing Anthropic response: %w", err)
	}
	var textContent string
	var toolCalls []ToolCall
	for _, block := range anthropicResponse.Content {
		switch block.Type {
		case "text":
			textContent += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}
	return Response{
		Message: Message{
			Role:      "assistant",
			Content:   textContent,
			ToolCalls: toolCalls,
		},
		ToolCalls: toolCalls,
	}, nil
}
