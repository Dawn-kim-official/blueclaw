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

func TestSDKDClientRejectsInvalidSuccessfulResponse(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{name: "empty response", body: `{}`},
		{name: "unknown backend", body: `{"provider":"sdkd","model":"model","content":"{}","selectedBackend":"unknown","finishReason":"stop"}`},
		{name: "non-stop finish", body: `{"provider":"sdkd","model":"model","content":"{}","selectedBackend":"device","finishReason":"length"}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				_, _ = responseWriter.Write([]byte(testCase.body))
			}))
			defer server.Close()
			fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewSDKDClient(SDKDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected invalid SDKD success without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestSDKDClientDoesNotFallbackOnContractFailure(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		code       string
	}{
		{name: "schema", statusCode: http.StatusUnprocessableEntity, code: "structured_output_invalid"},
		{name: "policy", statusCode: http.StatusForbidden, code: "policy_remote_disabled"},
		{name: "configuration", statusCode: http.StatusBadRequest, code: "configuration_invalid"},
		{name: "authentication", statusCode: http.StatusUnauthorized, code: "unauthorized"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(testCase.statusCode)
				_, _ = responseWriter.Write([]byte(`{"error":{"code":"` + testCase.code + `","allowLegacyFallback":true}}`))
			}))
			defer server.Close()
			fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewSDKDClient(SDKDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected SDKD contract failure without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestSDKDClientDoesNotFallbackOnUntrustedErrorEnvelope(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "unknown code", statusCode: http.StatusServiceUnavailable, body: `{"error":{"code":"unknown","allowLegacyFallback":true}}`},
		{name: "plain response", statusCode: http.StatusBadGateway, body: "bad gateway"},
		{name: "malformed response", statusCode: http.StatusBadGateway, body: `{"error":`},
		{name: "mismatched status", statusCode: http.StatusBadRequest, body: `{"error":{"code":"provider_unavailable","allowLegacyFallback":true}}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(testCase.statusCode)
				_, _ = responseWriter.Write([]byte(testCase.body))
			}))
			defer server.Close()
			fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewSDKDClient(SDKDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected SDKD failure without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestSDKDClientDoesNotFallbackInLocalOnlyMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()
	fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewSDKDClient(SDKDClientConfiguration{
		Endpoint:                   server.URL,
		AuthKey:                    "installation-key",
		LocalOnly:                  true,
		StructuredFallbackProvider: fallbackProvider,
	})

	_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
		t.Fatalf("expected local-only SDKD failure without legacy fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
	}
}

func TestSDKDClientRejectsRemoteResultInLocalOnlyMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = responseWriter.Write([]byte(`{"provider":"openrouter","model":"remote-model","content":"{}","selectedBackend":"remote","finishReason":"stop"}`))
	}))
	defer server.Close()
	fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewSDKDClient(SDKDClientConfiguration{
		Endpoint:                   server.URL,
		AuthKey:                    "installation-key",
		LocalOnly:                  true,
		StructuredFallbackProvider: fallbackProvider,
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue == nil || errorValue.Error() != "sdkd remote response is forbidden in local-only mode" {
		t.Fatalf("expected remote SDKD result rejection, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.structuredCallCount != 0 {
		t.Fatalf("expected no local-only legacy fallback, got %d calls", fallbackProvider.structuredCallCount)
	}
}

func TestSDKDClientDoesNotUseDisabledSchemaFallbackInLocalOnlyMode(t *testing.T) {
	fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewSDKDClient(SDKDClientConfiguration{
		LocalOnly:                  true,
		StructuredFallbackProvider: fallbackProvider,
		StructuredSchemaNames:      []string{"blueclaw_agent_turn_action"},
	})

	_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_turn_router"},
	})
	if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
		t.Fatalf("expected disabled local-only schema without legacy fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
	}
}

func TestSDKDClientFallsBackWhenResponseBodyReadFails(t *testing.T) {
	fallbackProvider := &sdkdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewSDKDClient(SDKDClientConfiguration{AuthKey: "installation-key", StructuredFallbackProvider: fallbackProvider})
	client.HTTPClient = sdkdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(sdkdFailingReader{}),
			Header:     make(http.Header),
		}, nil
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue != nil || response.ProviderName != "capabilityLLM" || !response.UsedFallback {
		t.Fatalf("expected response read failure fallback, got %+v, %v", response, errorValue)
	}
}

type sdkdFailingReader struct{}

func (sdkdFailingReader) Read([]byte) (int, error) {
	return 0, errors.New("response read failed")
}

func TestSDKDClientUsesBridgeEndpointWithoutGuestCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("expected no guest authorization header, got %q", request.Header.Get("Authorization"))
		}
		if request.URL.Path != "/_internkim/sdkd/v1/llm/structured" {
			t.Fatalf("unexpected SDKD bridge path %q", request.URL.Path)
		}
		_, _ = responseWriter.Write([]byte(`{"provider":"openrouter","model":"test","content":"{}","selectedBackend":"remote","finishReason":"stop"}`))
	}))
	defer server.Close()
	client := NewSDKDClient(SDKDClientConfiguration{Endpoint: sdkdLoopbackBridgeEndpoint, ModelName: "test"})
	client.HTTPClient = sdkdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
		request.URL.Scheme = "http"
		request.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultClient.Do(request)
	})

	_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue != nil {
		t.Fatalf("expected bridge request without guest credential: %v", errorValue)
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

func TestSDKDClientFallsBackOnRecognizedTransientFailures(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		code       string
	}{
		{name: "provider rate limit", statusCode: http.StatusTooManyRequests, code: "provider_rate_limited"},
		{name: "provider internal error", statusCode: http.StatusInternalServerError, code: "provider_unavailable"},
		{name: "provider bad gateway", statusCode: http.StatusBadGateway, code: "provider_unavailable"},
		{name: "provider unavailable", statusCode: http.StatusServiceUnavailable, code: "provider_unavailable"},
		{name: "provider timeout", statusCode: http.StatusGatewayTimeout, code: "provider_unavailable"},
		{name: "bridge unavailable", statusCode: http.StatusServiceUnavailable, code: "sdkd_bridge_unavailable"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(testCase.statusCode)
				_, _ = responseWriter.Write([]byte(`{"error":{"code":"` + testCase.code + `","allowLegacyFallback":true}}`))
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
				StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_agent_turn_action", Document: `{"type":"object"}`},
			})
			if errorValue != nil || response.ProviderName != "capabilityLLM" || !response.UsedFallback {
				t.Fatalf("expected marked legacy fallback response, got %+v, %v", response, errorValue)
			}
		})
	}
}

type sdkdTestHTTPClient func(*http.Request) (*http.Response, error)

func (client sdkdTestHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client(request)
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
