package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNewLLMDClientDoesNotSetHTTPTimeout(t *testing.T) {
	client := NewLLMDClient(LLMDClientConfiguration{})
	httpClient, isHTTPClient := client.HTTPClient.(*http.Client)
	if !isHTTPClient {
		t.Fatalf("expected net/http client, got %T", client.HTTPClient)
	}
	if httpClient.Timeout != 0 {
		t.Fatalf("expected no LLMD client timeout, got %s", httpClient.Timeout)
	}
}

func TestStructuredOutputCorrectionFromErrorAllowsOnlyAuthoritativeDiagnostics(t *testing.T) {
	correctableCategories := []StructuredOutputDiagnosticCategory{
		StructuredOutputDiagnosticJSONParse,
		StructuredOutputDiagnosticSchemaValidation,
		StructuredOutputDiagnosticFinishReason,
		StructuredOutputDiagnosticToolCallContract,
	}
	for _, code := range []string{"provider_response_invalid", "structured_output_invalid"} {
		for _, category := range correctableCategories {
			correction, isCorrectable := StructuredOutputCorrectionFromError(llmdHTTPError{
				Code:       code,
				Diagnostic: StructuredOutputDiagnostic{Category: category},
			})
			if !isCorrectable || correction.Code != code || correction.Diagnostic.Category != category {
				t.Fatalf("expected %s/%s to be correctable, got %+v, %t", code, category, correction, isCorrectable)
			}
		}
	}

	nonCorrectableErrors := []error{
		llmdHTTPError{Code: "provider_response_invalid", Diagnostic: StructuredOutputDiagnostic{Category: StructuredOutputDiagnosticSerialization}},
		llmdHTTPError{Code: "provider_response_invalid", AllowLegacyFallback: true, Diagnostic: StructuredOutputDiagnostic{Category: StructuredOutputDiagnosticJSONParse}},
		llmdHTTPError{Code: "provider_api_error", Diagnostic: StructuredOutputDiagnostic{Category: StructuredOutputDiagnosticJSONParse}},
		llmdTransportError{Cause: errors.New("transport failed")},
		context.Canceled,
		context.DeadlineExceeded,
		errors.New("request failed"),
	}
	for _, errorValue := range nonCorrectableErrors {
		if _, isCorrectable := StructuredOutputCorrectionFromError(errorValue); isCorrectable {
			t.Fatalf("expected non-correctable error %T: %v", errorValue, errorValue)
		}
	}
}

func TestLLMDClientSendsAuthenticatedStructuredRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer installation-key" {
			t.Fatalf("expected installation key authorization, got %q", request.Header.Get("Authorization"))
		}
		var requestDocument capabilityStructuredResponseRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Fatalf("expected valid request document: %v", errorValue)
		}
		if requestDocument.Model != "deepseek/deepseek-v4-flash" || requestDocument.ExecutionMode != "remote" {
			t.Fatalf("unexpected llmd route request: %+v", requestDocument)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"provider":"openrouter","model":"deepseek/deepseek-v4-flash","content":"{\"ok\":true}","selectedBackend":"remote","finishReason":"stop","constraintMode":"openai_json_schema","usage":{"promptTokens":7,"completionTokens":3,"totalTokens":10}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{
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
		t.Fatalf("expected llmd response: %v", errorValue)
	}
	if response.ProviderName != "openrouter" || response.ModelName != "deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected llmd response metadata: %+v", response)
	}
	if response.Usage.TotalTokens != 10 || response.Content != `{"ok":true}` {
		t.Fatalf("unexpected llmd response: %+v", response)
	}
	if response.Transport != "llmd" || response.SelectedBackend != "remote" || response.FinishReason != "stop" {
		t.Fatalf("expected llmd execution metadata, got %+v", response)
	}
}

func TestLLMDClientGenerateChatCompletionSendsAuthenticatedRequestContext(t *testing.T) {
	requestContext := RequestContext{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Platform:          "mattermost",
	}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/llm/chat" {
			t.Fatalf("expected chat path, got %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer installation-key" {
			t.Fatalf("expected installation key authorization, got %q", request.Header.Get("Authorization"))
		}
		var requestDocument llmdChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Fatalf("expected valid request document: %v", errorValue)
		}
		if requestDocument.Model != "gemma" || requestDocument.ExecutionMode != "device" {
			t.Fatalf("unexpected model routing: %+v", requestDocument)
		}
		if requestDocument.Context == nil || *requestDocument.Context != requestContext {
			t.Fatalf("expected request context, got %+v", requestDocument.Context)
		}
		if len(requestDocument.Messages) != 1 || requestDocument.Messages[0].Content != "check" {
			t.Fatalf("expected chat messages, got %+v", requestDocument.Messages)
		}
		if len(requestDocument.Tools) != 1 || requestDocument.Tools[0].Function.Name != "lookup" {
			t.Fatalf("expected chat tools, got %+v", requestDocument.Tools)
		}
		if string(requestDocument.ToolChoice) != `"auto"` || !requestDocument.ParallelToolCalls {
			t.Fatalf("expected tool settings, got %+v", requestDocument)
		}
		_, _ = responseWriter.Write([]byte(`{"finishReason":"stop","provider":"llama.cpp","model":"gemma","selectedBackend":"device","providerMetadata":{"route":"local"},"message":{"role":"assistant","content":"done"}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:      server.URL,
		AuthKey:       "installation-key",
		ModelName:     "gemma",
		ExecutionMode: "device",
	})
	response, errorValue := client.GenerateChatCompletion(
		ContextWithRequestContext(context.Background(), requestContext),
		ChatCompletionRequest{
			Messages: []ChatCompletionMessage{{Role: "user", Content: "check"}},
			Tools: []ChatCompletionTool{{
				Type: "function",
				Function: ChatCompletionFunction{
					Name:       "lookup",
					Parameters: json.RawMessage(`{"type":"object"}`),
				},
			}},
			ToolChoice:        json.RawMessage(`"auto"`),
			ParallelToolCalls: true,
		},
	)
	if errorValue != nil {
		t.Fatalf("expected chat completion response: %v", errorValue)
	}
	if response.Transport != "llmd" || response.ProviderName != "llama.cpp" || response.ModelName != "gemma" || response.SelectedBackend != "device" {
		t.Fatalf("unexpected response metadata: %+v", response)
	}
	if string(response.ProviderMetadata) != `{"route":"local"}` || response.Message.Content != "done" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Message.ToolCalls == nil {
		t.Fatal("expected nil tool calls to normalize to an empty slice")
	}
}

func TestLLMDClientGenerateChatCompletionMergesGenerationOptions(t *testing.T) {
	defaultSeed := int64(11)
	defaultTemperature := 0.4
	defaultMaxTokens := 64
	requestSeed := int64(22)
	requestMaxTokens := 128
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var requestDocument llmdChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&requestDocument); errorValue != nil {
			t.Fatalf("expected valid request document: %v", errorValue)
		}
		if requestDocument.GenerationOptions == nil {
			t.Fatal("expected generation options")
		}
		options := requestDocument.GenerationOptions
		if options.Seed == nil || *options.Seed != requestSeed {
			t.Fatalf("expected request seed to override default, got %+v", options)
		}
		if options.Temperature == nil || *options.Temperature != defaultTemperature {
			t.Fatalf("expected default temperature, got %+v", options)
		}
		if options.MaxTokens == nil || *options.MaxTokens != requestMaxTokens {
			t.Fatalf("expected request max tokens to override default, got %+v", options)
		}
		_, _ = responseWriter.Write([]byte(`{"finishReason":"stop","provider":"llama.cpp","model":"gemma","selectedBackend":"device","message":{"role":"assistant","content":"done"}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:      server.URL,
		AuthKey:       "installation-key",
		ModelName:     "gemma",
		ExecutionMode: "device",
		GenerationOptions: GenerationOptions{
			Seed:        &defaultSeed,
			Temperature: &defaultTemperature,
			MaxTokens:   &defaultMaxTokens,
		},
	})
	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{
		GenerationOptions: GenerationOptions{Seed: &requestSeed, MaxTokens: &requestMaxTokens},
	})
	if errorValue != nil {
		t.Fatalf("expected chat completion response: %v", errorValue)
	}
	if response.Message.Content != "done" {
		t.Fatalf("expected chat response, got %+v", response)
	}
}

func TestLLMDClientGenerateChatCompletionReturnsNativeToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = responseWriter.Write([]byte(`{"finishReason":"tool_calls","provider":"llama.cpp","model":"gemma","selectedBackend":"device","message":{"role":"assistant","toolCalls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"status\"}"}}]}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key"})
	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []ChatCompletionMessage{{Role: "user", Content: "check"}},
	})
	if errorValue != nil {
		t.Fatalf("expected native tool call response: %v", errorValue)
	}
	if response.FinishReason != "tool_calls" || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one native tool call, got %+v", response)
	}
	toolCall := response.Message.ToolCalls[0]
	if toolCall.ID != "call-1" || toolCall.Function.Name != "lookup" || toolCall.Function.Arguments != `{"query":"status"}` {
		t.Fatalf("unexpected native tool call: %+v", toolCall)
	}
}

func TestLLMDClientValidatesChatCompletionResponseContract(t *testing.T) {
	toolCall := ChatCompletionToolCall{
		ID:   "call-1",
		Type: "function",
		Function: ChatCompletionToolCallFunction{
			Name:      "lookup",
			Arguments: `{"city":"Seoul"}`,
		},
	}
	testCases := []struct {
		name         string
		finishReason string
		message      ChatCompletionMessage
		isValid      bool
	}{
		{name: "stop", finishReason: "stop", message: ChatCompletionMessage{Role: "assistant", Content: "done"}, isValid: true},
		{name: "length", finishReason: "length", message: ChatCompletionMessage{Role: "assistant", Content: "partial"}, isValid: true},
		{name: "tool calls", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant", ToolCalls: []ChatCompletionToolCall{toolCall}}, isValid: true},
		{name: "content filter", finishReason: "content_filter", message: ChatCompletionMessage{Role: "assistant", Content: "filtered"}, isValid: true},
		{name: "error", finishReason: "error", message: ChatCompletionMessage{Role: "assistant", Content: "failed"}, isValid: true},
		{name: "other", finishReason: "other", message: ChatCompletionMessage{Role: "assistant", Content: "other"}, isValid: true},
		{name: "unknown", finishReason: "unknown", message: ChatCompletionMessage{Role: "assistant", Content: "unknown"}, isValid: true},
		{name: "unrecognized finish reason", finishReason: "paused", message: ChatCompletionMessage{Role: "assistant", Content: "paused"}},
		{name: "non assistant message", finishReason: "stop", message: ChatCompletionMessage{Role: "tool", Content: "done"}},
		{name: "tool calls without calls", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant"}},
		{name: "empty tool call id", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant", ToolCalls: []ChatCompletionToolCall{{ID: " ", Type: "function", Function: toolCall.Function}}}},
		{name: "duplicate tool call id", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant", ToolCalls: []ChatCompletionToolCall{toolCall, toolCall}}},
		{name: "empty tool call name", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant", ToolCalls: []ChatCompletionToolCall{{ID: toolCall.ID, Type: "function", Function: ChatCompletionToolCallFunction{Arguments: toolCall.Function.Arguments}}}}},
		{name: "array tool call arguments", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant", ToolCalls: []ChatCompletionToolCall{{ID: toolCall.ID, Type: "function", Function: ChatCompletionToolCallFunction{Name: toolCall.Function.Name, Arguments: "[]"}}}}},
		{name: "invalid tool call arguments", finishReason: "tool_calls", message: ChatCompletionMessage{Role: "assistant", ToolCalls: []ChatCompletionToolCall{{ID: toolCall.ID, Type: "function", Function: ChatCompletionToolCallFunction{Name: toolCall.Function.Name, Arguments: "{invalid"}}}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			responseBody, errorValue := json.Marshal(ChatCompletionResponse{
				FinishReason:    testCase.finishReason,
				ProviderName:    "provider",
				ModelName:       "model",
				Message:         testCase.message,
				SelectedBackend: "device",
			})
			if errorValue != nil {
				t.Fatalf("expected response body to marshal: %v", errorValue)
			}
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				_, _ = responseWriter.Write(responseBody)
			}))
			defer server.Close()
			client := NewLLMDClient(LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key"})
			_, errorValue = client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
			if testCase.isValid && errorValue != nil {
				t.Fatalf("expected valid chat response: %v", errorValue)
			}
			if !testCase.isValid && errorValue == nil {
				t.Fatal("expected invalid chat response to be rejected")
			}
		})
	}
}

func TestLLMDClientGenerateChatCompletionPropagatesCancellationWithoutFallback(t *testing.T) {
	requestStarted := make(chan struct{})
	httpClient := llmdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{ProviderName: "legacy"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:     "http://blueclaw-llmd",
		AuthKey:      "installation-key",
		TextProvider: fallbackProvider,
	})
	client.HTTPClient = httpClient
	responseContext, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, errorValue := client.GenerateChatCompletion(responseContext, ChatCompletionRequest{})
		result <- errorValue
	}()
	<-requestStarted
	cancel()
	if errorValue := <-result; !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", errorValue)
	}
	if fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected no fallback after cancellation, got %d calls", fallbackProvider.chatCallCount)
	}
}

func TestLLMDClientRecoveryChatUsesAutoThenDeviceExecutionModes(t *testing.T) {
	receivedExecutionModes := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var receivedDocument llmdChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected chat request document to decode: %v", errorValue)
		}
		receivedExecutionModes = append(receivedExecutionModes, receivedDocument.ExecutionMode)
		if receivedDocument.ExecutionMode == "auto" {
			responseWriter.WriteHeader(http.StatusServiceUnavailable)
			_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_unavailable","message":"remote unavailable","allowLegacyFallback":true}}`))
			return
		}
		_, _ = responseWriter.Write([]byte(`{"finishReason":"stop","provider":"llmd","model":"gemma","selectedBackend":"device","message":{"role":"assistant","content":"device recovery chat"}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key", ExecutionMode: "remote", ModelName: "gemma"})
	response, errorValue := client.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "device recovery chat" {
		t.Fatalf("expected device recovery chat, got %+v, %v", response, errorValue)
	}
	if strings.Join(receivedExecutionModes, ",") != "auto,device" {
		t.Fatalf("expected auto then device execution modes, got %+v", receivedExecutionModes)
	}
}

func TestLLMDClientLocalOnlyRecoveryChatUsesDeviceExecutionMode(t *testing.T) {
	receivedExecutionMode := ""
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var receivedDocument llmdChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected chat request document to decode: %v", errorValue)
		}
		receivedExecutionMode = receivedDocument.ExecutionMode
		_, _ = responseWriter.Write([]byte(`{"finishReason":"stop","provider":"llmd","model":"gemma","selectedBackend":"device","message":{"role":"assistant","content":"local recovery chat"}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key", LocalOnly: true, ExecutionMode: "auto", ModelName: "gemma"})
	response, errorValue := client.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "local recovery chat" {
		t.Fatalf("expected local recovery chat, got %+v, %v", response, errorValue)
	}
	if receivedExecutionMode != "device" {
		t.Fatalf("expected device execution mode, got %q", receivedExecutionMode)
	}
}

func TestLLMDClientLocalRecoveryChatDoesNotUseTextProviderFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{
		FinishReason: "stop",
		Message:      ChatCompletionMessage{Role: "assistant", Content: "remote fallback"},
	}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:     server.URL,
		AuthKey:      "installation-key",
		TextProvider: fallbackProvider,
	})

	_, errorValue := client.GenerateLocalRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil {
		t.Fatal("expected local recovery chat failure")
	}
	if fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected no text provider fallback, got %d calls", fallbackProvider.chatCallCount)
	}
}

func TestLLMDClientDeviceRecoveryChatDoesNotUseTextProviderFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var receivedDocument llmdChatCompletionRequestDocument
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedDocument); errorValue != nil {
			t.Fatalf("expected chat request document to decode: %v", errorValue)
		}
		if receivedDocument.ExecutionMode != "device" {
			t.Fatalf("expected device execution mode, got %q", receivedDocument.ExecutionMode)
		}
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{
		FinishReason: "stop",
		Message:      ChatCompletionMessage{Role: "assistant", Content: "remote fallback"},
	}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:      server.URL,
		AuthKey:       "installation-key",
		ExecutionMode: "device",
		TextProvider:  fallbackProvider,
	})

	_, errorValue := client.GenerateRecoveryChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil {
		t.Fatal("expected device recovery chat failure")
	}
	if fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected no text provider fallback, got %d calls", fallbackProvider.chatCallCount)
	}
}

func TestLLMDClientGenerateChatCompletionUsesLoopbackBridgeWithoutGuestCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("expected no guest authorization header, got %q", request.Header.Get("Authorization"))
		}
		if request.URL.Path != "/_internkim/llmd/v1/llm/chat" {
			t.Fatalf("unexpected LLMD bridge path %q", request.URL.Path)
		}
		_, _ = responseWriter.Write([]byte(`{"finishReason":"stop","provider":"openrouter","model":"remote-model","selectedBackend":"remote","message":{"role":"assistant","content":"done"}}`))
	}))
	defer server.Close()

	client := NewLLMDClient(LLMDClientConfiguration{Endpoint: llmdLoopbackBridgeEndpoint, ModelName: "remote-model"})
	client.HTTPClient = llmdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
		request.URL.Scheme = "http"
		request.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultClient.Do(request)
	})
	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "done" {
		t.Fatalf("expected bridge chat response, got %+v, %v", response, errorValue)
	}
}

func TestLLMDClientGenerateChatCompletionRejectsRemoteResultInLocalOnlyMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = responseWriter.Write([]byte(`{"finishReason":"stop","provider":"openrouter","model":"remote-model","selectedBackend":"remote","message":{"role":"assistant","content":"done"}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{ProviderName: "legacy"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:     server.URL,
		AuthKey:      "installation-key",
		LocalOnly:    true,
		TextProvider: fallbackProvider,
	})
	_, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil || errorValue.Error() != "llmd remote chat response is forbidden in local-only mode" {
		t.Fatalf("expected local-only remote rejection, got %v", errorValue)
	}
	if fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected no local-only fallback, got %d calls", fallbackProvider.chatCallCount)
	}
}

func TestLLMDClientGenerateChatCompletionDoesNotFallbackOnBridgeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"llmd_bridge_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{ProviderName: "legacy", Message: ChatCompletionMessage{Role: "assistant", Content: "done"}}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:     server.URL,
		AuthKey:      "installation-key",
		TextProvider: fallbackProvider,
	})
	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil || response.Transport != "llmd" {
		t.Fatalf("expected LLMD bridge failure, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected no bridge fallback call, got %d", fallbackProvider.chatCallCount)
	}
}

func TestLLMDClientGenerateChatCompletionDoesNotFallbackOnTransportFailure(t *testing.T) {
	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{
		ProviderName: "legacy",
		Message:      ChatCompletionMessage{Role: "assistant", Content: "done"},
	}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:     "http://blueclaw-llmd",
		AuthKey:      "installation-key",
		TextProvider: fallbackProvider,
	})
	client.HTTPClient = llmdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("llmd connection failed")
	})

	response, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil || response.Transport != "llmd" {
		t.Fatalf("expected LLMD transport failure, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected no transport fallback call, got %d", fallbackProvider.chatCallCount)
	}
}

func TestLLMDClientGenerateChatCompletionDoesNotFallbackOnProviderFailure(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		code       string
	}{
		{name: "rate limited", statusCode: http.StatusTooManyRequests, code: "provider_rate_limited"},
		{name: "unavailable", statusCode: http.StatusServiceUnavailable, code: "provider_unavailable"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(testCase.statusCode)
				_, _ = responseWriter.Write([]byte(`{"error":{"code":"` + testCase.code + `","allowLegacyFallback":true}}`))
			}))
			defer server.Close()

			fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{ProviderName: "legacy"}}
			client := NewLLMDClient(LLMDClientConfiguration{
				Endpoint:     server.URL,
				AuthKey:      "installation-key",
				TextProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
			if errorValue == nil || fallbackProvider.chatCallCount != 0 {
				t.Fatalf("expected provider failure without fallback, got %v and %d calls", errorValue, fallbackProvider.chatCallCount)
			}
		})
	}
}

func TestLLMDClientGenerateChatCompletionDoesNotFallbackOnNonretryableError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusBadGateway)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_response_invalid","allowLegacyFallback":false,"diagnostic":{"category":"json_parse"}}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{chatResponse: ChatCompletionResponse{ProviderName: "legacy"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:     server.URL,
		AuthKey:      "installation-key",
		TextProvider: fallbackProvider,
	})
	_, errorValue := client.GenerateChatCompletion(context.Background(), ChatCompletionRequest{})
	if errorValue == nil || fallbackProvider.chatCallCount != 0 {
		t.Fatalf("expected nonretryable LLMD error without fallback, got %v and %d calls", errorValue, fallbackProvider.chatCallCount)
	}
	diagnostic, hasDiagnostic := StructuredOutputDiagnosticFromError(errorValue)
	if !hasDiagnostic || diagnostic.Category != StructuredOutputDiagnosticJSONParse {
		t.Fatalf("expected safe chat diagnostic, got %+v, available=%t", diagnostic, hasDiagnostic)
	}
}

func TestLLMDClientRejectsInvalidSuccessfulResponse(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{name: "empty response", body: `{}`},
		{name: "unknown backend", body: `{"provider":"llmd","model":"model","content":"{}","selectedBackend":"unknown","finishReason":"stop"}`},
		{name: "non-stop finish", body: `{"provider":"llmd","model":"model","content":"{}","selectedBackend":"device","finishReason":"length"}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				_, _ = responseWriter.Write([]byte(testCase.body))
			}))
			defer server.Close()
			fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewLLMDClient(LLMDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected invalid LLMD success without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestLLMDClientDoesNotFallbackOnContractFailure(t *testing.T) {
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
			fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewLLMDClient(LLMDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected LLMD contract failure without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestLLMDClientParsesClosedStructuredOutputDiagnostics(t *testing.T) {
	testCases := []struct {
		name               string
		diagnosticDocument string
		expectedDiagnostic StructuredOutputDiagnostic
	}{
		{name: "json parse", diagnosticDocument: `{"category":"json_parse"}`, expectedDiagnostic: StructuredOutputDiagnostic{Category: StructuredOutputDiagnosticJSONParse}},
		{name: "finish reason", diagnosticDocument: `{"category":"finish_reason","finishReason":"length"}`, expectedDiagnostic: StructuredOutputDiagnostic{Category: StructuredOutputDiagnosticFinishReason, FinishReason: StructuredOutputDiagnosticFinishLength}},
		{
			name:               "safe tool validation",
			diagnosticDocument: `{"category":"schema_validation","toolName":"task.add","validationIssues":[{"fieldPath":"/prompt","code":"required"},{"fieldPath":"/","code":"additional_property"}],"repairStatus":"failed"}`,
			expectedDiagnostic: StructuredOutputDiagnostic{
				Category:     StructuredOutputDiagnosticSchemaValidation,
				ToolName:     "task.add",
				RepairStatus: StructuredOutputRepairFailed,
				ValidationIssues: []StructuredOutputValidationIssue{
					{FieldPath: "/prompt", Code: StructuredOutputValidationRequired},
					{FieldPath: "/", Code: StructuredOutputValidationAdditionalProperty},
				},
			},
		},
		{name: "unknown category", diagnosticDocument: `{"category":"generated_content"}`},
		{name: "unknown finish reason", diagnosticDocument: `{"category":"finish_reason","finishReason":"unfinished"}`},
		{name: "misplaced finish reason", diagnosticDocument: `{"category":"schema_validation","finishReason":"length"}`},
		{name: "unknown field", diagnosticDocument: `{"category":"json_parse","generatedContent":"private"}`},
		{name: "content-like tool name", diagnosticDocument: `{"category":"schema_validation","toolName":"task.add with private content"}`},
		{name: "unknown validation code", diagnosticDocument: `{"category":"schema_validation","validationIssues":[{"fieldPath":"/prompt","code":"provider_message"}]}`},
		{name: "content-like field path", diagnosticDocument: `{"category":"schema_validation","validationIssues":[{"fieldPath":"/prompt contains private content","code":"required"}]}`},
		{name: "misplaced validation issues", diagnosticDocument: `{"category":"json_parse","validationIssues":[{"fieldPath":"/prompt","code":"required"}]}`},
		{name: "unknown repair status", diagnosticDocument: `{"category":"schema_validation","repairStatus":"retried_with_fallback"}`},
		{name: "invalid shape", diagnosticDocument: `[]`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = responseWriter.Write([]byte(`{"error":{"code":"structured_output_invalid","allowLegacyFallback":false,"diagnostic":` + testCase.diagnosticDocument + `}}`))
			}))
			defer server.Close()
			client := NewLLMDClient(LLMDClientConfiguration{Endpoint: server.URL, AuthKey: "installation-key"})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil {
				t.Fatal("expected LLMD contract error")
			}
			diagnostic, hasDiagnostic := StructuredOutputDiagnosticFromError(errorValue)
			if testCase.expectedDiagnostic.Category == "" {
				if hasDiagnostic {
					t.Fatalf("expected malformed diagnostic to be ignored, got %+v", diagnostic)
				}
				return
			}
			if !hasDiagnostic || !reflect.DeepEqual(diagnostic, testCase.expectedDiagnostic) {
				t.Fatalf("unexpected diagnostic: %+v, available=%t", diagnostic, hasDiagnostic)
			}
		})
	}
}

func TestLLMDClientDoesNotFallbackOnUntrustedErrorEnvelope(t *testing.T) {
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
			fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewLLMDClient(LLMDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected LLMD failure without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestLLMDClientDoesNotFallbackInLocalOnlyMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"provider_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()
	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:                   server.URL,
		AuthKey:                    "installation-key",
		LocalOnly:                  true,
		StructuredFallbackProvider: fallbackProvider,
	})

	_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
		t.Fatalf("expected local-only LLMD failure without legacy fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
	}
}

func TestLLMDClientRejectsRemoteResultInLocalOnlyMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = responseWriter.Write([]byte(`{"provider":"openrouter","model":"remote-model","content":"{}","selectedBackend":"remote","finishReason":"stop"}`))
	}))
	defer server.Close()
	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:                   server.URL,
		AuthKey:                    "installation-key",
		LocalOnly:                  true,
		StructuredFallbackProvider: fallbackProvider,
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue == nil || errorValue.Error() != "llmd remote response is forbidden in local-only mode" {
		t.Fatalf("expected remote LLMD result rejection, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.structuredCallCount != 0 {
		t.Fatalf("expected no local-only legacy fallback, got %d calls", fallbackProvider.structuredCallCount)
	}
}

func TestLLMDClientDoesNotUseDisabledSchemaFallbackInLocalOnlyMode(t *testing.T) {
	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
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

func TestLLMDClientFallsBackWhenResponseBodyReadFails(t *testing.T) {
	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{AuthKey: "installation-key", StructuredFallbackProvider: fallbackProvider})
	client.HTTPClient = llmdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(llmdFailingReader{}),
			Header:     make(http.Header),
		}, nil
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "test", Document: `{"type":"object"}`},
	})
	if errorValue != nil || response.Transport != "capability" || response.ProviderName != "capabilityLLM" || !response.UsedFallback {
		t.Fatalf("expected response read failure fallback, got %+v, %v", response, errorValue)
	}
}

type llmdFailingReader struct{}

func (llmdFailingReader) Read([]byte) (int, error) {
	return 0, errors.New("response read failed")
}

func TestLLMDClientUsesBridgeEndpointWithoutGuestCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("expected no guest authorization header, got %q", request.Header.Get("Authorization"))
		}
		if request.URL.Path != "/_internkim/llmd/v1/llm/structured" {
			t.Fatalf("unexpected LLMD bridge path %q", request.URL.Path)
		}
		_, _ = responseWriter.Write([]byte(`{"provider":"openrouter","model":"test","content":"{}","selectedBackend":"remote","finishReason":"stop"}`))
	}))
	defer server.Close()
	client := NewLLMDClient(LLMDClientConfiguration{Endpoint: llmdLoopbackBridgeEndpoint, ModelName: "test"})
	client.HTTPClient = llmdTestHTTPClient(func(request *http.Request) (*http.Response, error) {
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

func TestLLMDClientDelegatesTextGeneration(t *testing.T) {
	textProvider := &llmdTestLanguageModel{textResponse: "delegated"}
	client := NewLLMDClient(LLMDClientConfiguration{TextProvider: textProvider})

	response, errorValue := client.GenerateResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected delegated text response: %v", errorValue)
	}
	if response != "delegated" || textProvider.textCallCount != 1 {
		t.Fatalf("expected one delegated call, got response %q and count %d", response, textProvider.textCallCount)
	}
}

func TestLLMDClientUsesLegacyProviderOutsideEnabledSchemas(t *testing.T) {
	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
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

func TestLLMDClientDoesNotFallbackOnProviderFailures(t *testing.T) {
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
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(testCase.statusCode)
				_, _ = responseWriter.Write([]byte(`{"error":{"code":"` + testCase.code + `","allowLegacyFallback":true}}`))
			}))
			defer server.Close()
			fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
			client := NewLLMDClient(LLMDClientConfiguration{
				Endpoint:                   server.URL,
				AuthKey:                    "installation-key",
				StructuredFallbackProvider: fallbackProvider,
				StructuredSchemaNames:      []string{"blueclaw_agent_turn_action"},
			})

			_, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
				StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_agent_turn_action", Document: `{"type":"object"}`},
			})
			if errorValue == nil || fallbackProvider.structuredCallCount != 0 {
				t.Fatalf("expected provider failure without fallback, got %v and %d calls", errorValue, fallbackProvider.structuredCallCount)
			}
		})
	}
}

func TestLLMDClientFallsBackWhenBridgeIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"llmd_bridge_unavailable","allowLegacyFallback":true,"diagnostic":[]}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:                   server.URL,
		AuthKey:                    "installation-key",
		StructuredFallbackProvider: fallbackProvider,
		StructuredSchemaNames:      []string{"blueclaw_agent_turn_action"},
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_agent_turn_action", Document: `{"type":"object"}`},
	})
	if errorValue != nil || response.ProviderName != "capabilityLLM" || !response.UsedFallback {
		t.Fatalf("expected bridge fallback response, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.structuredCallCount != 1 {
		t.Fatalf("expected one bridge fallback call, got %d", fallbackProvider.structuredCallCount)
	}
}

func TestLLMDClientAuthoritativeStructuredResponseReturnsBridgeErrorWithoutFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"llmd_bridge_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:                        server.URL,
		AuthKey:                         "installation-key",
		StructuredFallbackProvider:      fallbackProvider,
		StructuredSchemaNames:           []string{"blueclaw_agent_turn_action"},
		IsStructuredOutputAuthoritative: true,
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_agent_turn_action", Document: `{"type":"object"}`},
	})
	httpError, isHTTPError := asLLMDHTTPError(errorValue)
	if !isHTTPError || httpError.StatusCode != http.StatusServiceUnavailable || httpError.Code != "llmd_bridge_unavailable" {
		t.Fatalf("expected original LLMD bridge error, got %+v, %v", response, errorValue)
	}
	if response.Transport != "llmd" {
		t.Fatalf("expected LLMD transport, got %+v", response)
	}
	if fallbackProvider.structuredCallCount != 0 {
		t.Fatalf("expected no authoritative bridge fallback call, got %d", fallbackProvider.structuredCallCount)
	}
}

func TestLLMDClientUsesMigrationFallbackForDisabledSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = responseWriter.Write([]byte(`{"error":{"code":"llmd_bridge_unavailable","allowLegacyFallback":true}}`))
	}))
	defer server.Close()

	fallbackProvider := &llmdTestLanguageModel{structuredResponse: StructuredResponse{ProviderName: "capabilityLLM"}}
	client := NewLLMDClient(LLMDClientConfiguration{
		Endpoint:                        server.URL,
		AuthKey:                         "installation-key",
		StructuredFallbackProvider:      fallbackProvider,
		StructuredSchemaNames:           []string{"blueclaw_agent_turn_action"},
		IsStructuredOutputAuthoritative: true,
	})

	response, errorValue := client.GenerateStructuredResponse(context.Background(), StructuredResponseRequest{
		StructuredOutputSchema: StructuredOutputSchema{Name: "blueclaw_turn_router", Document: `{"type":"object"}`},
	})
	if errorValue != nil || response.ProviderName != "capabilityLLM" || response.Transport != "capability" {
		t.Fatalf("expected disabled-schema migration fallback, got %+v, %v", response, errorValue)
	}
	if fallbackProvider.structuredCallCount != 1 {
		t.Fatalf("expected one disabled-schema fallback call, got %d", fallbackProvider.structuredCallCount)
	}
}

type llmdTestHTTPClient func(*http.Request) (*http.Response, error)

func (client llmdTestHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client(request)
}

type llmdTestLanguageModel struct {
	textResponse        string
	structuredResponse  StructuredResponse
	structuredError     error
	chatResponse        ChatCompletionResponse
	chatError           error
	textCallCount       int
	structuredCallCount int
	chatCallCount       int
	structuredCalled    chan struct{}
}

func (languageModel *llmdTestLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	languageModel.textCallCount++
	return languageModel.textResponse, nil
}

func (languageModel *llmdTestLanguageModel) GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error) {
	languageModel.structuredCallCount++
	if languageModel.structuredCalled != nil {
		languageModel.structuredCalled <- struct{}{}
	}
	return languageModel.structuredResponse, languageModel.structuredError
}

func (languageModel *llmdTestLanguageModel) GenerateChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error) {
	languageModel.chatCallCount++
	return languageModel.chatResponse, languageModel.chatError
}
