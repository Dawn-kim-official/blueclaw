package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestActionSchemasRecursivelyCloseEveryObject(t *testing.T) {
	toolDefinitions := []toolcontract.ToolDefinition{{
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

const eightToolActionSchemaByteCeiling = 19500

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

func legacyRootOneOfFinalizerSchema(hasFailureDebt bool) string {
	return mustMarshalStructuredSchema(map[string]any{"oneOf": []any{finishActionSchema(hasFailureDebt), failActionSchema(hasFailureDebt)}})
}

func TestTerminalActionSchemasAreFlatAndSmallerThanTheLegacyRootOneOf(t *testing.T) {
	cases := []struct {
		name           string
		hasFailureDebt bool
		flatSchema     string
		legacySchema   string
	}{
		{name: "finalizer", hasFailureDebt: false, flatSchema: finalizerActionSchema(), legacySchema: legacyRootOneOfFinalizerSchema(false)},
		{name: "terminal no tools", hasFailureDebt: true, flatSchema: terminalNoToolsActionSchema(), legacySchema: legacyRootOneOfFinalizerSchema(true)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var flatDocument map[string]any
			if errorValue := json.Unmarshal([]byte(testCase.flatSchema), &flatDocument); errorValue != nil {
				t.Fatalf("expected flat schema json: %v", errorValue)
			}
			if _, hasOneOf := flatDocument["oneOf"]; hasOneOf {
				t.Fatalf("expected a flat schema without a root oneOf, got %s", testCase.flatSchema)
			}
			if flatDocument["type"] != "object" {
				t.Fatalf("expected a flat closed object schema, got %s", testCase.flatSchema)
			}
			if flatDocument["additionalProperties"] != false {
				t.Fatalf("expected the flat schema to stay closed, got %s", testCase.flatSchema)
			}
			actionProperty := mapFromAny(mapFromAny(flatDocument["properties"])["action"])
			for _, actionName := range []string{"finish", "fail"} {
				if !containsString(stringSliceFromAny(actionProperty["enum"]), actionName) {
					t.Fatalf("expected flat schema action enum to allow %q, got %+v", actionName, actionProperty)
				}
			}
			t.Logf("%s schema bytes: legacy root oneOf=%d, flat=%d", testCase.name, len(testCase.legacySchema), len(testCase.flatSchema))
			if len(testCase.flatSchema) >= len(testCase.legacySchema) {
				t.Fatalf("expected the flat %s schema (%d bytes) to be smaller than the legacy root oneOf schema (%d bytes)", testCase.name, len(testCase.flatSchema), len(testCase.legacySchema))
			}
		})
	}
}

func TestTerminalActionSchemasAcceptFinishAndFailDocuments(t *testing.T) {
	finishDocument := `{"action":"finish","message":"done","goalStatus":"satisfied","goalSatisfied":true,"hasRemainingWork":false,"completionEvidenceIDs":[],"qualityReview":[]}`
	failDocument := `{"action":"fail","message":"","reason":"blocked by captcha","goalStatus":"blocked","goalSatisfied":false}`
	failWithDebtDocument := `{"action":"fail","message":"","reason":"blocked by captcha","goalStatus":"blocked","goalSatisfied":false,"failureResolution":"failure_report","usedFailureFacts":{"attempts":[{"toolName":"terminal.run","errorCode":"operation_failed","failureStage":"terminal_run","message":"blocked"}],"budgetState":"failure_report_required"}}`
	finishWithDebtDocument := `{"action":"finish","message":"done from context","goalStatus":"satisfied","goalSatisfied":true,"hasRemainingWork":false,"completionEvidenceIDs":[],"qualityReview":[],"failureResolution":"no_tool_fallback"}`

	assertDocumentValidatesAgainstSchema(t, finalizerActionSchema(), finishDocument)
	assertDocumentValidatesAgainstSchema(t, finalizerActionSchema(), failDocument)
	assertDocumentValidatesAgainstSchema(t, terminalNoToolsActionSchema(), finishWithDebtDocument)
	assertDocumentValidatesAgainstSchema(t, terminalNoToolsActionSchema(), failWithDebtDocument)
}

func assertDocumentValidatesAgainstSchema(t *testing.T, schemaDocument string, instanceDocument string) {
	t.Helper()
	var compiledSchema jsonschema.Schema
	if errorValue := json.Unmarshal([]byte(schemaDocument), &compiledSchema); errorValue != nil {
		t.Fatalf("expected schema json: %v", errorValue)
	}
	resolvedSchema, errorValue := compiledSchema.Resolve(nil)
	if errorValue != nil {
		t.Fatalf("expected schema to resolve: %v", errorValue)
	}
	var instanceValue any
	if errorValue := json.Unmarshal([]byte(instanceDocument), &instanceValue); errorValue != nil {
		t.Fatalf("expected instance json: %v", errorValue)
	}
	if errorValue := resolvedSchema.Validate(instanceValue); errorValue != nil {
		t.Fatalf("expected instance to validate against schema: %v\ninstance: %s\nschema: %s", errorValue, instanceDocument, schemaDocument)
	}
}

func eightToolCapabilityCatalogFixture(t *testing.T) []toolcontract.ToolDefinition {
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
		"document.read": true, "image.read": true, "web.search": true, "site.serve": true,
	}
	toolDefinitions := make([]toolcontract.ToolDefinition, 0, len(selectedToolNames))
	for _, tool := range catalog.Tools {
		if !selectedToolNames[tool.ModelName] {
			continue
		}
		toolDefinitions = append(toolDefinitions, toolcontract.ToolDefinition{
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
		if toolcontract.SchemaTypeIncludesObject(typedValue["type"]) && typedValue["additionalProperties"] != false {
			t.Fatalf("expected object schema to be explicitly closed: %+v", typedValue)
		}
		for _, child := range typedValue {
			assertEveryObjectSchemaIsClosed(t, child)
		}
	}
}
