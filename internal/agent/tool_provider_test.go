package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type testToolProvider struct {
	providerID string
	tools      []BoundTool
	errorValue error
}

func (provider testToolProvider) ProviderID() string {
	return provider.providerID
}

func (provider testToolProvider) ListTools(context.Context) ([]BoundTool, error) {
	return provider.tools, provider.errorValue
}

func TestRegisterProviderAddsValidatedTools(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{validProviderTool("capabilityd/task/task.add", "task", "task.add")},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	toolDescriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound {
		t.Fatal("expected registered tool")
	}
	if toolDescriptor.ID != "capabilityd/task/task.add" || toolDescriptor.ProviderID != "capabilityd" {
		t.Fatalf("unexpected canonical identity: %+v", toolDescriptor)
	}
}

func TestRegisterProviderRequiresValidInputIntentSchemaForVisibleMutation(t *testing.T) {
	testCases := []struct {
		name              string
		inputIntentSchema json.RawMessage
		expectedError     string
	}{
		{name: "missing", expectedError: "inputIntentSchema is required"},
		{name: "requires value", inputIntentSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`), expectedError: "must accept an empty object"},
		{name: "unknown property", inputIntentSchema: json.RawMessage(`{"type":"object","properties":{"unknown":{"type":"string"}},"additionalProperties":false}`), expectedError: "property is absent from inputSchema: unknown"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			boundTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
			boundTool.Definition.InputIntentSchema = testCase.inputIntentSchema
			provider := &testToolProvider{providerID: "capabilityd", tools: []BoundTool{boundTool}}

			errorValue := NewToolSet(nil).RegisterProvider(context.Background(), provider)

			if errorValue == nil || !strings.Contains(errorValue.Error(), testCase.expectedError) {
				t.Fatalf("expected %s, got %v", testCase.expectedError, errorValue)
			}
		})
	}
}

func TestRegisterProviderPreservesInputIntentSchema(t *testing.T) {
	boundTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	boundTool.Definition.InputSchema = json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)
	boundTool.Definition.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`)
	provider := &testToolProvider{providerID: "capabilityd", tools: []BoundTool{boundTool}}
	toolSet := NewToolSet(nil)

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound || !strings.Contains(string(descriptor.InputIntentSchema), `"title"`) {
		t.Fatalf("expected canonical input intent schema, got %+v", descriptor)
	}
}

func TestRegisterProviderRejectsMissingSchemaAtomically(t *testing.T) {
	validTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	invalidTool := validProviderTool("capabilityd/task/task.list", "task", "task.list")
	invalidTool.Definition.OutputSchema = nil
	toolSet := NewToolSet([]string{"task.add", "task.list"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{validTool, invalidTool},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "outputSchema is required") {
		t.Fatalf("expected missing schema failure, got %v", errorValue)
	}
	if len(toolSet.ListRegisteredToolNames()) != 0 {
		t.Fatalf("expected atomic rejection, got %+v", toolSet.ListRegisteredToolNames())
	}
}

func TestRegisterProviderRejectsNonCanonicalToolName(t *testing.T) {
	testCases := []struct {
		name     string
		toolName string
	}{
		{name: "contains a space", toolName: "task add"},
		{name: "exceeds 128 characters", toolName: "task." + strings.Repeat("a", 128)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			providerTool := validProviderTool("capabilityd/task/"+testCase.toolName, "task", testCase.toolName)
			toolSet := NewToolSet(nil)

			errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
				providerID: "capabilityd",
				tools:      []BoundTool{providerTool},
			})

			if errorValue == nil || !strings.Contains(errorValue.Error(), "name must match") {
				t.Fatalf("expected canonical name rejection, got %v", errorValue)
			}
		})
	}
}

func TestRegisterProviderRejectsModelVisibleToolWithoutResultContract(t *testing.T) {
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.ResultContract = nil
	toolSet := NewToolSet([]string{"task.add"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{providerTool},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "resultContract is required for model-visible tools") {
		t.Fatalf("expected missing result contract rejection, got %v", errorValue)
	}
	if toolSet.IsRegistered("task.add") {
		t.Fatal("expected rejected provider tool to remain unregistered")
	}
}

func TestRegisterProviderAllowsHiddenToolWithoutResultContract(t *testing.T) {
	providerTool := validProviderTool("capabilityd/internal/llm.text", "internal", "llm.text")
	providerTool.Definition.Visibility = ToolVisibilityInternal
	providerTool.Definition.ResultContract = nil
	toolSet := NewToolSet([]string{"llm.text"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{providerTool},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if toolSet.IsAllowed("llm.text") || !toolSet.IsRegistered("llm.text") {
		t.Fatal("expected hidden uncontracted tool to remain internal")
	}
}

func TestRegisterProviderRejectsUnresolvableSchemasAtomically(t *testing.T) {
	testCases := []struct {
		name         string
		mutateSchema func(*ToolDescriptor)
		expected     string
	}{
		{
			name: "input",
			mutateSchema: func(descriptor *ToolDescriptor) {
				descriptor.InputSchema = json.RawMessage(`{"type":"object","$ref":"#/$defs/missing","additionalProperties":false}`)
			},
			expected: "inputSchema cannot be resolved",
		},
		{
			name: "result",
			mutateSchema: func(descriptor *ToolDescriptor) {
				descriptor.ResultContract.Schema = json.RawMessage(`{"type":"object","$ref":"#/$defs/missing","additionalProperties":false}`)
			},
			expected: "resultContract.schema cannot be resolved",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := NewToolSet([]string{"task.add"})
			providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
			providerTool.Definition.ResultContract = &ToolResultContract{
				Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			}
			testCase.mutateSchema(&providerTool.Definition)

			errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
				providerID: "capabilityd",
				tools:      []BoundTool{providerTool},
			})

			if errorValue == nil || !strings.Contains(errorValue.Error(), testCase.expected) {
				t.Fatalf("expected unresolvable %s schema failure, got %v", testCase.name, errorValue)
			}
			if len(toolSet.ListRegisteredToolNames()) != 0 {
				t.Fatalf("expected atomic rejection, got %+v", toolSet.ListRegisteredToolNames())
			}
		})
	}
}

func TestRegisterProviderRequiresExplicitlyClosedObjectSchemas(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.InputSchema = json.RawMessage(`{"type":"object","properties":{"patch":{"type":"object","properties":{}}}}`)

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "object schema must explicitly set additionalProperties to false") {
		t.Fatalf("expected implicitly open schema rejection, got %v", errorValue)
	}
	if toolSet.IsRegistered("task.add") {
		t.Fatal("expected rejected provider tool to remain unregistered")
	}
}

func TestRegisterProviderRejectsOpenObjectSchema(t *testing.T) {
	for _, inputSchema := range []json.RawMessage{
		json.RawMessage(`{"type":"object","additionalProperties":true}`),
		json.RawMessage(`{"type":"object","additionalProperties":{}}`),
	} {
		toolSet := NewToolSet([]string{"task.add"})
		providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
		providerTool.Definition.InputSchema = inputSchema

		errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

		if errorValue == nil || !strings.Contains(errorValue.Error(), "object schema must explicitly set additionalProperties to false") {
			t.Fatalf("expected open input schema to fail closed, got %v", errorValue)
		}
	}
}

func TestRegisterProvidersRequiresExplicitlyClosedExternalObjectSchemas(t *testing.T) {
	testCases := []struct {
		name           string
		schema         json.RawMessage
		shouldRegister bool
	}{
		{name: "missing", schema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{name: "true", schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)},
		{name: "false", schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), shouldRegister: true},
		{name: "nested missing", schema: json.RawMessage(`{"type":"object","properties":{"nested":{"type":"object","properties":{}}},"additionalProperties":false}`)},
		{name: "nested true", schema: json.RawMessage(`{"type":"object","properties":{"nested":{"type":"object","properties":{},"additionalProperties":true}},"additionalProperties":false}`)},
		{name: "nested false", schema: json.RawMessage(`{"type":"object","properties":{"nested":{"type":"object","properties":{},"additionalProperties":false}},"additionalProperties":false}`), shouldRegister: true},
		{name: "nested nullable object missing", schema: json.RawMessage(`{"type":"object","properties":{"nested":{"type":["object","null"],"properties":{}}},"additionalProperties":false}`)},
		{name: "nested nullable object false", schema: json.RawMessage(`{"type":"object","properties":{"nested":{"type":["object","null"],"properties":{},"additionalProperties":false}},"additionalProperties":false}`), shouldRegister: true},
	}

	for _, testCase := range testCases {
		for _, schemaField := range []string{"input", "output"} {
			t.Run(testCase.name+"/"+schemaField, func(t *testing.T) {
				providerTool := validProviderTool("mcp/schema/task.add", "task", "task.add")
				providerTool.Definition.OutputSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
				if schemaField == "input" {
					providerTool.Definition.InputSchema = testCase.schema
				} else {
					providerTool.Definition.OutputSchema = testCase.schema
				}

				toolSet := NewToolSet([]string{"task.add"})
				quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{{
					Provider: testToolProvider{providerID: "mcp:schema", tools: []BoundTool{providerTool}},
					Trust:    ToolProviderExternal,
				}})
				if errorValue != nil {
					t.Fatal(errorValue)
				}

				if testCase.shouldRegister {
					if len(quarantinedProviders) != 0 || !toolSet.IsRegistered("task.add") {
						t.Fatalf("expected external provider to register, quarantined=%+v", quarantinedProviders)
					}
					return
				}
				if len(quarantinedProviders) != 1 || !strings.Contains(quarantinedProviders[0].Reason, "explicitly set additionalProperties to false") {
					t.Fatalf("expected external provider quarantine, got %+v", quarantinedProviders)
				}
				if toolSet.IsRegistered("task.add") {
					t.Fatal("expected rejected external provider to remain unregistered")
				}
			})
		}
	}
}

func TestRegisterProviderRejectsNonObjectOutputSchema(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.OutputSchema = json.RawMessage(`{"type":"string"}`)

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "outputSchema must describe an object") {
		t.Fatalf("expected non-object output schema rejection, got %v", errorValue)
	}
}

func TestRegisterProviderRejectsIncompleteObservationMetadata(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.Completion.Action = ""

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "completion.action and completion.targetKind are required") {
		t.Fatalf("expected incomplete observation metadata rejection, got %v", errorValue)
	}
}

func TestRegisterProviderRequiresIdempotencyScopeWhenSupported(t *testing.T) {
	for _, idempotency := range []string{ToolIdempotencySupported, ToolIdempotencyRequired} {
		toolSet := NewToolSet([]string{"task.add"})
		providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
		providerTool.Definition.Idempotency = idempotency

		errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

		if errorValue == nil || !strings.Contains(errorValue.Error(), "idempotencyScope is required") {
			t.Fatalf("expected %s without scope to fail closed, got %v", idempotency, errorValue)
		}
	}
}

func TestRegisterProviderAllowsMissingIdempotencyScopeWhenUnsupported(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{validProviderTool("capabilityd/task/task.add", "task", "task.add")},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestRegisterProviderRejectsUnboundResultEffectIdentity(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
		Effects: []ResourceEffectContract{{
			ObjectType:     "task",
			Effect:         "created",
			ResultField:    "missingID",
			EffectIdentity: "id",
		}},
	}

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "resultField must name a required string or nonempty unique string array property") {
		t.Fatalf("expected unbound result identity rejection, got %v", errorValue)
	}
}

func TestRegisterProviderAcceptsDistinctIdentitiesForOneEffect(t *testing.T) {
	toolSet := NewToolSet([]string{"site.publish"})
	providerTool := validProviderTool("capabilityd/site/site.publish", "site", "site.publish")
	providerTool.Definition.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"},"publishedURL":{"type":"string"}},"required":["siteID","publishedURL"],"additionalProperties":false}`),
		Effects: []ResourceEffectContract{
			{ObjectType: "website", Effect: "published", ResultField: "siteID", EffectIdentity: "id"},
			{ObjectType: "website", Effect: "published", ResultField: "publishedURL", EffectIdentity: "url"},
		},
	}

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestRegisterProviderValidatesEvidenceConditionAgainstResultSchema(t *testing.T) {
	providerTool := validProviderTool("capabilityd/artifact/artifact.review", "artifact", "artifact.review")
	providerTool.Definition.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"passed":{"type":"boolean"}},"required":["passed"],"additionalProperties":false}`),
		EvidenceCondition: &EvidenceCondition{
			ResultField: "passed",
			Equals:      json.RawMessage(`"true"`),
		},
	}

	errorValue := NewToolSet([]string{"artifact.review"}).RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{providerTool},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "must match a required result property") {
		t.Fatalf("expected evidence condition type mismatch rejection, got %v", errorValue)
	}
}

func TestRegisterProviderAcceptsStringArrayResultEffectIdentity(t *testing.T) {
	toolSet := NewToolSet([]string{"file.edit"})
	providerTool := validProviderTool("kernel/file.edit", "file", "file.edit")
	providerTool.Definition.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{"editedFiles":{"type":"array","items":{"type":"string"},"minItems":1,"uniqueItems":true}},
			"required":["editedFiles"],
			"additionalProperties":false
		}`),
		Effects: []ResourceEffectContract{{
			ObjectType:     "file",
			Effect:         "updated",
			ResultField:    "editedFiles",
			EffectIdentity: "path",
		}},
	}

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{providerTool},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestRegisterProviderRejectsNoncanonicalStringArrayResultEffectIdentity(t *testing.T) {
	for _, schema := range []json.RawMessage{
		json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`),
		json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["paths"],"additionalProperties":false}`),
		json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"number"},"minItems":1,"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`),
	} {
		toolSet := NewToolSet([]string{"file.edit"})
		providerTool := validProviderTool("kernel/file.edit", "file", "file.edit")
		providerTool.Definition.ResultContract = &ToolResultContract{
			Schema: schema,
			Effects: []ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "updated",
				ResultField:    "paths",
				EffectIdentity: "path",
			}},
		}
		errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
			providerID: "kernel",
			tools:      []BoundTool{providerTool},
		})
		if errorValue == nil {
			t.Fatalf("expected noncanonical array identity rejection for %s", schema)
		}
	}
}

func TestToolSetValidatesCanonicalResultContract(t *testing.T) {
	testCases := []struct {
		name      string
		result    ToolResult
		isSuccess bool
	}{
		{
			name: "valid",
			result: ToolResult{
				Output:  ToolOutput{Data: json.RawMessage(`{"taskID":"task-1"}`)},
				Effects: []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}},
			},
			isSuccess: true,
		},
		{
			name:   "invalid schema",
			result: ToolResult{Output: ToolOutput{Data: json.RawMessage(`{"id":"task-1"}`)}, Effects: []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}}},
		},
		{
			name:   "missing effect",
			result: ToolResult{Output: ToolOutput{Data: json.RawMessage(`{"taskID":"task-1"}`)}},
		},
		{
			name:   "mismatched effect identity",
			result: ToolResult{Output: ToolOutput{Data: json.RawMessage(`{"taskID":"task-1"}`)}, Effects: []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-2"}}},
		},
		{
			name:   "undeclared extra effect",
			result: ToolResult{Output: ToolOutput{Data: json.RawMessage(`{"taskID":"task-1"}`)}, Effects: []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}, {ObjectType: "file", Effect: "created", Path: "/workspace/report.md"}}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := NewToolSet([]string{"task.add"})
			boundTool := validProviderTool("capabilityd/task.add", "task", "task.add")
			boundTool.Definition.ResultContract = &ToolResultContract{
				Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
				Effects: []ResourceEffectContract{{
					ObjectType:     "task",
					Effect:         "created",
					ResultField:    "taskID",
					EffectIdentity: "id",
				}},
			}
			boundTool.Handler = func(context.Context, ToolInvocation) (ToolResult, error) {
				return testCase.result, nil
			}
			if errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{boundTool}}); errorValue != nil {
				t.Fatal(errorValue)
			}

			result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "task.add", Input: json.RawMessage(`{}`)})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if testCase.isSuccess && result.Failed() {
				t.Fatalf("expected success, got %+v", result)
			}
			if !testCase.isSuccess && !result.Failed() {
				t.Fatalf("success=%v result=%+v", testCase.isSuccess, result)
			}
			if !testCase.isSuccess && result.FailureStage() != "tool_result_contract" {
				t.Fatalf("expected result contract failure, got %+v", result)
			}
		})
	}
}

func TestToolSetValidatesEveryArrayEffectIdentity(t *testing.T) {
	testCases := []struct {
		name      string
		effects   []ResourceEffect
		isSuccess bool
	}{
		{
			name: "exact paths",
			effects: []ResourceEffect{
				{ObjectType: "file", Effect: "updated", Path: "/workspace/second.md"},
				{ObjectType: "file", Effect: "updated", Path: "/workspace/first.md"},
			},
			isSuccess: true,
		},
		{
			name: "missing path",
			effects: []ResourceEffect{
				{ObjectType: "file", Effect: "updated", Path: "/workspace/first.md"},
			},
		},
		{
			name: "duplicated path",
			effects: []ResourceEffect{
				{ObjectType: "file", Effect: "updated", Path: "/workspace/first.md"},
				{ObjectType: "file", Effect: "updated", Path: "/workspace/first.md"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			toolSet := NewToolSet([]string{"file.edit"})
			boundTool := validProviderTool("kernel/file.edit", "file", "file.edit")
			boundTool.Definition.ResultContract = &ToolResultContract{
				Schema: json.RawMessage(`{
					"type":"object",
					"properties":{"editedFiles":{"type":"array","items":{"type":"string"},"minItems":1,"uniqueItems":true}},
					"required":["editedFiles"],
					"additionalProperties":false
				}`),
				Effects: []ResourceEffectContract{{
					ObjectType:     "file",
					Effect:         "updated",
					ResultField:    "editedFiles",
					EffectIdentity: "path",
				}},
			}
			boundTool.Handler = func(context.Context, ToolInvocation) (ToolResult, error) {
				return ToolResult{
					Output:  ToolOutput{Data: json.RawMessage(`{"editedFiles":["/workspace/first.md","/workspace/second.md"]}`)},
					Effects: testCase.effects,
				}, nil
			}
			if errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{boundTool}}); errorValue != nil {
				t.Fatal(errorValue)
			}

			result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "file.edit", Input: json.RawMessage(`{}`)})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if testCase.isSuccess == result.Failed() {
				t.Fatalf("expected success=%v, got %+v", testCase.isSuccess, result)
			}
		})
	}
}

func TestToolSetRejectsEffectsWithoutResultContract(t *testing.T) {
	toolSet := NewToolSet([]string{"external.tasks.create"})
	boundTool := validProviderTool("external/tasks/create", "tasks", "external.tasks.create")
	boundTool.Definition.ResultContract = nil
	boundTool.Handler = func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{
			Output:  ToolOutput{Data: json.RawMessage(`{"taskID":"task-1"}`)},
			Effects: []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}},
		}, nil
	}
	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "external", tools: []BoundTool{boundTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "resultContract is required for model-visible tools") {
		t.Fatalf("expected uncontracted model tool rejection, got %v", errorValue)
	}
}

func TestRegisterProviderRejectsCanonicalIdentityAndModelNameCollisions(t *testing.T) {
	tests := []struct {
		name  string
		tools []BoundTool
	}{
		{
			name: "identifier",
			tools: []BoundTool{
				validProviderTool("external/tasks/create", "tasks", "external.task.create"),
				validProviderTool("external/tasks/create", "tasks", "external.task.copy"),
			},
		},
		{
			name: "model name",
			tools: []BoundTool{
				validProviderTool("external/tasks/create", "tasks", "external.task.create"),
				validProviderTool("external/tasks/copy", "tasks", "external.task.create"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			toolSet := NewToolSet(nil)
			errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
				providerID: "external",
				tools:      test.tools,
			})
			if errorValue == nil {
				t.Fatal("expected collision failure")
			}
		})
	}
}

func TestRegisterProvidersQuarantinesOnlyExternalFailure(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{
		{
			Provider: testToolProvider{providerID: "broken-mcp", errorValue: errors.New("offline")},
			Trust:    ToolProviderExternal,
		},
		{
			Provider: testToolProvider{
				providerID: "capabilityd",
				tools:      []BoundTool{validProviderTool("capabilityd/task/task.add", "task", "task.add")},
			},
			Trust: ToolProviderTrusted,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 1 || quarantinedProviders[0].ProviderID != "broken-mcp" {
		t.Fatalf("unexpected quarantine result: %+v", quarantinedProviders)
	}
	if !toolSet.IsRegistered("task.add") {
		t.Fatal("expected trusted provider to remain available")
	}
}

func TestRegisterProvidersFailsOnTrustedProviderError(t *testing.T) {
	toolSet := NewToolSet(nil)

	_, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{{
		Provider: testToolProvider{providerID: "kernel", errorValue: errors.New("invalid descriptor")},
		Trust:    ToolProviderTrusted,
	}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "invalid descriptor") {
		t.Fatalf("expected trusted provider failure, got %v", errorValue)
	}
}

func TestRegisterProvidersRejectsUnknownTrust(t *testing.T) {
	toolSet := NewToolSet(nil)

	_, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{{
		Provider: testToolProvider{providerID: "unknown"},
		Trust:    "unknown",
	}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "trust is invalid") {
		t.Fatalf("expected unknown trust rejection, got %v", errorValue)
	}
}

func TestRegisterProvidersQuarantinesEveryExternalProviderInAModelNameCollision(t *testing.T) {
	toolSet := NewToolSet([]string{"workspace.echo"})
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{
		{
			Provider: testToolProvider{
				providerID: "mcp:first",
				tools:      []BoundTool{validProviderTool("mcp/first/echo", "workspace", "workspace.echo")},
			},
			Trust: ToolProviderExternal,
		},
		{
			Provider: testToolProvider{
				providerID: "mcp:second",
				tools:      []BoundTool{validProviderTool("mcp/second/echo", "workspace", "workspace.echo")},
			},
			Trust: ToolProviderExternal,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 2 {
		t.Fatalf("expected both conflicting providers to be quarantined, got %+v", quarantinedProviders)
	}
	if toolSet.IsRegistered("workspace.echo") {
		t.Fatal("expected a colliding external tool name to remain unregistered")
	}
}

func TestRegisterProvidersQuarantinesExternalCollisionWithTrustedTool(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{
		{
			Provider: testToolProvider{
				providerID: "mcp:tasks",
				tools:      []BoundTool{validProviderTool("mcp/tasks/add", "task", "task.add")},
			},
			Trust: ToolProviderExternal,
		},
		{
			Provider: testToolProvider{
				providerID: "capabilityd",
				tools:      []BoundTool{validProviderTool("capabilityd/task/add", "task", "task.add")},
			},
			Trust: ToolProviderTrusted,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 1 || quarantinedProviders[0].ProviderID != "mcp:tasks" {
		t.Fatalf("expected only the external provider to be quarantined, got %+v", quarantinedProviders)
	}
	descriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound || descriptor.ProviderID != "capabilityd" {
		t.Fatalf("expected the trusted tool to remain registered, got %+v", descriptor)
	}
}

func TestRegisterBoundToolRejectsOverwrite(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	firstTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	secondTool := firstTool

	if errorValue := toolSet.RegisterBoundTool(firstTool); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := toolSet.RegisterBoundTool(secondTool); errorValue == nil {
		t.Fatal("expected duplicate registration failure")
	}
}

func TestProviderVisibilityControlsModelExposure(t *testing.T) {
	hiddenTool := validProviderTool("capabilityd/internal/llm.text", "internal", "llm.text")
	hiddenTool.Definition.Visibility = ToolVisibilityInternal
	toolSet := NewToolSet([]string{"llm.text"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{hiddenTool},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if toolSet.IsAllowed("llm.text") {
		t.Fatal("expected hidden descriptor to stay out of model exposure")
	}
	if !toolSet.IsRegistered("llm.text") {
		t.Fatal("expected hidden descriptor to remain internally registered")
	}
}

func validProviderTool(toolID string, namespace string, name string) BoundTool {
	return BoundTool{
		Definition: ToolDescriptor{
			ID:                toolID,
			Namespace:         namespace,
			Name:              name,
			Description:       "Execute " + name,
			PrivacyClass:      "workspace",
			InputSchema:       json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			ResultContract:    &ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
			Visibility:        ToolVisibilityModel,
			PolicyResource:    "tool:" + name,
			SideEffectClass:   ToolSideEffectStateChange,
			Completion:        ToolCompletion{Mode: ToolCompletionObservation, Action: "write_task", TargetKind: "task"},
			Idempotency:       ToolIdempotencyNone,
		},
		Availability: ToolAvailability{Status: ToolAvailabilityAvailable},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		},
	}
}
