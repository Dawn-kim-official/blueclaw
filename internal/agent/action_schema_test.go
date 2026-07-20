package agent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
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

const eightToolActionSchemaByteCeiling = 17500

func TestActionSchemaSharedEnvelopeByteBudget(t *testing.T) {
	toolDefinitions := eightToolCapabilityCatalogFixture(t)

	schemaDocument := buildActionSchemaFromToolDefinitions(toolDefinitions, true, nil, false)

	t.Logf("action schema byte length for an 8-tool catalog: %d", len(schemaDocument))
	if len(schemaDocument) >= eightToolActionSchemaByteCeiling {
		t.Fatalf("expected the deduplicated action schema to stay under %d bytes, got %d", eightToolActionSchemaByteCeiling, len(schemaDocument))
	}
	var compiledSchema jsonschema.Schema
	if errorValue := json.Unmarshal([]byte(schemaDocument), &compiledSchema); errorValue != nil {
		t.Fatalf("expected the action schema to parse with the santhosh jsonschema library, got %v", errorValue)
	}
	if _, errorValue := compiledSchema.Resolve(nil); errorValue != nil {
		t.Fatalf("expected the action schema to resolve with the santhosh jsonschema library, got %v", errorValue)
	}
}

func eightToolCapabilityCatalogFixture(t *testing.T) []ToolDefinition {
	t.Helper()
	document, errorValue := os.ReadFile("../../protocol/generated/capability-tools.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var catalog struct {
		Tools []struct {
			ModelName   string          `json:"modelName"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	selectedToolNames := map[string]bool{
		"task.add": true, "task.update": true, "message.send": true, "message.search": true,
		"document.read": true, "image.read": true, "web.search": true, "site.create": true,
	}
	toolDefinitions := make([]ToolDefinition, 0, len(selectedToolNames))
	for _, tool := range catalog.Tools {
		if !selectedToolNames[tool.ModelName] {
			continue
		}
		toolDefinitions = append(toolDefinitions, ToolDefinition{
			Name:        tool.ModelName,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	if len(toolDefinitions) != len(selectedToolNames) {
		t.Fatalf("expected %d fixture tools, got %d", len(selectedToolNames), len(toolDefinitions))
	}
	return toolDefinitions
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
