package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type OpenRouterClient struct {
	BaseURL               string
	ModelName             string
	APIKey                string
	RequireParameters     bool
	EnableResponseHealing bool
	HTTPClient            HTTPClient
}

type openRouterRequestDocument struct {
	Model          string                          `json:"model"`
	Messages       []Message                       `json:"messages"`
	ResponseFormat openRouterResponseFormat        `json:"response_format"`
	Provider       openRouterProviderRequirements  `json:"provider,omitempty"`
	Plugins        []openRouterPluginConfiguration `json:"plugins,omitempty"`
	Stream         bool                            `json:"stream"`
}

type openRouterResponseFormat struct {
	Type       string                    `json:"type"`
	JSONSchema openRouterJSONSchemaValue `json:"json_schema"`
}

type openRouterJSONSchemaValue struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openRouterProviderRequirements struct {
	RequireParameters bool `json:"require_parameters"`
}

type openRouterPluginConfiguration struct {
	ID string `json:"id"`
}

type openRouterResponseDocument struct {
	Choices []openRouterChoice `json:"choices"`
}

type openRouterChoice struct {
	Message openRouterMessage `json:"message"`
}

type openRouterMessage struct {
	Content string `json:"content"`
}

func (openRouterClient OpenRouterClient) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	structuredResponse, errorValue := openRouterClient.GenerateStructuredResponse(
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

func (openRouterClient OpenRouterClient) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if openRouterClient.HTTPClient == nil {
		return StructuredResponse{}, errors.New("openrouter http client is not configured")
	}
	if openRouterClient.APIKey == "" {
		return StructuredResponse{}, errors.New("openrouter api key is not configured")
	}

	requestDocument, errorValue := openRouterClient.BuildStructuredRequestDocument(structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	requestURL := openRouterClient.BaseURL
	if requestURL == "" {
		requestURL = "https://openrouter.ai/api/v1/chat/completions"
	}

	httpRequest, errorValue := http.NewRequestWithContext(
		responseContext,
		http.MethodPost,
		requestURL,
		bytes.NewReader(requestDocument),
	)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	httpRequest.Header.Set("Authorization", "Bearer "+openRouterClient.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, errorValue := openRouterClient.HTTPClient.Do(httpRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	defer httpResponse.Body.Close()

	responseDocument, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	if httpResponse.StatusCode >= http.StatusBadRequest {
		return StructuredResponse{}, errors.New(string(responseDocument))
	}

	var parsedResponse openRouterResponseDocument
	errorValue = json.Unmarshal(responseDocument, &parsedResponse)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}
	if len(parsedResponse.Choices) == 0 {
		return StructuredResponse{}, errors.New("openrouter response did not include choices")
	}

	return StructuredResponse{
		ProviderName: "openrouter",
		ModelName:    openRouterClient.ModelName,
		Content:      parsedResponse.Choices[0].Message.Content,
	}, nil
}

func (openRouterClient OpenRouterClient) BuildStructuredRequestDocument(structuredResponseRequest StructuredResponseRequest) ([]byte, error) {
	jsonSchemaDocument, errorValue := normalizeStructuredOutputSchema(structuredResponseRequest.StructuredOutputSchema)
	if errorValue != nil {
		return nil, errorValue
	}

	requestDocument := openRouterRequestDocument{
		Model:    openRouterClient.ModelName,
		Messages: append([]Message{}, structuredResponseRequest.Messages...),
		ResponseFormat: openRouterResponseFormat{
			Type: "json_schema",
			JSONSchema: openRouterJSONSchemaValue{
				Name:   structuredResponseRequest.StructuredOutputSchema.Name,
				Strict: structuredResponseRequest.StructuredOutputSchema.IsStrictlyEnforced,
				Schema: jsonSchemaDocument,
			},
		},
		Stream: false,
	}

	if openRouterClient.RequireParameters {
		requestDocument.Provider = openRouterProviderRequirements{
			RequireParameters: true,
		}
	}

	if openRouterClient.EnableResponseHealing {
		requestDocument.Plugins = []openRouterPluginConfiguration{
			{ID: "response-healing"},
		}
	}

	return json.Marshal(requestDocument)
}
