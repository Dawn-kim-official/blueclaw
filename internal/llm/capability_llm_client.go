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
	Model                  string                           `json:"model"`
	ExecutionMode          string                           `json:"executionMode"`
	Messages               []Message                        `json:"messages"`
	StructuredOutputSchema capabilityStructuredOutputSchema `json:"structuredOutputSchema"`
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
	structuredResponse, errorValue := capabilityLLMClient.GenerateStructuredResponse(
		responseContext,
		StructuredResponseRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: prompt,
				},
			},
			StructuredOutputSchema: StructuredOutputSchema{
				Name:               "plain_text_response",
				Document:           `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		return "", errorValue
	}

	return structuredResponse.Content, nil
}

func (capabilityLLMClient CapabilityLLMClient) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if capabilityLLMClient.CapabilityClient.HTTPClient == nil {
		return StructuredResponse{}, errors.New("capability llm http client is not configured")
	}

	requestDocument, errorValue := capabilityLLMClient.buildStructuredRequestDocument(structuredResponseRequest)
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

func (capabilityLLMClient CapabilityLLMClient) buildStructuredRequestDocument(structuredResponseRequest StructuredResponseRequest) (capabilityStructuredResponseRequestDocument, error) {
	jsonSchemaDocument, errorValue := normalizeStructuredOutputSchema(structuredResponseRequest.StructuredOutputSchema)
	if errorValue != nil {
		return capabilityStructuredResponseRequestDocument{}, errorValue
	}

	return capabilityStructuredResponseRequestDocument{
		Model:         capabilityLLMClient.ModelName,
		ExecutionMode: capabilityLLMClient.executionMode(),
		Messages:      append([]Message{}, structuredResponseRequest.Messages...),
		StructuredOutputSchema: capabilityStructuredOutputSchema{
			Name:               structuredResponseRequest.StructuredOutputSchema.Name,
			Document:           jsonSchemaDocument,
			IsStrictlyEnforced: structuredResponseRequest.StructuredOutputSchema.IsStrictlyEnforced,
		},
	}, nil
}

func (capabilityLLMClient CapabilityLLMClient) executionMode() string {
	executionMode := strings.TrimSpace(capabilityLLMClient.ExecutionMode)
	if executionMode == "" {
		return "auto"
	}
	return executionMode
}
