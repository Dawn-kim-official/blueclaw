package llm

import (
	"context"
	"strings"

	"blueclaw/internal/capability"
)

type CapabilityClient struct {
	Client                capability.Client
	ModelName             string
	RequireParameters     bool
	EnableResponseHealing bool
}

type capabilityCompletionRequest struct {
	ModelName              string                  `json:"modelName"`
	Messages               []Message               `json:"messages"`
	StructuredOutputSchema capabilitySchemaRequest `json:"structuredOutputSchema"`
	RequireParameters      bool                    `json:"requireParameters"`
	EnableResponseHealing  bool                    `json:"enableResponseHealing"`
}

type capabilitySchemaRequest struct {
	Name               string `json:"name"`
	Document           string `json:"document"`
	IsStrictlyEnforced bool   `json:"isStrictlyEnforced"`
}

type capabilityCompletionResponse struct {
	ProviderName string `json:"providerName"`
	ModelName    string `json:"modelName"`
	Content      string `json:"content"`
}

func (client CapabilityClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	structuredResponse, errorValue := client.GenerateStructuredResponse(
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

func (client CapabilityClient) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	request := capabilityCompletionRequest{
		ModelName: strings.TrimSpace(client.ModelName),
		Messages:  append([]Message{}, structuredResponseRequest.Messages...),
		StructuredOutputSchema: capabilitySchemaRequest{
			Name:               structuredResponseRequest.StructuredOutputSchema.Name,
			Document:           structuredResponseRequest.StructuredOutputSchema.Document,
			IsStrictlyEnforced: structuredResponseRequest.StructuredOutputSchema.IsStrictlyEnforced,
		},
		RequireParameters:     client.RequireParameters,
		EnableResponseHealing: client.EnableResponseHealing,
	}

	var response capabilityCompletionResponse
	errorValue := client.Client.Call(responseContext, "llm.complete", request, &response)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	return StructuredResponse{
		ProviderName: firstCapabilityValue(response.ProviderName, "capability"),
		ModelName:    firstCapabilityValue(response.ModelName, client.ModelName),
		Content:      response.Content,
	}, nil
}

func firstCapabilityValue(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
