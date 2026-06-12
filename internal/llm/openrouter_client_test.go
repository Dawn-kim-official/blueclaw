package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type openRouterRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTripper openRouterRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
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

func openRouterTestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
