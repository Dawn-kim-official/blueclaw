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
	if receivedDocument.GenerationOptions != nil {
		t.Fatalf("expected empty generation options to be omitted, got %+v", receivedDocument.GenerationOptions)
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

func TestCapabilityLLMClientForwardsGenerationOptions(t *testing.T) {
	seed := int64(1234)
	temperature := 0.2
	var receivedDocument capabilityStructuredResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"content":"{\"reply\":\"ok\"}"}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma",
	}
	request := buildTestStructuredResponseRequest()
	request.GenerationOptions = GenerationOptions{Seed: &seed, Temperature: &temperature}

	_, errorValue := client.GenerateStructuredResponse(context.Background(), request)

	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}
	if receivedDocument.GenerationOptions == nil || receivedDocument.GenerationOptions.Seed == nil || *receivedDocument.GenerationOptions.Seed != seed {
		t.Fatalf("expected seed to be forwarded, got %+v", receivedDocument.GenerationOptions)
	}
	if receivedDocument.GenerationOptions.Temperature == nil || *receivedDocument.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected temperature to be forwarded, got %+v", receivedDocument.GenerationOptions)
	}
}

func TestCapabilityLLMClientGenerateResponseUsesTextEndpoint(t *testing.T) {
	var receivedDocument capabilityTextResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/llm/text" {
			t.Fatalf("expected text llm path, got %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("expected no authorization header, got %q", request.Header.Get("Authorization"))
		}
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"plain reply","selectedBackend":"cpu"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "local",
	}

	response, errorValue := client.GenerateResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected text response: %v", errorValue)
	}
	if response != "plain reply" {
		t.Fatalf("expected plain text response, got %q", response)
	}
	if receivedDocument.Model != "gemma" || receivedDocument.ExecutionMode != "local" {
		t.Fatalf("expected model and execution mode, got %+v", receivedDocument)
	}
	if len(receivedDocument.Messages) != 1 || receivedDocument.Messages[0].Content != "hello" {
		t.Fatalf("expected prompt message, got %+v", receivedDocument.Messages)
	}
}

func TestCapabilityLLMClientSendsRequesterContext(t *testing.T) {
	var receivedDocument capabilityStructuredResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"{\"reply\":\"ok\"}","selectedBackend":"companion_local"}`), nil
	}}
	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName: "gemma",
	}
	requestContext := RequestContext{
		RequesterPersonID:       "person-1",
		RequesterEmail:          "alice@example.com",
		RequesterName:           "Alice",
		RequesterPlatformUserID: "user-1",
		ConversationID:          "dm:channel-1",
		Platform:                "mattermost",
	}
	_, errorValue := client.GenerateStructuredResponse(ContextWithRequestContext(context.Background(), requestContext), buildTestStructuredResponseRequest())
	if errorValue != nil {
		t.Fatalf("expected structured response: %v", errorValue)
	}
	if receivedDocument.Context == nil || *receivedDocument.Context != requestContext {
		t.Fatalf("expected requester context, got %+v", receivedDocument.Context)
	}
}

func TestCapabilityLLMClientRecoveryResponseUsesLocalCapableExecutionMode(t *testing.T) {
	receivedExecutionModes := []string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		var receivedDocument capabilityTextResponseRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		receivedExecutionModes = append(receivedExecutionModes, receivedDocument.ExecutionMode)
		if receivedDocument.ExecutionMode == "auto" {
			return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"local-ish reply","selectedBackend":"cpu"}`), nil
		}
		return jsonCapabilityResponse(http.StatusBadGateway, "remote unavailable"), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateRecoveryResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected recovery text response: %v", errorValue)
	}
	if response != "local-ish reply" {
		t.Fatalf("expected recovery response, got %q", response)
	}
	if len(receivedExecutionModes) != 1 || receivedExecutionModes[0] != "auto" {
		t.Fatalf("expected recovery to use auto execution mode, got %+v", receivedExecutionModes)
	}
}

func TestCapabilityLLMClientRecoveryResponseFallsBackToDeviceAfterAutoFailure(t *testing.T) {
	receivedExecutionModes := []string{}
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		var receivedDocument capabilityTextResponseRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		receivedExecutionModes = append(receivedExecutionModes, receivedDocument.ExecutionMode)
		if receivedDocument.ExecutionMode == "auto" {
			return jsonCapabilityResponse(http.StatusBadGateway, "remote unavailable"), nil
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"device recovery reply","selectedBackend":"device"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateRecoveryResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected recovery text response: %v", errorValue)
	}
	if response != "device recovery reply" {
		t.Fatalf("expected device recovery response, got %q", response)
	}
	if strings.Join(receivedExecutionModes, ",") != "auto,device" {
		t.Fatalf("expected auto then device execution modes, got %+v", receivedExecutionModes)
	}
}

func TestCapabilityLLMClientLocalRecoveryResponseUsesDeviceExecutionMode(t *testing.T) {
	var receivedDocument capabilityTextResponseRequestDocument
	httpClient := fakeCapabilityHTTPClient{handler: func(request *http.Request) (*http.Response, error) {
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected request document to decode: %v", errorValue)
		}
		return jsonCapabilityResponse(http.StatusOK, `{"provider":"capabilityLLM","model":"gemma","content":"local failure notice","selectedBackend":"device"}`), nil
	}}

	client := CapabilityLLMClient{
		CapabilityClient: capability.Client{
			Endpoint:   "http://internkim-capability",
			HTTPClient: httpClient,
		},
		ModelName:     "gemma",
		ExecutionMode: "remote",
	}

	response, errorValue := client.GenerateLocalRecoveryResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected local recovery response: %v", errorValue)
	}
	if response != "local failure notice" {
		t.Fatalf("expected local recovery response, got %q", response)
	}
	if receivedDocument.ExecutionMode != "device" {
		t.Fatalf("expected device execution mode, got %q", receivedDocument.ExecutionMode)
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
