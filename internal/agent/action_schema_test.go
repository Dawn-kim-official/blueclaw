package agent

import (
	"encoding/json"
	"testing"
)

func TestActionSchemasRecursivelyCloseEveryObject(t *testing.T) {
	toolDefinitions := []ToolDefinition{{
		Name: "test.create",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{
					"type":"object",
					"properties":{"title":{"type":"string"}},
					"additionalProperties":false
				}
			},
			"additionalProperties":false
		}`),
	}}
	schemaDocuments := map[string]string{
		"agent action":      buildActionSchemaFromToolDefinitions(toolDefinitions, true, nil, true),
		"finalizer":         finalizerActionSchema(),
		"terminal no tools": terminalNoToolsActionSchema(),
		"recovery decision": recoveryDecisionSchema(),
	}

	for schemaName, schemaDocument := range schemaDocuments {
		t.Run(schemaName, func(t *testing.T) {
			var schemaValue any
			if errorValue := json.Unmarshal([]byte(schemaDocument), &schemaValue); errorValue != nil {
				t.Fatal(errorValue)
			}
			assertEveryObjectSchemaIsClosed(t, schemaValue)
		})
	}
}

func assertEveryObjectSchemaIsClosed(t *testing.T, schemaValue any) {
	t.Helper()
	switch typedValue := schemaValue.(type) {
	case []any:
		for _, item := range typedValue {
			assertEveryObjectSchemaIsClosed(t, item)
		}
	case map[string]any:
		if schemaTypeIncludesObject(typedValue["type"]) && typedValue["additionalProperties"] != false {
			t.Fatalf("expected object schema to be explicitly closed: %+v", typedValue)
		}
		for _, child := range typedValue {
			assertEveryObjectSchemaIsClosed(t, child)
		}
	}
}
