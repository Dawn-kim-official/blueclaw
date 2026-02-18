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

var openaiCompatibleEndpoints = map[string]string{
	"openai":   "https://api.openai.com/v1/chat/completions",
	"gemini":   "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
	"deepseek": "https://api.deepseek.com/v1/chat/completions",
	"glm":      "https://api.z.ai/api/coding/paas/v4/chat/completions",
}

var openaiCompatibleDefaultModels = map[string]string{
	"openai":   "gpt-4.1",
	"gemini":   "gemini-2.5-flash-preview",
	"deepseek": "deepseek-chat",
	"glm":      "glm-5",
}

type OpenAICompatibleProvider struct {
	providerName string
	apiKey       string
	model        string
	endpoint     string
	debug        bool
	client       *http.Client
}

func NewOpenAICompatibleProvider(providerName string, apiKey string, model string, endpointOverride string, debug bool) *OpenAICompatibleProvider {
	endpoint := openaiCompatibleEndpoints[providerName]
	if endpointOverride != "" {
		endpoint = endpointOverride
	}
	if model == "" {
		model = openaiCompatibleDefaultModels[providerName]
	}
	return &OpenAICompatibleProvider{
		providerName: providerName,
		apiKey:       apiKey,
		model:        model,
		endpoint:     endpoint,
		debug:        debug,
		client:       &http.Client{},
	}
}

func (provider *OpenAICompatibleProvider) Name() string { return provider.providerName }

func (provider *OpenAICompatibleProvider) SendMessage(requestContext context.Context, request Request) (Response, error) {
	openaiRequest := buildOpenAIRequest(provider.model, request)
	requestBody, err := json.Marshal(openaiRequest)
	if err != nil {
		return Response{}, fmt.Errorf("marshaling OpenAI request: %w", err)
	}
	if provider.debug {
		var indented bytes.Buffer
		json.Indent(&indented, requestBody, "", "  ")
		log.Printf("[debug] %s request: endpoint=%s model=%s messages=%d tools=%d\n%s\n",
			provider.providerName, provider.endpoint, provider.model,
			len(openaiRequest.Messages), len(openaiRequest.Tools), indented.String())
	}
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, provider.endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return Response{}, fmt.Errorf("creating OpenAI HTTP request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	httpResponse, err := provider.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("sending OpenAI request: %w", err)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return Response{}, fmt.Errorf("reading OpenAI response: %w", err)
	}
	if provider.debug {
		var indented bytes.Buffer
		json.Indent(&indented, body, "", "  ")
		log.Printf("[debug] %s response: status=%d\n%s\n", provider.providerName, httpResponse.StatusCode, indented.String())
	}
	if httpResponse.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("OpenAI API error (status %d): %s", httpResponse.StatusCode, string(body))
	}
	return parseOpenAIResponse(body)
}

type openaiRequest struct {
	Model          string                `json:"model"`
	Messages       []openaiMessage       `json:"messages"`
	Tools          []openaiTool          `json:"tools,omitempty"`
	ResponseFormat *openaiResponseFormat `json:"response_format,omitempty"`
}

type openaiResponseFormat struct {
	Type string `json:"type"`
}

type openaiMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

type openaiTool struct {
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openaiToolCallFunction `json:"function"`
}

type openaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
}

type openaiChoice struct {
	Message openaiMessage `json:"message"`
}


func buildOpenAIRequest(model string, request Request) openaiRequest {
	messages := make([]openaiMessage, 0, len(request.Messages)+1)
	if request.SystemPrompt != "" {
		messages = append(messages, openaiMessage{Role: "system", Content: request.SystemPrompt})
	}
	for _, message := range request.Messages {
		openaiMsg := openaiMessage{Role: message.Role, Content: message.Content, ReasoningContent: message.ReasoningContent}
		if message.Role == "tool" {
			openaiMsg.ToolCallID = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			for _, toolCall := range message.ToolCalls {
				argumentsJSON, _ := json.Marshal(toolCall.Arguments)
				openaiMsg.ToolCalls = append(openaiMsg.ToolCalls, openaiToolCall{
					ID:   toolCall.ID,
					Type: "function",
					Function: openaiToolCallFunction{
						Name:      toolCall.Name,
						Arguments: string(argumentsJSON),
					},
				})
			}
		}
		messages = append(messages, openaiMsg)
	}
	tools := make([]openaiTool, 0, len(request.ToolDefinitions))
	for _, definition := range request.ToolDefinitions {
		tools = append(tools, openaiTool{
			Type: "function",
			Function: openaiToolFunction{
				Name:        definition.Name,
				Description: definition.Description,
				Parameters:  definition.Parameters,
			},
		})
	}
	if request.JSONMode {
		return openaiRequest{
			Model:          model,
			Messages:       messages,
			ResponseFormat: &openaiResponseFormat{Type: "json_object"},
		}
	}
	return openaiRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
	}
}

func parseOpenAIResponse(body []byte) (Response, error) {
	var openaiResponse openaiResponse
	if err := json.Unmarshal(body, &openaiResponse); err != nil {
		return Response{}, fmt.Errorf("parsing OpenAI response: %w", err)
	}
	if len(openaiResponse.Choices) == 0 {
		return Response{}, fmt.Errorf("OpenAI response contained no choices")
	}
	choice := openaiResponse.Choices[0]
	var toolCalls []ToolCall
	for _, openaiToolCall := range choice.Message.ToolCalls {
		var arguments map[string]any
		if err := json.Unmarshal([]byte(openaiToolCall.Function.Arguments), &arguments); err != nil {
			arguments = map[string]any{"raw": openaiToolCall.Function.Arguments}
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:        openaiToolCall.ID,
			Name:      openaiToolCall.Function.Name,
			Arguments: arguments,
		})
	}
	return Response{
		Message: Message{
			Role:             "assistant",
			Content:          choice.Message.Content,
			ReasoningContent: choice.Message.ReasoningContent,
			ToolCalls:        toolCalls,
		},
		ToolCalls: toolCalls,
	}, nil
}
