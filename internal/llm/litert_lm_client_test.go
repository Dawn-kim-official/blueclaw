package llm

import (
	"encoding/json"
	"testing"
)

func TestLiteRTLMBuildStructuredRequestDocumentUsesConstrainedDecoding(t *testing.T) {
	liteRTLMClient := LiteRTLMClient{
		WrapperPath: "/usr/local/bin/blueclaw-litert-wrapper",
		ModelPath:   "/models/gemma-3n.litertlm",
		Backend:     "cpu",
	}

	requestDocument, errorValue := liteRTLMClient.BuildStructuredRequestDocument(
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
		t.Fatalf("expected wrapper request document to build: %v", errorValue)
	}

	var parsedDocument map[string]json.RawMessage
	errorValue = json.Unmarshal(requestDocument, &parsedDocument)
	if errorValue != nil {
		t.Fatalf("expected wrapper request document to be valid json: %v", errorValue)
	}

	if string(parsedDocument["enableConstrainedDecoding"]) != "true" {
		t.Fatalf("expected constrained decoding to be enabled, got %s", string(parsedDocument["enableConstrainedDecoding"]))
	}
	if string(parsedDocument["constraintProvider"]) != `"llguidance"` {
		t.Fatalf("expected llguidance constraint provider, got %s", string(parsedDocument["constraintProvider"]))
	}
}
