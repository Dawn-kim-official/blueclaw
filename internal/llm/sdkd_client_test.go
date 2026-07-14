package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSDKDClientSendsAuthenticatedStructuredRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer installation-key" {
			t.Fatalf("expected installation key authorization, got %q", request.Header.Get("Authorization"))
		}
		var requestDocument capabilityStructuredResponseRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Fatalf("expected valid request document: %v", errorValue)
		}
		if requestDocument.Model != "deepseek/deepseek-v4-flash" || requestDocument.ExecutionMode != "remote" {
			t.Fatalf("unexpected sdkd route request: %+v", requestDocument)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"provider":"openrouter","model":"deepseek/deepseek-v4-flash","content":"{\"ok\":true}","selectedBackend":"remote","finishReason":"stop","constraintMode":"openai_json_schema","usage":{"promptTokens":7,"completionTokens":3,"totalTokens":10}}`))
	}))
	defer server.Close()

	client := NewSDKDClient(SDKDClientConfiguration{
		Endpoint:      server.URL,
		AuthKey:       "installation-key",
		ModelName:     "deepseek/deepseek-v4-flash",
		ExecutionMode: "remote",
	})
	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "Return ok."}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:               "test_output",
			Document:           `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		t.Fatalf("expected sdkd response: %v", errorValue)
	}
	if response.ProviderName != "openrouter" || response.ModelName != "deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected sdkd response metadata: %+v", response)
	}
	if response.Usage.TotalTokens != 10 || response.Content != `{"ok":true}` {
		t.Fatalf("unexpected sdkd response: %+v", response)
	}
	if response.SelectedBackend != "remote" || response.FinishReason != "stop" {
		t.Fatalf("expected sdkd execution metadata, got %+v", response)
	}
}

func TestSDKDClientDelegatesTextGeneration(t *testing.T) {
	textProvider := &sdkdTestLanguageModel{textResponse: "delegated"}
	client := NewSDKDClient(SDKDClientConfiguration{TextProvider: textProvider})

	response, errorValue := client.GenerateResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected delegated text response: %v", errorValue)
	}
	if response != "delegated" || textProvider.textCallCount != 1 {
		t.Fatalf("expected one delegated call, got response %q and count %d", response, textProvider.textCallCount)
	}
}

func TestSDKDClientUsesLegacyProviderOutsideEnabledSchemas(t *testing.T) {
	fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewSDKDClient(SDKDClientConfiguration{
		StructuredFallbackProvider: fallbackProvider,
		StructuredSchemaNames:      []string{"blueclaw_agent_turn_action"},
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_turn_router"},
	})
	if errorValue != nil || response.ProviderName != "capabilityLLM" {
		t.Fatalf("expected legacy structured provider, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.structuredCallCount != 1 {
		t.Fatalf("expected one legacy structured call, got %d", fallbackProvider.structuredCallCount)
	}
}

func TestSDKDClientFallsBackWhenEnabledSchemaFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusBadGateway)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_unavailable"}}`))
	}))
	defer server.Close()
	fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewSDKDClient(SDKDClientConfiguration{
		Endpoint:                   server.URL,
		AuthKey:                    "installation-key",
		StructuredFallbackProvider: fallbackProvider,
		StructuredSchemaNames:      []string{"blueclaw_agent_turn_action"},
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "Return action."}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:     "blueclaw_agent_turn_action",
			Document: `{"type":"object"}`,
		},
	})
	if errorValue != nil || response.ProviderName != "capabilityLLM" || !response.UsedFallback {
		t.Fatalf("expected marked legacy fallback response, got %+v, %v", response, errorValue)
	}
}

type sdkdTestLanguageModel struct {
	textResponse        string
	structuredResponse  StructuredResponse
	structuredError     error
	textCallCount       int
	structuredCallCount int
	structuredCalled    chan struct{}
}

func (languageModel *sdkdTestLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	languageModel.textCallCount++
	return languageModel.textResponse, nil
}

func (languageModel *sdkdTestLanguageModel) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	languageModel.structuredCallCount++
	if languageModel.structuredCalled != nil {
		languageModel.structuredCalled <- struct{}{}
	}
	return languageModel.structuredResponse, languageModel.structuredError
}
