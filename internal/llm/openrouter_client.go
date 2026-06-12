package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultOpenRouterChatCompletionsURL = "https://openrouter.ai/api/v1/chat/completions"

type OpenRouterClient struct {
	APIKey         string
	BaseURL        string
	ModelName      string
	HTTPClient     *http.Client
	AttemptCount   int
	InitialBackoff time.Duration
}

type openRouterRequest struct {
	Model          string                `json:"model"`
	Messages       []openRouterMessage   `json:"messages"`
	Stream         bool                  `json:"stream"`
	ResponseFormat *openRouterJSONSchema `json:"response_format,omitempty"`
	Seed           *int64                `json:"seed,omitempty"`
	Temperature    *float64              `json:"temperature,omitempty"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openRouterJSONSchema struct {
	Type       string                      `json:"type"`
	JSONSchema openRouterJSONSchemaPayload `json:"json_schema"`
}

type openRouterJSONSchemaPayload struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openRouterUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage openRouterUsage `json:"usage"`
}

type OpenRouterRateLimitError struct {
	Message string
}

func (errorValue OpenRouterRateLimitError) Error() string {
	return errorValue.Message
}

func (client OpenRouterClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	response, errorValue := client.send(responseContext, openRouterRequest{
		Model:    client.modelName(),
		Messages: []openRouterMessage{{Role: "user", Content: prompt}},
		Stream:   false,
	})
	if errorValue != nil {
		return "", errorValue
	}
	return openRouterResponseContent(response), nil
}

func (client OpenRouterClient) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	schemaDocument, errorValue := normalizeStructuredOutputSchema(request.StructuredOutputSchema)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	response, errorValue := client.send(responseContext, openRouterRequest{
		Model:       client.modelName(),
		Messages:    openRouterMessages(request.Messages),
		Stream:      false,
		Seed:        request.GenerationOptions.Seed,
		Temperature: request.GenerationOptions.Temperature,
		ResponseFormat: &openRouterJSONSchema{
			Type: "json_schema",
			JSONSchema: openRouterJSONSchemaPayload{
				Name:   request.StructuredOutputSchema.Name,
				Strict: request.StructuredOutputSchema.IsStrictlyEnforced,
				Schema: schemaDocument,
			},
		},
	})
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	return StructuredResponse{
		ProviderName: "openrouter",
		ModelName:    client.modelName(),
		Content:      openRouterResponseContent(response),
		Usage: Usage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      openRouterTotalTokens(response.Usage),
		},
	}, nil
}

func (client OpenRouterClient) send(ctx context.Context, request openRouterRequest) (openRouterResponse, error) {
	if strings.TrimSpace(client.APIKey) == "" {
		return openRouterResponse{}, errors.New("openrouter api key is not configured")
	}
	var response openRouterResponse
	errorValue := retryOpenRouterRateLimit(ctx, client.attemptCount(), client.initialBackoff(), func() error {
		nextResponse, nextError := client.sendOnce(ctx, request)
		if nextError != nil {
			return nextError
		}
		response = nextResponse
		return nil
	})
	return response, errorValue
}

func (client OpenRouterClient) sendOnce(ctx context.Context, request openRouterRequest) (openRouterResponse, error) {
	requestDocument, errorValue := json.Marshal(request)
	if errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL(), bytes.NewReader(requestDocument))
	if errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(client.APIKey))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, errorValue := client.httpClient().Do(httpRequest)
	if errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	defer httpResponse.Body.Close()
	responseDocument, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return openRouterResponse{}, errors.New("read openrouter response: " + errorValue.Error())
	}
	if httpResponse.StatusCode == http.StatusTooManyRequests {
		return openRouterResponse{}, OpenRouterRateLimitError{Message: strings.TrimSpace(string(responseDocument))}
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return openRouterResponse{}, errors.New(strings.TrimSpace(string(responseDocument)))
	}
	var response openRouterResponse
	if errorValue := json.Unmarshal(responseDocument, &response); errorValue != nil {
		return openRouterResponse{}, errorValue
	}
	if len(response.Choices) == 0 {
		return openRouterResponse{}, errors.New("openrouter response did not include choices")
	}
	if strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return openRouterResponse{}, errors.New("openrouter response content was empty")
	}
	return response, nil
}

func retryOpenRouterRateLimit(ctx context.Context, attemptCount int, initialBackoff time.Duration, action func() error) error {
	backoff := initialBackoff
	for attempt := 1; attempt <= attemptCount; attempt++ {
		errorValue := action()
		if errorValue == nil {
			return nil
		}
		if !isOpenRouterRateLimit(errorValue) || attempt == attemptCount {
			return errorValue
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
	}
	return nil
}

func isOpenRouterRateLimit(errorValue error) bool {
	var rateLimitError OpenRouterRateLimitError
	return errors.As(errorValue, &rateLimitError)
}

func (client OpenRouterClient) httpClient() *http.Client {
	if client.HTTPClient != nil {
		return client.HTTPClient
	}
	return &http.Client{Timeout: 90 * time.Second}
}

func (client OpenRouterClient) baseURL() string {
	trimmedBaseURL := strings.TrimSpace(client.BaseURL)
	if trimmedBaseURL != "" {
		return trimmedBaseURL
	}
	return DefaultOpenRouterChatCompletionsURL
}

func (client OpenRouterClient) modelName() string {
	return strings.TrimSpace(client.ModelName)
}

func (client OpenRouterClient) attemptCount() int {
	if client.AttemptCount > 0 {
		return client.AttemptCount
	}
	return 3
}

func (client OpenRouterClient) initialBackoff() time.Duration {
	if client.InitialBackoff > 0 {
		return client.InitialBackoff
	}
	return 750 * time.Millisecond
}

func openRouterMessages(messages []Message) []openRouterMessage {
	result := make([]openRouterMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, openRouterMessage{
			Role:    message.Role,
			Content: openRouterMessageContent(message),
		})
	}
	return result
}

func openRouterMessageContent(message Message) any {
	if len(message.Parts) == 0 {
		return message.Content
	}
	parts := []map[string]any{}
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": message.Content})
	}
	for _, part := range message.Parts {
		if strings.TrimSpace(part.Type) == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, map[string]any{"type": "text", "text": part.Text})
		}
		if strings.TrimSpace(part.Type) == "image" && strings.TrimSpace(part.DataBase64) != "" && strings.TrimSpace(part.MimeType) != "" {
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]string{
					"url": "data:" + strings.TrimSpace(part.MimeType) + ";base64," + strings.TrimSpace(part.DataBase64),
				},
			})
		}
	}
	if len(parts) == 0 {
		return message.Content
	}
	return parts
}

func openRouterResponseContent(response openRouterResponse) string {
	if len(response.Choices) == 0 {
		return ""
	}
	return response.Choices[0].Message.Content
}

func openRouterTotalTokens(usage openRouterUsage) int64 {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}
