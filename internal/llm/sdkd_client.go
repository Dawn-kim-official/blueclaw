package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
)

const defaultSDKDEndpoint = "http://blueclaw-sdkd"
const sdkdLoopbackBridgeEndpoint = "http://127.0.0.1:18081/_internkim/sdkd"
const sdkdMaximumBodySize = 8 * 1024 * 1024

type sdkdHTTPError struct {
	StatusCode          int
	Code                string
	Message             string
	AllowLegacyFallback bool
	Diagnostic          StructuredOutputDiagnostic
}

func (errorValue sdkdHTTPError) Error() string {
	return errorValue.Message
}

type structuredOutputCorrectionError interface {
	error
	StructuredOutputCorrection() (StructuredOutputCorrection, bool)
}

type StructuredOutputDiagnosticCategory string

type StructuredOutputFinishReason string

type StructuredOutputValidationCode string

type StructuredOutputRepairStatus string

const (
	StructuredOutputDiagnosticJSONParse        StructuredOutputDiagnosticCategory = "json_parse"
	StructuredOutputDiagnosticSchemaValidation StructuredOutputDiagnosticCategory = "schema_validation"
	StructuredOutputDiagnosticFinishReason     StructuredOutputDiagnosticCategory = "finish_reason"
	StructuredOutputDiagnosticToolCallContract StructuredOutputDiagnosticCategory = "tool_call_contract"
	StructuredOutputDiagnosticSerialization    StructuredOutputDiagnosticCategory = "serialization"
)

const (
	StructuredOutputDiagnosticFinishStop          StructuredOutputFinishReason = "stop"
	StructuredOutputDiagnosticFinishLength        StructuredOutputFinishReason = "length"
	StructuredOutputDiagnosticFinishToolCalls     StructuredOutputFinishReason = "tool_calls"
	StructuredOutputDiagnosticFinishContentFilter StructuredOutputFinishReason = "content_filter"
	StructuredOutputDiagnosticFinishError         StructuredOutputFinishReason = "error"
	StructuredOutputDiagnosticFinishOther         StructuredOutputFinishReason = "other"
	StructuredOutputDiagnosticFinishUnknown       StructuredOutputFinishReason = "unknown"
)

const (
	StructuredOutputValidationRequired           StructuredOutputValidationCode = "required"
	StructuredOutputValidationAdditionalProperty StructuredOutputValidationCode = "additional_property"
	StructuredOutputValidationType               StructuredOutputValidationCode = "type"
	StructuredOutputValidationOther              StructuredOutputValidationCode = "other"
)

const (
	StructuredOutputRepairNotAttempted StructuredOutputRepairStatus = "not_attempted"
	StructuredOutputRepairFailed       StructuredOutputRepairStatus = "failed"
)

type StructuredOutputValidationIssue struct {
	FieldPath string                         `json:"fieldPath"`
	Code      StructuredOutputValidationCode `json:"code"`
}

type StructuredOutputDiagnostic struct {
	Category         StructuredOutputDiagnosticCategory
	FinishReason     StructuredOutputFinishReason
	ToolName         string
	ValidationIssues []StructuredOutputValidationIssue
	RepairStatus     StructuredOutputRepairStatus
}

type StructuredOutputCorrection struct {
	Code       string
	Diagnostic StructuredOutputDiagnostic
}

func (errorValue sdkdHTTPError) StructuredOutputCorrection() (StructuredOutputCorrection, bool) {
	if errorValue.AllowLegacyFallback {
		return StructuredOutputCorrection{}, false
	}
	return StructuredOutputCorrection{Code: errorValue.Code, Diagnostic: errorValue.Diagnostic}, true
}

func StructuredOutputCorrectionFromError(errorValue error) (StructuredOutputCorrection, bool) {
	var correctionError structuredOutputCorrectionError
	if !errors.As(errorValue, &correctionError) {
		return StructuredOutputCorrection{}, false
	}
	correction, isCorrectable := correctionError.StructuredOutputCorrection()
	if !isCorrectable || !isCorrectableStructuredOutputCode(correction.Code) || !isCorrectableStructuredOutputCategory(correction.Diagnostic.Category) {
		return StructuredOutputCorrection{}, false
	}
	return correction, true
}

func isCorrectableStructuredOutputCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "provider_response_invalid", "structured_output_invalid":
		return true
	default:
		return false
	}
}

func isCorrectableStructuredOutputCategory(category StructuredOutputDiagnosticCategory) bool {
	switch category {
	case StructuredOutputDiagnosticJSONParse,
		StructuredOutputDiagnosticSchemaValidation,
		StructuredOutputDiagnosticFinishReason,
		StructuredOutputDiagnosticToolCallContract:
		return true
	default:
		return false
	}
}

func StructuredOutputDiagnosticFromError(errorValue error) (StructuredOutputDiagnostic, bool) {
	httpError, isHTTPError := asSDKDHTTPError(errorValue)
	if !isHTTPError || httpError.Diagnostic.Category == "" {
		return StructuredOutputDiagnostic{}, false
	}
	return httpError.Diagnostic, true
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
	Endpoint                        string
	UnixSocketPath                  string
	AuthKey                         string
	ModelName                       string
	ExecutionMode                   string
	LocalOnly                       bool
	TextProvider                    LanguageModelProvider
	GenerationOptions               GenerationOptions
	StructuredFallbackProvider      LanguageModelProvider
	StructuredSchemaNames           []string
	IsStructuredOutputAuthoritative bool
}

type SDKDClient struct {
	Endpoint   string
	HTTPClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	AuthKey                         string
	ModelName                       string
	ExecutionMode                   string
	LocalOnly                       bool
	TextProvider                    LanguageModelProvider
	GenerationOptions               GenerationOptions
	StructuredFallbackProvider      LanguageModelProvider
	StructuredSchemaNames           []string
	IsStructuredOutputAuthoritative bool
}

type sdkdChatCompletionRequestDocument struct {
	Model             string                  `json:"model,omitempty"`
	ExecutionMode     string                  `json:"executionMode"`
	Context           *RequestContext         `json:"context,omitempty"`
	Messages          []ChatCompletionMessage `json:"messages"`
	Tools             []ChatCompletionTool    `json:"tools,omitempty"`
	ToolChoice        json.RawMessage         `json:"toolChoice,omitempty"`
	ParallelToolCalls bool                    `json:"parallelToolCalls"`
	GenerationOptions *GenerationOptions      `json:"generationOptions,omitempty"`
}

func NewSDKDClient(configuration SDKDClientConfiguration) SDKDClient {
	endpoint := strings.TrimRight(strings.TrimSpace(configuration.Endpoint), "/")
	if endpoint == "" {
		endpoint = defaultSDKDEndpoint
	}
	httpClient := &http.Client{}
	if unixSocketPath := strings.TrimSpace(configuration.UnixSocketPath); unixSocketPath != "" {
		httpClient.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", unixSocketPath)
			},
		}
	}
	return SDKDClient{
		Endpoint:                        endpoint,
		HTTPClient:                      httpClient,
		AuthKey:                         strings.TrimSpace(configuration.AuthKey),
		ModelName:                       strings.TrimSpace(configuration.ModelName),
		ExecutionMode:                   strings.TrimSpace(configuration.ExecutionMode),
		LocalOnly:                       configuration.LocalOnly,
		TextProvider:                    configuration.TextProvider,
		GenerationOptions:               configuration.GenerationOptions,
		StructuredFallbackProvider:      configuration.StructuredFallbackProvider,
		StructuredSchemaNames:           append([]string{}, configuration.StructuredSchemaNames...),
		IsStructuredOutputAuthoritative: configuration.IsStructuredOutputAuthoritative,
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

func (client SDKDClient) GenerateRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	if client.LocalOnly || client.executionMode() == "device" {
		return client.generateSDKDRecoveryChatAttempt(responseContext, request, "device")
	}
	response, errorValue := client.generateSDKDRecoveryChatAttempt(responseContext, request, "auto")
	if errorValue == nil {
		return response, nil
	}
	if contextError := responseContext.Err(); contextError != nil {
		return response, contextError
	}
	if !shouldRetrySDKDRecovery(errorValue) {
		return response, errorValue
	}
	return client.generateSDKDRecoveryChatAttempt(responseContext, request, "device")
}

func (client SDKDClient) GenerateLocalRecoveryChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	return client.generateSDKDRecoveryChatAttempt(responseContext, request, "device")
}

func (client SDKDClient) generateSDKDRecoveryChatAttempt(responseContext context.Context, request ChatCompletionRequest, executionMode string) (ChatCompletionResponse, error) {
	attemptContext, cancelAttempt := recoveryAttemptContext(responseContext)
	response, errorValue := client.generateSDKDChatCompletion(attemptContext, request, executionMode)
	cancelAttempt()
	if response.Transport == "" {
		response.Transport = "sdkd"
	}
	if errorValue != nil {
		return response, errorValue
	}
	if executionMode == "device" && response.SelectedBackend != "device" {
		return response, errors.New("device recovery chat returned a non-device backend")
	}
	_, errorValue = RecoveryChatCompletionText(response)
	return response, errorValue
}

func (client SDKDClient) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	if !client.usesSDKDForSchema(request.StructuredOutputSchema.Name) {
		if client.LocalOnly || client.StructuredFallbackProvider == nil {
			return StructuredResponse{}, errors.New("sdkd structured schema is not enabled")
		}
		response, errorValue := client.StructuredFallbackProvider.GenerateStructuredResponse(responseContext, request)
		response.Transport = "capability"
		return response, errorValue
	}
	response, errorValue := client.generateSDKDStructuredResponse(responseContext, request)
	if response.Transport == "" {
		response.Transport = "sdkd"
	}
	if errorValue == nil || !client.canUseStructuredFallback(errorValue) {
		return response, errorValue
	}
	fallbackResponse, fallbackError := client.StructuredFallbackProvider.GenerateStructuredResponse(responseContext, request)
	if fallbackError != nil {
		return StructuredResponse{}, fallbackError
	}
	fallbackResponse.UsedFallback = true
	fallbackResponse.Transport = "capability"
	return fallbackResponse, nil
}

func (client SDKDClient) canUseStructuredFallback(errorValue error) bool {
	return !client.LocalOnly &&
		!client.IsStructuredOutputAuthoritative &&
		client.StructuredFallbackProvider != nil &&
		canUseLegacySDKDFallback(errorValue)
}

func (client SDKDClient) GenerateChatCompletion(responseContext context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	response, errorValue := client.generateSDKDChatCompletion(responseContext, request, client.executionMode())
	if response.Transport == "" {
		response.Transport = "sdkd"
	}
	return response, errorValue
}

func (client SDKDClient) generateSDKDChatCompletion(responseContext context.Context, request ChatCompletionRequest, executionMode string) (ChatCompletionResponse, error) {
	if client.HTTPClient == nil {
		return ChatCompletionResponse{}, errors.New("sdkd http client is not configured")
	}
	if client.AuthKey == "" && client.Endpoint != sdkdLoopbackBridgeEndpoint {
		return ChatCompletionResponse{}, errors.New("sdkd auth key is not configured")
	}
	requestDocument := sdkdChatCompletionRequestDocument{
		Model:             client.ModelName,
		ExecutionMode:     executionMode,
		Context:           requestContextPointer(responseContext),
		Messages:          append([]ChatCompletionMessage{}, request.Messages...),
		Tools:             append([]ChatCompletionTool{}, request.Tools...),
		ToolChoice:        append(json.RawMessage{}, request.ToolChoice...),
		ParallelToolCalls: request.ParallelToolCalls,
		GenerationOptions: generationOptionsPointer(mergeGenerationOptions(client.GenerationOptions, request.GenerationOptions)),
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
	response.Transport = "sdkd"
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
		Transport:       "sdkd",
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
	generationOptions := mergeGenerationOptions(client.GenerationOptions, request.GenerationOptions)
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
				Code                string          `json:"code"`
				AllowLegacyFallback bool            `json:"allowLegacyFallback"`
				Diagnostic          json.RawMessage `json:"diagnostic"`
			} `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &errorDocument)
		return sdkdHTTPError{
			StatusCode:          httpResponse.StatusCode,
			Code:                strings.TrimSpace(errorDocument.Error.Code),
			Message:             strings.TrimSpace(string(responseBody)),
			AllowLegacyFallback: errorDocument.Error.AllowLegacyFallback,
			Diagnostic:          parseStructuredOutputDiagnostic(errorDocument.Error.Diagnostic),
		}
	}
	return json.Unmarshal(responseBody, responseDocument)
}

func parseStructuredOutputDiagnostic(document json.RawMessage) StructuredOutputDiagnostic {
	var diagnosticDocument struct {
		Category         string                            `json:"category"`
		FinishReason     string                            `json:"finishReason"`
		ToolName         string                            `json:"toolName"`
		ValidationIssues []StructuredOutputValidationIssue `json:"validationIssues"`
		RepairStatus     string                            `json:"repairStatus"`
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(&diagnosticDocument); errorValue != nil {
		return StructuredOutputDiagnostic{}
	}
	if errorValue := decoder.Decode(&struct{}{}); errorValue != io.EOF {
		return StructuredOutputDiagnostic{}
	}
	normalizedCategory := StructuredOutputDiagnosticCategory(strings.TrimSpace(diagnosticDocument.Category))
	normalizedFinishReason := StructuredOutputFinishReason(strings.TrimSpace(diagnosticDocument.FinishReason))
	normalizedToolName := strings.TrimSpace(diagnosticDocument.ToolName)
	normalizedRepairStatus := StructuredOutputRepairStatus(strings.TrimSpace(diagnosticDocument.RepairStatus))
	if !isStructuredOutputDiagnosticCategory(normalizedCategory) {
		return StructuredOutputDiagnostic{}
	}
	if normalizedCategory != StructuredOutputDiagnosticFinishReason && normalizedFinishReason != "" {
		return StructuredOutputDiagnostic{}
	}
	if normalizedFinishReason != "" && !isSDKDChatCompletionFinishReason(string(normalizedFinishReason)) {
		return StructuredOutputDiagnostic{}
	}
	if !isStructuredOutputDiagnosticToolName(normalizedToolName) ||
		!areStructuredOutputValidationIssuesValid(normalizedCategory, diagnosticDocument.ValidationIssues) ||
		!isStructuredOutputRepairStatus(normalizedRepairStatus) {
		return StructuredOutputDiagnostic{}
	}
	return StructuredOutputDiagnostic{
		Category:         normalizedCategory,
		FinishReason:     normalizedFinishReason,
		ToolName:         normalizedToolName,
		ValidationIssues: diagnosticDocument.ValidationIssues,
		RepairStatus:     normalizedRepairStatus,
	}
}

var structuredOutputDiagnosticToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var structuredOutputDiagnosticFieldPathPattern = regexp.MustCompile(`^/([A-Za-z0-9_.$~-]+(/[A-Za-z0-9_.$~-]+)*)?$`)

func isStructuredOutputDiagnosticToolName(toolName string) bool {
	return toolName == "" || len(toolName) <= 128 && structuredOutputDiagnosticToolNamePattern.MatchString(toolName)
}

func areStructuredOutputValidationIssuesValid(category StructuredOutputDiagnosticCategory, issues []StructuredOutputValidationIssue) bool {
	if len(issues) > 8 || len(issues) > 0 && category != StructuredOutputDiagnosticSchemaValidation {
		return false
	}
	for _, issue := range issues {
		if len(issue.FieldPath) > 256 || !structuredOutputDiagnosticFieldPathPattern.MatchString(issue.FieldPath) || !isStructuredOutputValidationCode(issue.Code) {
			return false
		}
	}
	return true
}

func isStructuredOutputValidationCode(code StructuredOutputValidationCode) bool {
	switch code {
	case StructuredOutputValidationRequired,
		StructuredOutputValidationAdditionalProperty,
		StructuredOutputValidationType,
		StructuredOutputValidationOther:
		return true
	default:
		return false
	}
}

func isStructuredOutputRepairStatus(status StructuredOutputRepairStatus) bool {
	return status == "" || status == StructuredOutputRepairNotAttempted || status == StructuredOutputRepairFailed
}

func isStructuredOutputDiagnosticCategory(category StructuredOutputDiagnosticCategory) bool {
	switch category {
	case StructuredOutputDiagnosticJSONParse,
		StructuredOutputDiagnosticSchemaValidation,
		StructuredOutputDiagnosticFinishReason,
		StructuredOutputDiagnosticToolCallContract,
		StructuredOutputDiagnosticSerialization:
		return true
	default:
		return false
	}
}

func canUseLegacySDKDFallback(errorValue error) bool {
	if isSDKDTransportError(errorValue) {
		return true
	}
	httpError, isHTTPError := asSDKDHTTPError(errorValue)
	return isHTTPError &&
		httpError.AllowLegacyFallback &&
		httpError.Code == "sdkd_bridge_unavailable" &&
		httpError.StatusCode == http.StatusServiceUnavailable
}

func shouldRetrySDKDRecovery(errorValue error) bool {
	if isSDKDTransportError(errorValue) {
		return true
	}
	httpError, isHTTPError := asSDKDHTTPError(errorValue)
	if !isHTTPError || !httpError.AllowLegacyFallback {
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

func isSDKDTransportError(errorValue error) bool {
	if errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) {
		return false
	}
	var transportError sdkdTransportError
	return errors.As(errorValue, &transportError)
}

func asSDKDHTTPError(errorValue error) (sdkdHTTPError, bool) {
	var httpError sdkdHTTPError
	isHTTPError := errors.As(errorValue, &httpError)
	return httpError, isHTTPError
}

func (client SDKDClient) executionMode() string {
	if client.ExecutionMode == "" {
		return "auto"
	}
	return client.ExecutionMode
}
