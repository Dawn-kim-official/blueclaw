package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultSDKDEndpoint = "http://blueclaw-sdkd"
const sdkdLoopbackBridgeEndpoint = "http://127.0.0.1:18081/_internkim/sdkd"
const sdkdMaximumBodySize = 8 * 1024 * 1024

type sdkdHTTPError struct {
	StatusCode          int
	Code                string
	Message             string
	AllowLegacyFallback bool
}

func (errorValue sdkdHTTPError) Error() string {
	return errorValue.Message
}

type sdkdTransportError struct {
	Cause error
}

func (errorValue sdkdTransportError) Error() string {
	return errorValue.Cause.Error()
}

func (errorValue sdkdTransportError) Unwrap() error {
	return errorValue.Cause
}

type SDKDClientConfiguration struct {
	Endpoint                   string
	UnixSocketPath             string
	AuthKey                    string
	Timeout                    time.Duration
	ModelName                  string
	ExecutionMode              string
	LocalOnly                  bool
	TextProvider               LanguageModelProvider
	GenerationOptions          GenerationOptions
	StructuredFallbackProvider LanguageModelProvider
	StructuredSchemaNames      []string
}

type SDKDClient struct {
	Endpoint   string
	HTTPClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	AuthKey                    string
	ModelName                  string
	ExecutionMode              string
	LocalOnly                  bool
	TextProvider               LanguageModelProvider
	GenerationOptions          GenerationOptions
	StructuredFallbackProvider LanguageModelProvider
	StructuredSchemaNames      []string
}

type sdkdChatCompletionRequestDocument struct {
	Model             string                  `json:"model,omitempty"`
	ExecutionMode     string                  `json:"executionMode"`
	Context           *RequestContext         `json:"context,omitempty"`
	Messages          []ChatCompletionMessage `json:"messages"`
	Tools             []ChatCompletionTool    `json:"tools,omitempty"`
	ToolChoice        json.RawMessage         `json:"toolChoice,omitempty"`
	ParallelToolCalls bool                    `json:"parallelToolCalls"`
}

func NewSDKDClient(configuration SDKDClientConfiguration) SDKDClient {
	endpoint := strings.TrimRight(strings.TrimSpace(configuration.Endpoint), "/")
	if endpoint == "" {
		endpoint = defaultSDKDEndpoint
	}
	httpClient := &http.Client{Timeout: configuration.Timeout}
	if unixSocketPath := strings.TrimSpace(configuration.UnixSocketPath); unixSocketPath != "" {
		httpClient.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", unixSocketPath)
			},
		}
	}
	return SDKDClient{
		Endpoint:                   endpoint,
		HTTPClient:                 httpClient,
		AuthKey:                    strings.TrimSpace(configuration.AuthKey),
		ModelName:                  strings.TrimSpace(configuration.ModelName),
		ExecutionMode:              strings.TrimSpace(configuration.ExecutionMode),
		LocalOnly:                  configuration.LocalOnly,
		TextProvider:               configuration.TextProvider,
		GenerationOptions:          configuration.GenerationOptions,
		StructuredFallbackProvider: configuration.StructuredFallbackProvider,
		StructuredSchemaNames:      append([]string{}, configuration.StructuredSchemaNames...),
	}
}

func (client SDKDClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	if client.TextProvider == nil {
		return "", errors.New("sdkd text provider is not configured")
	}
	return client.TextProvider.GenerateResponse(responseContext, prompt)
}

func (client SDKDClient) GenerateRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	recoveryProvider, isRecoveryProvider := client.TextProvider.(RecoveryResponder)
	if !isRecoveryProvider {
		return client.GenerateResponse(responseContext, prompt)
	}
	return recoveryProvider.GenerateRecoveryResponse(responseContext, prompt)
}

func (client SDKDClient) GenerateLocalRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	localRecoveryProvider, isLocalRecoveryProvider := client.TextProvider.(LocalRecoveryResponder)
	if !isLocalRecoveryProvider {
		return client.GenerateResponse(responseContext, prompt)
	}
	return localRecoveryProvider.GenerateLocalRecoveryResponse(responseContext, prompt)
}

func (client SDKDClient) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	if !client.usesSDKDForSchema(request.StructuredOutputSchema.Name) {
		if client.LocalOnly || client.StructuredFallbackProvider == nil {
			return StructuredResponse{}, errors.New("sdkd structured schema is not enabled")
		}
		return client.StructuredFallbackProvider.GenerateStructuredResponse(responseContext, request)
	}
	response, errorValue := client.generateSDKDStructuredResponse(responseContext, request)
	if errorValue == nil || client.LocalOnly || client.StructuredFallbackProvider == nil || !isRetryableSDKDError(errorValue) {
		return response, errorValue
	}
	fallbackResponse, fallbackError := client.StructuredFallbackProvider.GenerateStructuredResponse(responseContext, request)
	if fallbackError != nil {
		return StructuredResponse{}, fallbackError
	}
	fallbackResponse.UsedFallback = true
	return fallbackResponse, nil
}

func (client SDKDClient) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	response, errorValue := client.generateSDKDChatCompletion(responseContext, request)
	if errorValue == nil || client.LocalOnly || !isRetryableSDKDError(errorValue) {
		return response, errorValue
	}
	if responseContext.Err() != nil {
		return ChatCompletionResponse{}, responseContext.Err()
	}
	fallbackCompleter, isFallbackCompleter := client.TextProvider.(ChatCompleter)
	if !isFallbackCompleter {
		return response, errorValue
	}
	fallbackResponse, fallbackError := fallbackCompleter.GenerateChatCompletion(responseContext, request)
	if fallbackError != nil {
		return ChatCompletionResponse{}, fallbackError
	}
	if fallbackResponse.Message.ToolCalls == nil {
		fallbackResponse.Message.ToolCalls = []ChatCompletionToolCall{}
	}
	fallbackResponse.UsedFallback = true
	return fallbackResponse, nil
}

func (client SDKDClient) generateSDKDChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	if client.HTTPClient == nil {
		return ChatCompletionResponse{}, errors.New("sdkd http client is not configured")
	}
	if client.AuthKey == "" && client.Endpoint != sdkdLoopbackBridgeEndpoint {
		return ChatCompletionResponse{}, errors.New("sdkd auth key is not configured")
	}
	requestDocument := sdkdChatCompletionRequestDocument{
		Model:             client.ModelName,
		ExecutionMode:     client.executionMode(),
		Context:           requestContextPointer(responseContext),
		Messages:          append([]ChatCompletionMessage{}, request.Messages...),
		Tools:             append([]ChatCompletionTool{}, request.Tools...),
		ToolChoice:        append(json.RawMessage{}, request.ToolChoice...),
		ParallelToolCalls: request.ParallelToolCalls,
	}
	var response ChatCompletionResponse
	if errorValue := client.postJSON(responseContext, "/v1/llm/chat", requestDocument, &response); errorValue != nil {
		return ChatCompletionResponse{}, errorValue
	}
	if errorValue := validateSDKDChatCompletionResponse(response, client.LocalOnly); errorValue != nil {
		return ChatCompletionResponse{}, errorValue
	}
	if response.Message.ToolCalls == nil {
		response.Message.ToolCalls = []ChatCompletionToolCall{}
	}
	return response, nil
}

func validateSDKDChatCompletionResponse(response ChatCompletionResponse, isLocalOnly bool) error {
	if strings.TrimSpace(response.ProviderName) == "" || strings.TrimSpace(response.ModelName) == "" {
		return errors.New("sdkd chat response provider and model are required")
	}
	if response.Message.Role != "assistant" {
		return errors.New("sdkd chat response message role must be assistant")
	}
	if response.SelectedBackend != "device" && response.SelectedBackend != "remote" {
		return errors.New("sdkd chat response selected backend is invalid")
	}
	if isLocalOnly && response.SelectedBackend == "remote" {
		return errors.New("sdkd remote chat response is forbidden in local-only mode")
	}
	if !isSDKDChatCompletionFinishReason(response.FinishReason) {
		return errors.New("sdkd chat response finish reason is invalid")
	}
	if response.FinishReason == "tool_calls" && len(response.Message.ToolCalls) == 0 {
		return errors.New("sdkd chat response tool_calls finish reason requires tool calls")
	}
	seenToolCallIDs := make(map[string]struct{}, len(response.Message.ToolCalls))
	for _, toolCall := range response.Message.ToolCalls {
		normalizedToolCallID := strings.TrimSpace(toolCall.ID)
		if normalizedToolCallID == "" {
			return errors.New("sdkd chat response tool call id is required")
		}
		if _, isDuplicate := seenToolCallIDs[normalizedToolCallID]; isDuplicate {
			return errors.New("sdkd chat response tool call id must be unique")
		}
		seenToolCallIDs[normalizedToolCallID] = struct{}{}
		if toolCall.Type != "function" {
			return errors.New("sdkd chat response tool call type is invalid")
		}
		if strings.TrimSpace(toolCall.Function.Name) == "" {
			return errors.New("sdkd chat response tool call name is required")
		}
		if !isJSONDocumentObject(toolCall.Function.Arguments) {
			return errors.New("sdkd chat response tool call arguments must be a JSON object")
		}
	}
	return nil
}

func isSDKDChatCompletionFinishReason(finishReason string) bool {
	switch finishReason {
	case "stop", "length", "tool_calls", "content_filter", "error", "other", "unknown":
		return true
	default:
		return false
	}
}

func isJSONDocumentObject(document string) bool {
	var parsedDocument map[string]any
	if errorValue := json.Unmarshal([]byte(document), &parsedDocument); errorValue != nil {
		return false
	}
	return parsedDocument != nil
}

func (client SDKDClient) generateSDKDStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	if client.HTTPClient == nil {
		return StructuredResponse{}, errors.New("sdkd http client is not configured")
	}
	if client.AuthKey == "" && client.Endpoint != sdkdLoopbackBridgeEndpoint {
		return StructuredResponse{}, errors.New("sdkd auth key is not configured")
	}
	requestDocument, errorValue := client.buildStructuredRequestDocument(responseContext, request)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	var responseDocument capabilityStructuredResponseDocument
	if errorValue = client.postJSON(responseContext, "/v1/llm/structured", requestDocument, &responseDocument); errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	if errorValue = validateSDKDStructuredResponse(responseDocument, client.LocalOnly); errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	return StructuredResponse{
		ProviderName:    strings.TrimSpace(responseDocument.ProviderName),
		ModelName:       strings.TrimSpace(responseDocument.ModelName),
		Content:         responseDocument.Content,
		SelectedBackend: responseDocument.SelectedBackend,
		FinishReason:    responseDocument.FinishReason,
		ConstraintMode:  responseDocument.ConstraintMode,
		Usage:           responseDocument.Usage,
	}, nil
}

func validateSDKDStructuredResponse(response capabilityStructuredResponseDocument, isLocalOnly bool) error {
	if strings.TrimSpace(response.ProviderName) == "" || strings.TrimSpace(response.ModelName) == "" {
		return errors.New("sdkd response provider and model are required")
	}
	if strings.TrimSpace(response.Content) == "" {
		return errors.New("sdkd response content is required")
	}
	if response.SelectedBackend != "device" && response.SelectedBackend != "remote" {
		return errors.New("sdkd response selected backend is invalid")
	}
	if isLocalOnly && response.SelectedBackend == "remote" {
		return errors.New("sdkd remote response is forbidden in local-only mode")
	}
	if response.FinishReason != "stop" {
		return errors.New("sdkd response did not finish successfully")
	}
	return nil
}

func (client SDKDClient) usesSDKDForSchema(schemaName string) bool {
	if len(client.StructuredSchemaNames) == 0 {
		return true
	}
	for _, configuredSchemaName := range client.StructuredSchemaNames {
		if strings.TrimSpace(configuredSchemaName) == strings.TrimSpace(schemaName) {
			return true
		}
	}
	return false
}

func (client SDKDClient) buildStructuredRequestDocument(responseContext context.Context, request StructuredResponseRequest) (capabilityStructuredResponseRequestDocument, error) {
	jsonSchemaDocument, errorValue := normalizeStructuredOutputSchema(request.StructuredOutputSchema)
	if errorValue != nil {
		return capabilityStructuredResponseRequestDocument{}, errorValue
	}
	generationOptions := request.GenerationOptions
	if generationOptions.Seed == nil {
		generationOptions.Seed = client.GenerationOptions.Seed
	}
	if generationOptions.Temperature == nil {
		generationOptions.Temperature = client.GenerationOptions.Temperature
	}
	if generationOptions.MaxTokens == nil {
		generationOptions.MaxTokens = client.GenerationOptions.MaxTokens
	}
	return capabilityStructuredResponseRequestDocument{
		Model:             client.ModelName,
		ExecutionMode:     client.executionMode(),
		Context:           requestContextPointer(responseContext),
		Messages:          append([]Message{}, request.Messages...),
		GenerationOptions: generationOptionsPointer(generationOptions),
		StructuredOutputSchema: capabilityStructuredOutputSchema{
			Name:               request.StructuredOutputSchema.Name,
			Document:           jsonSchemaDocument,
			IsStrictlyEnforced: request.StructuredOutputSchema.IsStrictlyEnforced,
		},
	}, nil
}

func (client SDKDClient) postJSON(responseContext context.Context, path string, requestDocument any, responseDocument any) error {
	requestBody, errorValue := json.Marshal(requestDocument)
	if errorValue != nil {
		return errorValue
	}
	if len(requestBody) > sdkdMaximumBodySize {
		return errors.New("sdkd request exceeds 8 MiB")
	}
	httpRequest, errorValue := http.NewRequestWithContext(
		responseContext,
		http.MethodPost,
		strings.TrimRight(client.Endpoint, "/")+"/"+strings.TrimLeft(path, "/"),
		bytes.NewReader(requestBody),
	)
	if errorValue != nil {
		return errorValue
	}
	if client.AuthKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+client.AuthKey)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, errorValue := client.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return sdkdTransportError{Cause: errorValue}
	}
	defer httpResponse.Body.Close()
	responseBody, errorValue := io.ReadAll(io.LimitReader(httpResponse.Body, sdkdMaximumBodySize+1))
	if errorValue != nil {
		return sdkdTransportError{Cause: errorValue}
	}
	if len(responseBody) > sdkdMaximumBodySize {
		return errors.New("sdkd response exceeds 8 MiB")
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		var errorDocument struct {
			Error struct {
				Code                string `json:"code"`
				AllowLegacyFallback bool   `json:"allowLegacyFallback"`
			} `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &errorDocument)
		return sdkdHTTPError{
			StatusCode:          httpResponse.StatusCode,
			Code:                strings.TrimSpace(errorDocument.Error.Code),
			Message:             strings.TrimSpace(string(responseBody)),
			AllowLegacyFallback: errorDocument.Error.AllowLegacyFallback,
		}
	}
	return json.Unmarshal(responseBody, responseDocument)
}

func isRetryableSDKDError(errorValue error) bool {
	if errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) {
		return false
	}
	var transportError sdkdTransportError
	if errors.As(errorValue, &transportError) {
		return true
	}
	var httpError sdkdHTTPError
	if !errors.As(errorValue, &httpError) {
		return false
	}
	if !httpError.AllowLegacyFallback {
		return false
	}
	switch httpError.Code {
	case "provider_rate_limited":
		return httpError.StatusCode == http.StatusTooManyRequests
	case "provider_unavailable":
		return httpError.StatusCode >= http.StatusInternalServerError
	case "sdkd_bridge_unavailable":
		return httpError.StatusCode == http.StatusServiceUnavailable
	default:
		return false
	}
}

func (client SDKDClient) executionMode() string {
	if client.ExecutionMode == "" {
		return "auto"
	}
	return client.ExecutionMode
}
