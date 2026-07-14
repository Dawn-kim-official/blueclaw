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

type SDKDClientConfiguration struct {
	Endpoint                   string
	UnixSocketPath             string
	AuthKey                    string
	Timeout                    time.Duration
	ModelName                  string
	ExecutionMode              string
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
	TextProvider               LanguageModelProvider
	GenerationOptions          GenerationOptions
	StructuredFallbackProvider LanguageModelProvider
	StructuredSchemaNames      []string
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
		if client.StructuredFallbackProvider == nil {
			return StructuredResponse{}, errors.New("sdkd structured schema is not enabled")
		}
		return client.StructuredFallbackProvider.GenerateStructuredResponse(responseContext, request)
	}
	response, errorValue := client.generateSDKDStructuredResponse(responseContext, request)
	if errorValue == nil || client.StructuredFallbackProvider == nil {
		return response, errorValue
	}
	fallbackResponse, fallbackError := client.StructuredFallbackProvider.GenerateStructuredResponse(responseContext, request)
	if fallbackError != nil {
		return StructuredResponse{}, fallbackError
	}
	fallbackResponse.UsedFallback = true
	return fallbackResponse, nil
}

func (client SDKDClient) generateSDKDStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	if client.HTTPClient == nil {
		return StructuredResponse{}, errors.New("sdkd http client is not configured")
	}
	if client.AuthKey == "" {
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
	httpRequest, errorValue := http.NewRequestWithContext(
		responseContext,
		http.MethodPost,
		strings.TrimRight(client.Endpoint, "/")+"/"+strings.TrimLeft(path, "/"),
		bytes.NewReader(requestBody),
	)
	if errorValue != nil {
		return errorValue
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.AuthKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, errorValue := client.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return errorValue
	}
	defer httpResponse.Body.Close()
	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return errorValue
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return errors.New(strings.TrimSpace(string(responseBody)))
	}
	return json.Unmarshal(responseBody, responseDocument)
}

func (client SDKDClient) executionMode() string {
	if client.ExecutionMode == "" {
		return "auto"
	}
	return client.ExecutionMode
}
