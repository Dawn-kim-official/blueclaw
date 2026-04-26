package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"blueclaw/internal/capability"
)

func TestCapabilityLLMClientSendsStructuredRequestWithoutAuthorization(t *testing.T) {
	var receivedAuthorization string
	var receivedDocument capabilityStructuredResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/llm/structured" {
			t.Fatalf("expected structured llm path, got %q", request.URL.Path)
		}
		receivedAuthorization = request.Header.Get("Authorization")
		errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument)
		if errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma-4-E4B-it","content":"{\"reply\":\"안녕하세요\"}","selectedBackend":"gpu"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma-4-E4B-it",
		ExecutionMode: "local",
	}

	structuredResponse, errorValue := client.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}

	if receivedAuthorization != "" {
		t.Fatalf("expected no authorization header, got %q", receivedAuthorization)
	}
	if receivedDocument.Model != "gemma-4-E4B-it" {
		t.Fatalf("expected model to be passed through, got %q", receivedDocument.Model)
	}
	if receivedDocument.ExecutionMode != "local" {
		t.Fatalf("expected execution mode to be passed through, got %q", receivedDocument.ExecutionMode)
	}
	if string(receivedDocument.StructuredOutputSchema.Document) != `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"],"additionalProperties":false}` {
		t.Fatalf("expected schema document to be unchanged, got %s", string(receivedDocument.StructuredOutputSchema.Document))
	}
	if structuredResponse.Content != `{"reply":"안녕하세요"}` {
		t.Fatalf("expected capability content to be returned, got %q", structuredResponse.Content)
	}
	if structuredResponse.ModelName != "gemma-4-E4B-it" {
		t.Fatalf("expected model name from capability response, got %q", structuredResponse.ModelName)
	}
}

func TestCapabilityLLMClientReturnsCapabilityError(t *testing.T) {
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		return jsonCapabilityResponse(http.StatusBadGateway, "local model unavailable"), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma-4-E4B-it",
	}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), buildTestStructuredResponseRequest())
	if errorValue == nil {
		t.Fatal("expected capability error")
	}
	if errorValue.Error() != "local model unavailable" {
		t.Fatalf("expected capability error body, got %q", errorValue.Error())
	}
}

type fakeCapabilityHTTPClient struct {
	handler func(*http.Request) (*http.Response, error)
}

func (client fakeCapabilityHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client.handler(request)
}

func jsonCapabilityResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
