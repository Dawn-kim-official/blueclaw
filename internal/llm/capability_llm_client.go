package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/capability"
)

type CapabilityLLMClient struct {
	CapabilityClient capability.Client
	ModelName        string
	ExecutionMode    string
}

type capabilityStructuredResponseRequestDocument struct {
	Model                  string                           `json:"model,omitempty"`
	ExecutionMode          string                           `json:"executionMode"`
	Context                *RequestContext                  `json:"context,omitempty"`
	Messages               []Message                        `json:"messages"`
	StructuredOutputSchema capabilityStructuredOutputSchema `json:"structuredOutputSchema"`
}

type capabilityTextResponseRequestDocument struct {
	Model                 string          `json:"model,omitempty"`
	ExecutionMode         string          `json:"executionMode"`
	Context               *RequestContext `json:"context,omitempty"`
	Messages              []Message       `json:"messages"`
	RequireParameters     bool            `json:"requireParameters"`
	EnableResponseHealing bool            `json:"enableResponseHealing"`
}

type capabilityStructuredOutputSchema struct {
	Name               string          `json:"name"`
	Document           json.RawMessage `json:"document"`
	IsStrictlyEnforced bool            `json:"isStrictlyEnforced"`
}

type capabilityStructuredResponseDocument struct {
	ProviderName    string `json:"provider"`
	ModelName       string `json:"model"`
	Content         string `json:"content"`
	SelectedBackend string `json:"selectedBackend"`
}

func (capabilityLLMClient CapabilityLLMClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	return capabilityLLMClient.generateResponse(responseContext, prompt, capabilityLLMClient.executionMode())
}

func (capabilityLLMClient CapabilityLLMClient) GenerateRecoveryResponse(responseContext context.Context, prompt string) (string, error) {
	response, errorValue := capabilityLLMClient.generateResponse(responseContext, prompt, "auto")
	if errorValue == nil && strings.TrimSpace(response) != "" {
		return response, nil
	}
	if capabilityLLMClient.executionMode() == "device" {
		return response, errorValue
	}
	return capabilityLLMClient.generateResponse(responseContext, prompt, "device")
}

func (capabilityLLMClient CapabilityLLMClient) generateResponse(responseContext context.Context, prompt string, executionMode string) (string, error) {
	if capabilityLLMClient.CapabilityClient.HTTPClient == nil {
		return "", errors.New("capability llm http client is not configured")
	}

	var responseDocument capabilityStructuredResponseDocument
	errorValue := capabilityLLMClient.CapabilityClient.PostJSON(
		responseContext,
		"/v1/llm/text",
		capabilityTextResponseRequestDocument{
			Model:         capabilityLLMClient.ModelName,
			ExecutionMode: executionMode,
			Context:       requestContextPointer(responseContext),
			Messages: []Message{{
				Role:    "user",
				Content: prompt,
			}},
		},
		&responseDocument,
	)
	if errorValue != nil {
		return "", errorValue
	}

	return responseDocument.Content, nil
}

func (capabilityLLMClient CapabilityLLMClient) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if capabilityLLMClient.CapabilityClient.HTTPClient == nil {
		return StructuredResponse{}, errors.New("capability llm http client is not configured")
	}

	requestDocument, errorValue := capabilityLLMClient.buildStructuredRequestDocument(responseContext, structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	var responseDocument capabilityStructuredResponseDocument
	errorValue = capabilityLLMClient.CapabilityClient.PostJSON(responseContext, "/v1/llm/structured", requestDocument, &responseDocument)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	providerName := strings.TrimSpace(responseDocument.ProviderName)
	if providerName == "" {
		providerName = "capabilityLLM"
	}
	modelName := strings.TrimSpace(responseDocument.ModelName)
	if modelName == "" {
		modelName = capabilityLLMClient.ModelName
	}

	return StructuredResponse{
		ProviderName: providerName,
		ModelName:    modelName,
		Content:      responseDocument.Content,
	}, nil
}

func (capabilityLLMClient CapabilityLLMClient) buildStructuredRequestDocument(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (capabilityStructuredResponseRequestDocument, error) {
	jsonSchemaDocument, errorValue := normalizeStructuredOutputSchema(structuredResponseRequest.StructuredOutputSchema)
	if errorValue != nil {
		return capabilityStructuredResponseRequestDocument{}, errorValue
	}

	return capabilityStructuredResponseRequestDocument{
		Model:         capabilityLLMClient.ModelName,
		ExecutionMode: capabilityLLMClient.executionMode(),
		Context:       requestContextPointer(responseContext),
		Messages:      append([]Message{}, structuredResponseRequest.Messages...),
		StructuredOutputSchema: capabilityStructuredOutputSchema{
			Name:               structuredResponseRequest.StructuredOutputSchema.Name,
			Document:           jsonSchemaDocument,
			IsStrictlyEnforced: structuredResponseRequest.StructuredOutputSchema.IsStrictlyEnforced,
		},
	}, nil
}

func requestContextPointer(ctx context.Context) *RequestContext {
	requestContext := RequestContextFromContext(ctx)
	if requestContext.RequesterPersonID == "" &&
		requestContext.RequesterEmail == "" &&
		requestContext.RequesterName == "" &&
		requestContext.RequesterPlatformUserID == "" &&
		requestContext.ConversationID == "" &&
		requestContext.Platform == "" {
		return nil
	}
	return &requestContext
}

func (capabilityLLMClient CapabilityLLMClient) executionMode() string {
	executionMode := strings.TrimSpace(capabilityLLMClient.ExecutionMode)
	if executionMode == "" {
		return "auto"
	}
	return executionMode
}
