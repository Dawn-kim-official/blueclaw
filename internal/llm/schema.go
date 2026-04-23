package llm

import (
	"encoding/json"
	"errors"
	"strings"
)

func normalizeStructuredOutputSchema(structuredOutputSchema StructuredOutputSchema) (json.RawMessage, error) {
	if strings.TrimSpace(structuredOutputSchema.Name) == "" {
		return nil, errors.New("structured output schema name is required")
	}
	if strings.TrimSpace(structuredOutputSchema.Document) == "" {
		return nil, errors.New("structured output schema document is required")
	}

	var parsedDocument json.RawMessage
	errorValue := json.Unmarshal([]byte(structuredOutputSchema.Document), &parsedDocument)
	if errorValue != nil {
		return nil, errorValue
	}

	return parsedDocument, nil
}
