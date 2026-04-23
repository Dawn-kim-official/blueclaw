package llm

import (
	"encoding/json"
	"testing"
)

func TestOpenRouterBuildStructuredRequestDocumentUsesJSONSchema(t *testing.T) {
	openRouterClient := OpenRouterClient{
		ModelName:             "openai/gpt-4.1-mini",
		RequireParameters:     true,
		EnableResponseHealing: true,
	}

	requestDocument, errorValue := openRouterClient.BuildStructuredRequestDocument(
		StructuredResponseRequest{
			Messages: []Message{
				{
					Role:    "user",
					Content: "extract payroll policy",
				},
			},
			StructuredOutputSchema: StructuredOutputSchema{
				Name:               "policy_result",
				Document:           `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
				IsStrictlyEnforced: true,
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected request document to build: %v", errorValue)
	}

	var parsedDocument map[string]json.RawMessage
	errorValue = json.Unmarshal(requestDocument, &parsedDocument)
	if errorValue != nil {
		t.Fatalf("expected request document to be valid json: %v", errorValue)
	}

	if string(parsedDocument["response_format"]) == "" {
		t.Fatal("expected response_format to be present")
	}
	if string(parsedDocument["provider"]) == "" {
		t.Fatal("expected provider requirements to be present")
	}
	if string(parsedDocument["plugins"]) == "" {
		t.Fatal("expected response healing plugin to be present")
	}
}
