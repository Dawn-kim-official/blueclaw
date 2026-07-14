package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type openRouterRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTripper openRouterRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestOpenRouterClientUsesCallerContextForRequestLifetime(t *testing.T) {
	if timeout := (OpenRouterClient{}).httpClient().Timeout; timeout != 0 {
		t.Fatalf("expected no provider-specific request timeout, got %s", timeout)
	}
}

func TestOpenRouterClientRetriesRateLimit(t *testing.T) {
	requestCount := 0
	client := OpenRouterClient{
		APIKey:         "sk-test",
		BaseURL:        "https://openrouter.test/api/v1/chat/completions",
		ModelName:      "test-model",
		AttemptCount:   2,
		InitialBackoff: time.Millisecond,
		HTTPClient: &http.Client{Transport: openRouterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestCount++
			if request.Header.Get("Authorization") != "Bearer sk-test" {
				t.Fatalf("expected authorization header, got %q", request.Header.Get("Authorization"))
			}
			if requestCount == 1 {
				return openRouterTestResponse(http.StatusTooManyRequests, "rate limited"), nil
			}
			var requestDocument map[string]any
			if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
				t.Fatalf("expected request document: %v", errorValue)
			}
			if requestDocument["model"] != "test-model" {
				t.Fatalf("expected model test-model, got %+v", requestDocument)
			}
			return openRouterTestResponse(http.StatusOK, `{"choices":[{"message":{"content":"{\"reply\":\"ok\"}"}}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`), nil
		})},
	}

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:               "reply",
			Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})

	if errorValue != nil {
		t.Fatalf("expected retry to succeed: %v", errorValue)
	}
	if requestCount != 2 {
		t.Fatalf("expected two requests, got %d", requestCount)
	}
	if response.Content != `{"reply":"ok"}` {
		t.Fatalf("expected structured content, got %q", response.Content)
	}
	if response.Usage.TotalTokens != 5 {
		t.Fatalf("expected total token fallback, got %+v", response.Usage)
	}
}

func TestOpenRouterClientRetriesStructuredProseWithJSONInstruction(t *testing.T) {
	requestDocuments := []openRouterRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestDocuments = append(requestDocuments, decodeOpenRouterTestRequest(t, request))
		if len(requestDocuments) == 1 {
			writeOpenRouterTestContent(t, responseWriter, "회의록은 결정과 실행 과제를 담아야 합니다.", 2, 3)
			return
		}
		writeOpenRouterTestContent(t, responseWriter, `{"reply":"ok"}`, 5, 7)
	}))
	defer server.Close()

	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   server.URL,
		ModelName: "google/gemma-4-31b-it:free",
	}

	response, errorValue := client.GenerateStructuredResponse(context.Background(), buildOpenRouterStructuredResponseTestRequest())

	if errorValue != nil {
		t.Fatalf("expected retry to succeed: %v", errorValue)
	}
	if response.Content != `{"reply":"ok"}` {
		t.Fatalf("expected retry json content, got %q", response.Content)
	}
	if response.Usage.TotalTokens != 12 {
		t.Fatalf("expected retry usage, got %+v", response.Usage)
	}
	if len(requestDocuments) != 2 {
		t.Fatalf("expected two requests, got %d", len(requestDocuments))
	}
	schemaText := `"additionalProperties":false`
	if !openRouterTestMessagesContainText(requestDocuments[0].Messages, schemaText) {
		t.Fatalf("expected first request schema instruction, got %+v", requestDocuments[0].Messages)
	}
	if !openRouterTestMessagesContainText(requestDocuments[1].Messages, schemaText) {
		t.Fatalf("expected retry request schema instruction, got %+v", requestDocuments[1].Messages)
	}
	retryMessages := requestDocuments[1].Messages
	if len(retryMessages) != 3 {
		t.Fatalf("expected retry conversation messages, got %+v", retryMessages)
	}
	if retryMessages[1].Role != "assistant" || retryMessages[1].Content != "회의록은 결정과 실행 과제를 담아야 합니다." {
		t.Fatalf("expected assistant prose in retry conversation, got %+v", retryMessages[1])
	}
	retryInstruction, isText := retryMessages[2].Content.(string)
	if retryMessages[2].Role != "user" || !isText || !strings.Contains(retryInstruction, openRouterStructuredResponseRetryInstruction) {
		t.Fatalf("expected retry instruction, got %+v", retryMessages[2])
	}
}

func TestOpenRouterClientReturnsErrorWhenStructuredRetryIsProse(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		_ = decodeOpenRouterTestRequest(t, request)
		if requestCount == 1 {
			writeOpenRouterTestContent(t, responseWriter, "first prose response", 2, 3)
			return
		}
		writeOpenRouterTestContent(t, responseWriter, "second prose response", 5, 7)
	}))
	defer server.Close()

	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   server.URL,
		ModelName: "test-model",
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), buildOpenRouterStructuredResponseTestRequest())

	if errorValue == nil {
		t.Fatal("expected prose retry to fail")
	}
	errorText := errorValue.Error()
	if requestCount != 2 {
		t.Fatalf("expected two requests, got %d", requestCount)
	}
	if !strings.Contains(errorText, "code=200") || !strings.Contains(errorText, "first prose response") || !strings.Contains(errorText, "second prose response") {
		t.Fatalf("expected status and prose summaries in error, got %q", errorText)
	}
}

func TestOpenRouterClientReturnsTruncationErrorWithoutGrowingRetry(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		_ = decodeOpenRouterTestRequest(t, request)
		responseWriter.Header().Set("Content-Type", "application/json")
		responseDocument := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"reply":"this got cut o`}, "finish_reason": "length"},
			},
			"usage": map[string]int64{"prompt_tokens": 99000, "completion_tokens": 8000, "total_tokens": 107000},
		}
		if errorValue := json.NewEncoder(responseWriter).Encode(responseDocument); errorValue != nil {
			t.Fatalf("expected response document to encode: %v", errorValue)
		}
	}))
	defer server.Close()

	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   server.URL,
		ModelName: "test-model",
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), buildOpenRouterStructuredResponseTestRequest())

	if errorValue == nil {
		t.Fatal("expected truncated structured output to error")
	}
	var truncatedError StructuredOutputTruncatedError
	if !errors.As(errorValue, &truncatedError) {
		t.Fatalf("expected StructuredOutputTruncatedError, got %v", errorValue)
	}
	if requestCount != 1 {
		t.Fatalf("expected no prompt-growing retry on truncation, got %d requests", requestCount)
	}
	if !strings.Contains(errorValue.Error(), "truncated") || !strings.Contains(errorValue.Error(), "compact context") {
		t.Fatalf("expected clear truncation message, got %q", errorValue.Error())
	}
}

func TestOpenRouterClientDoesNotRetryValidStructuredJSON(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		_ = decodeOpenRouterTestRequest(t, request)
		writeOpenRouterTestContent(t, responseWriter, `{"reply":"ok"}`, 2, 3)
	}))
	defer server.Close()

	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   server.URL,
		ModelName: "test-model",
	}

	response, errorValue := client.GenerateStructuredResponse(context.Background(), buildOpenRouterStructuredResponseTestRequest())

	if errorValue != nil {
		t.Fatalf("expected first response to succeed: %v", errorValue)
	}
	if requestCount != 1 {
		t.Fatalf("expected one request, got %d", requestCount)
	}
	if response.Content != `{"reply":"ok"}` {
		t.Fatalf("expected json content, got %q", response.Content)
	}
}

func TestOpenRouterClientMapsGemmaSystemMessages(t *testing.T) {
	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   "https://openrouter.test/api/v1/chat/completions",
		ModelName: "google/gemma-4-31b-it:free",
		HTTPClient: &http.Client{Transport: openRouterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			var requestDocument struct {
				Messages []openRouterMessage `json:"messages"`
			}
			if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
				t.Fatalf("expected request document: %v", errorValue)
			}
			if len(requestDocument.Messages) != 1 {
				t.Fatalf("expected merged user message, got %+v", requestDocument.Messages)
			}
			message := requestDocument.Messages[0]
			if message.Role != "user" {
				t.Fatalf("expected mapped user role, got %q", message.Role)
			}
			content, isText := message.Content.(string)
			if !isText {
				t.Fatalf("expected text content, got %+v", message.Content)
			}
			if !strings.Contains(content, "system instruction:") || !strings.Contains(content, "answer tersely") || !strings.Contains(content, "hello") {
				t.Fatalf("expected merged instruction content, got %q", content)
			}
			return openRouterTestResponse(http.StatusOK, `{"choices":[{"message":{"content":"{\"reply\":\"ok\"}"}}]}`), nil
		})},
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{
			{Role: "system", Content: "answer tersely"},
			{Role: "user", Content: "hello"},
		},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:     "reply",
			Document: `{"type":"object","properties":{"reply":{"type":"string"}}}`,
		},
	})

	if errorValue != nil {
		t.Fatalf("expected request to succeed: %v", errorValue)
	}
}

func TestOpenRouterClientStripsStructuredMarkdownFence(t *testing.T) {
	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   "https://openrouter.test/api/v1/chat/completions",
		ModelName: "test-model",
		HTTPClient: &http.Client{Transport: openRouterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return openRouterTestResponse(http.StatusOK, "{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"reply\\\":\\\"ok\\\"}\\n```\"}}]}"), nil
		})},
	}

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:     "reply",
			Document: `{"type":"object","properties":{"reply":{"type":"string"}}}`,
		},
	})

	if errorValue != nil {
		t.Fatalf("expected request to succeed: %v", errorValue)
	}
	if response.Content != `{"reply":"ok"}` {
		t.Fatalf("expected stripped json content, got %q", response.Content)
	}
}

func TestOpenRouterClientReportsHTTPStatusAndBody(t *testing.T) {
	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   "https://openrouter.test/api/v1/chat/completions",
		ModelName: "test-model",
		HTTPClient: &http.Client{Transport: openRouterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return openRouterTestResponse(http.StatusUnprocessableEntity, `{"error":{"message":"unsupported parameter seed"}}`), nil
		})},
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:     "reply",
			Document: `{"type":"object","properties":{"reply":{"type":"string"}}}`,
		},
	})

	if errorValue == nil {
		t.Fatal("expected request to fail")
	}
	errorText := errorValue.Error()
	if !strings.Contains(errorText, "code=422") || !strings.Contains(errorText, "unsupported parameter seed") {
		t.Fatalf("expected status and body in error, got %q", errorText)
	}
}

func TestOpenRouterClientReportsSuccessfulErrorEnvelope(t *testing.T) {
	client := OpenRouterClient{
		APIKey:    "sk-test",
		BaseURL:   "https://openrouter.test/api/v1/chat/completions",
		ModelName: "test-model",
		HTTPClient: &http.Client{Transport: openRouterRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return openRouterTestResponse(http.StatusOK, `{"error":{"message":"structured output is unsupported"}}`), nil
		})},
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), buildOpenRouterStructuredResponseTestRequest())

	if errorValue == nil {
		t.Fatal("expected successful error envelope to fail")
	}
	if !strings.Contains(errorValue.Error(), "code=200") || !strings.Contains(errorValue.Error(), "structured output is unsupported") {
		t.Fatalf("expected status and provider error body, got %q", errorValue.Error())
	}
}

func buildOpenRouterStructuredResponseTestRequest() StructuredResponseRequest {
	return StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:               "reply",
			Document:           `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	}
}

func decodeOpenRouterTestRequest(t *testing.T, request *http.Request) openRouterRequest {
	t.Helper()
	if request.Method != http.MethodPost {
		t.Fatalf("expected post request, got %s", request.Method)
	}
	if request.Header.Get("Authorization") != "Bearer sk-test" {
		t.Fatalf("expected authorization header, got %q", request.Header.Get("Authorization"))
	}
	var requestDocument openRouterRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
		t.Fatalf("expected request document: %v", errorValue)
	}
	return requestDocument
}

func writeOpenRouterTestContent(t *testing.T, responseWriter http.ResponseWriter, content string, promptTokens int64, completionTokens int64) {
	t.Helper()
	responseWriter.Header().Set("Content-Type", "application/json")
	responseDocument := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"content": content}},
		},
		"usage": map[string]int64{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	}
	if errorValue := json.NewEncoder(responseWriter).Encode(responseDocument); errorValue != nil {
		t.Fatalf("expected response document to encode: %v", errorValue)
	}
}

func openRouterTestMessagesContainText(messages []openRouterMessage, expectedText string) bool {
	for _, message := range messages {
		content, isText := message.Content.(string)
		if isText && strings.Contains(content, expectedText) {
			return true
		}
	}
	return false
}

func openRouterTestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
