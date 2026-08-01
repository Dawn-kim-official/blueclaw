package bluecollar

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type echoToolInput struct {
	Message string `json:"message"`
}

type echoToolOutput struct {
	Message string `json:"message"`
}

func TestToolSetExcludesUnregisteredAllowedToolNames(t *testing.T) {
	toolSet := NewToolSet([]string{"registered.tool", "missing.tool"})
	registerTestTool(toolSet, ToolDefinition{Name: "registered.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})

	toolNames := toolSet.ListToolNames()
	if len(toolNames) != 1 || toolNames[0] != "registered.tool" {
		t.Fatalf("expected only registered exposed tool, got %+v", toolNames)
	}
	if toolSet.IsAllowed("missing.tool") {
		t.Fatal("expected unregistered allowed tool name to stay hidden")
	}
}

func TestDirectToolRegistrationIsNotModelCallableWithoutResultContract(t *testing.T) {
	toolSet := NewToolSet([]string{"internal.tool"})
	if errorValue := toolSet.RegisterTool(ToolDefinition{Name: "internal.tool", Visibility: ToolVisibilityModel}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	if !toolSet.IsRegistered("internal.tool") {
		t.Fatal("expected direct tool registration to remain available internally")
	}
	if toolSet.IsAllowed("internal.tool") || toolSet.CanExpose("internal.tool") {
		t.Fatal("expected direct tool without a result contract to stay off model surfaces")
	}
}

func TestToolSetRejectsDuplicateToolNamesWithoutReplacingTheFirstHandler(t *testing.T) {
	toolSet := NewToolSet([]string{"registered.tool"})
	if errorValue := registerTestTool(toolSet, ToolDefinition{Name: "registered.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("first"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	errorValue := registerTestTool(toolSet, ToolDefinition{Name: "registered.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("second"), nil
	})
	if errorValue == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	result, invocationError := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "registered.tool"})
	if invocationError != nil {
		t.Fatal(invocationError)
	}
	if result.ContentText() != "first" {
		t.Fatalf("expected the original handler to remain registered, got %+v", result)
	}
}

func TestFailureCodeIsGenericOpaqueCode(t *testing.T) {
	failureCode := FailureCodes.Unavailable

	if failureCode.String() != "unavailable" {
		t.Fatalf("expected generic failure code, got %q", failureCode.String())
	}
}

func TestFailureCodeNormalizesLegacyMemorySearchCode(t *testing.T) {
	result := ToolFailureResult(FailureDependencyUnavailable, FailureCode("memory_search_unavailable"), "graphiti_search", "memory failed")

	if result.FailureCode() != FailureCodes.Unavailable.String() {
		t.Fatalf("expected canonical memory search code, got %+v", result)
	}
}

func TestFailureCodeCollapsesUnknownCodesToOperationFailed(t *testing.T) {
	result := ToolFailureResult(FailureExternalService, FailureCode("provider.special.case"), "provider", "provider failed")

	if result.FailureCode() != FailureCodes.OperationFailed.String() {
		t.Fatalf("expected unknown failure code to collapse, got %+v", result)
	}
}

func TestToolSetDescriptionsAndActionSchemaOnlyShowExposedKernelTools(t *testing.T) {
	toolSet := NewToolSet([]string{"visible.tool", "denied.tool"})
	registerTestTool(toolSet, ToolDefinition{Name: "visible.tool", Description: "Visible", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})
	registerTestTool(toolSet, ToolDefinition{Name: "hidden.tool", Description: "Hidden"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})
	toolSet.RegisterBoundTool(BoundTool{
		Definition:   ToolDefinition{Name: "denied.tool", Description: "Denied"},
		Availability: ToolAvailability{Status: ToolAvailabilityDenied, Reason: "policy"},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolFailureResult(FailurePolicyBlocked, FailureCodes.PolicyBlocked, "policy", "denied"), nil
		},
	})

	descriptions := toolSet.Descriptions()
	actionSchema := toolSet.ActionSchema(false, nil, false)
	if !strings.Contains(descriptions, "Available tool catalog") {
		t.Fatalf("expected tool prompt to frame tools as optional, got prompt=%s", descriptions)
	}
	if !strings.Contains(descriptions, "visible.tool") || !strings.Contains(actionSchema, "visible.tool") {
		t.Fatalf("expected visible tool in prompt and schema, got prompt=%s schema=%s", descriptions, actionSchema)
	}
	// hidden.tool is registered but not allowed, so the catalog no longer lists it.
	if strings.Contains(descriptions, "hidden.tool") || strings.Contains(actionSchema, "hidden.tool") {
		t.Fatalf("expected registered-but-not-allowed tool to stay out of both surfaces, got prompt=%s schema=%s", descriptions, actionSchema)
	}
	if strings.Contains(descriptions, "denied.tool") || strings.Contains(actionSchema, "denied.tool") {
		t.Fatalf("expected denied tool to stay hidden, got prompt=%s schema=%s", descriptions, actionSchema)
	}
	if hiddenToolNames := toolSet.ListHiddenDescribedToolNames(); len(hiddenToolNames) != 0 {
		t.Fatalf("expected no hidden described tools now that the catalog only shows exposed kernel tools, got %+v", hiddenToolNames)
	}
}

func TestToolSetDescriptionsUseDescriptorDescription(t *testing.T) {
	toolSet := NewToolSet([]string{"task.update"})
	registerTestTool(toolSet, ToolDefinition{
		Name:        "task.update",
		Description: "Update the task identified by exact taskID.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}}}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})

	descriptions := toolSet.Descriptions()
	if !strings.Contains(descriptions, "exact taskID") || strings.Contains(descriptions, `"query"`) {
		t.Fatalf("expected the descriptor to be the only description source, got %s", descriptions)
	}
}

func TestToolSetDoesNotExposeControlToolsToTheModel(t *testing.T) {
	toolSet := NewToolSet([]string{AskConfirmToolName})
	registerTestTool(toolSet, ToolDefinition{
		Name:         AskConfirmToolName,
		Description:  "Confirm",
		Visibility:   ToolVisibilityControl,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})

	descriptions := toolSet.Descriptions()
	actionSchema := toolSet.ActionSchema(false, nil, false)

	if !toolSet.IsRegistered(AskConfirmToolName) {
		t.Fatal("expected runtime registration to remain observable")
	}
	if toolSet.IsAllowed(AskConfirmToolName) || toolSet.CanExpose(AskConfirmToolName) {
		t.Fatalf("expected ask.confirm to be unavailable to the model, names=%+v", toolSet.ListToolNames())
	}
	if strings.Contains(descriptions, AskConfirmToolName) || strings.Contains(actionSchema, AskConfirmToolName) {
		t.Fatalf("expected ask.confirm to be absent from model surfaces, prompt=%s schema=%s", descriptions, actionSchema)
	}
}

func TestFallbackActionSchemaDoesNotAllowToolCalls(t *testing.T) {
	actionSchema := buildActionSchemaFromToolDefinitions(nil, false, nil, false)
	if strings.Contains(actionSchema, "continue") {
		t.Fatalf("expected fallback schema to omit continue, got %s", actionSchema)
	}
}

func TestActionSchemaUsesRegisteredTaskUpdateSchema(t *testing.T) {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"},"query":{"type":"string"},"title":{"type":"string"},"status":{"type":"string"},"endDate":{"type":"string"}}}`)
	actionSchema := buildActionSchemaFromToolDefinitions([]ToolDefinition{{Name: "task.update", InputSchema: inputSchema}}, false, nil, false)
	for _, fragment := range []string{"task.update", "taskID", "query", "title", "status", "endDate"} {
		if !strings.Contains(actionSchema, fragment) {
			t.Fatalf("expected action schema to include %q, got %s", fragment, actionSchema)
		}
	}
	if strings.Contains(actionSchema, `"content"`) {
		t.Fatalf("expected action schema to omit removed content field, got %s", actionSchema)
	}
	toolSet := NewToolSet([]string{"task.update"})
	registerTestTool(toolSet, ToolDefinition{
		Name:            "task.update",
		InputSchema:     inputSchema,
		SideEffectClass: ToolSideEffectExternalWrite,
		Completion:      ToolCompletion{Mode: ToolCompletionObservation},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, nil
	})
	if !isOneShotCompletionEvidenceTool(toolSet, "task.update") {
		t.Fatal("expected task.update to count as one-shot completion evidence")
	}
}

func TestActionSchemaUsesRegisteredTaskListSchema(t *testing.T) {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"scope":{"type":"string","enum":["self","all"]},"targetPersonHint":{"type":"string"}}}`)
	actionSchema := buildActionSchemaFromToolDefinitions([]ToolDefinition{{Name: "task.list", InputSchema: inputSchema}}, false, nil, false)
	for _, fragment := range []string{`"scope"`, `"self"`, `"all"`, `"targetPersonHint"`} {
		if !strings.Contains(actionSchema, fragment) {
			t.Fatalf("expected task.list schema to include %s, got %s", fragment, actionSchema)
		}
	}
	if strings.Contains(actionSchema, "everyone's tasks") {
		t.Fatalf("expected task.list schema not to default to everyone, got %s", actionSchema)
	}
}

func TestActionSchemaDoesNotInferInputSchemaFromToolName(t *testing.T) {
	actionSchema := buildActionSchemaFromToolDefinitions([]ToolDefinition{{Name: "task.add"}}, false, nil, false)

	if strings.Contains(actionSchema, `"task.add"`) || strings.Contains(actionSchema, `"prompt"`) {
		t.Fatalf("expected the schema-less tool to stay out of the action schema, got %s", actionSchema)
	}
}

func TestToolSideEffectClassUsesOnlyDescriptorMetadata(t *testing.T) {
	tests := []struct {
		toolName           string
		sideEffectClass    string
		expectedSideEffect string
		requiresCompletion bool
	}{
		{toolName: "task.add", sideEffectClass: ToolSideEffectStateChange, expectedSideEffect: ToolSideEffectStateChange, requiresCompletion: true},
		{toolName: "task.list", sideEffectClass: ToolSideEffectRead, expectedSideEffect: ToolSideEffectRead, requiresCompletion: false},
		{toolName: "message.send", sideEffectClass: ToolSideEffectExternalWrite, expectedSideEffect: ToolSideEffectExternalWrite, requiresCompletion: true},
		{toolName: "llm.structured", sideEffectClass: ToolSideEffectComputation, expectedSideEffect: ToolSideEffectComputation, requiresCompletion: false},
		{toolName: "looks.like.write", expectedSideEffect: "", requiresCompletion: false},
	}

	for _, test := range tests {
		toolDefinition := ToolDefinition{Name: test.toolName, SideEffectClass: test.sideEffectClass}
		if actualSideEffect := ToolDefinitionSideEffectClass(toolDefinition); actualSideEffect != test.expectedSideEffect {
			t.Fatalf("expected %s side effect for %s, got %s", test.expectedSideEffect, test.toolName, actualSideEffect)
		}
		if actualRequirement := ToolDefinitionRequiresSideEffectEvidence(toolDefinition); actualRequirement != test.requiresCompletion {
			t.Fatalf("expected requiresCompletion=%v for %s, got %v", test.requiresCompletion, test.toolName, actualRequirement)
		}
	}
}

func TestToolSetKeepsDeclaredRecoverySideEffectBeforeDefault(t *testing.T) {
	toolSet := NewToolSet([]string{"data.write"})
	registerTestTool(toolSet, ToolDefinition{
		Name:         "data.write",
		RecoveryCard: ToolRecoveryCard{SideEffect: ToolSideEffectRead},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})

	toolDefinition, isFound := toolSet.ToolDefinition("data.write")
	if !isFound {
		t.Fatal("expected tool definition")
	}
	if actualSideEffect := ToolDefinitionSideEffectClass(toolDefinition); actualSideEffect != ToolSideEffectRead {
		t.Fatalf("expected declared side effect %s, got %s", ToolSideEffectRead, actualSideEffect)
	}
}

func TestToolSetInvokeRejectsHiddenTool(t *testing.T) {
	toolSet := NewToolSet([]string{"visible.tool"})
	registerTestTool(toolSet, ToolDefinition{Name: "hidden.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("hidden"), nil
	})

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "hidden.tool"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected hidden tool invocation to fail, got %+v", result)
	}
}

func TestToolSetValidatesDescriptorInputSchemaBeforeHandler(t *testing.T) {
	toolSet := NewToolSet([]string{"site.serve"})
	handlerCallCount := 0
	registerTestTool(toolSet, ToolDefinition{
		Name: "site.serve",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"siteID":{"type":"string","pattern":"^[a-z0-9-]+$"},
				"revision":{"type":"integer","minimum":1}
			},
			"required":["siteID","revision"],
			"additionalProperties":false
		}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		handlerCallCount++
		return testToolSuccess("published"), nil
	})

	invalidInputs := []json.RawMessage{
		nil,
		json.RawMessage(`{"siteID":"site-1"}`),
		json.RawMessage(`{"siteID":"SITE 1","revision":1}`),
		json.RawMessage(`{"siteID":"site-1","revision":0}`),
		json.RawMessage(`{"siteID":"site-1","revision":"1"}`),
		json.RawMessage(`{"siteID":"site-1","revision":1,"confirm":true}`),
	}
	for _, input := range invalidInputs {
		result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "site.serve", Input: input})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() || result.Failure.Stage != "tool_input_schema" {
			t.Fatalf("expected descriptor input rejection for %s, got %+v", string(input), result)
		}
	}
	if handlerCallCount != 0 {
		t.Fatalf("expected invalid inputs to stay outside the handler, got %d calls", handlerCallCount)
	}

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{
		ToolName: "site.serve",
		Input:    json.RawMessage(`{"siteID":"site-1","revision":1}`),
	})
	if errorValue != nil || result.Failed() {
		t.Fatalf("expected valid descriptor input, got result=%+v error=%v", result, errorValue)
	}
	if handlerCallCount != 1 {
		t.Fatalf("expected one valid handler call, got %d", handlerCallCount)
	}
}

func TestProjectResourceEffectsSupportsCanonicalIdentityArrays(t *testing.T) {
	contract := &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"minItems":1,"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`),
		Effects: []ResourceEffectContract{{
			ObjectType:     "file",
			Effect:         "updated",
			ResultField:    "paths",
			EffectIdentity: "path",
		}},
	}
	effects := ProjectResourceEffects(contract, json.RawMessage(`{"paths":[" /workspace/one.md ","/workspace/two.md"]}`))
	expectedEffects := []ResourceEffect{
		{ObjectType: "file", Effect: "updated", Path: "/workspace/one.md"},
		{ObjectType: "file", Effect: "updated", Path: "/workspace/two.md"},
	}
	if !reflect.DeepEqual(effects, expectedEffects) {
		t.Fatalf("expected canonical projected effects, got %+v", effects)
	}
	for _, result := range []json.RawMessage{
		json.RawMessage(`{"paths":[]}`),
		json.RawMessage(`{"paths":[""]}`),
		json.RawMessage(`{"paths":["/workspace/one.md","/workspace/one.md"]}`),
		json.RawMessage(`{"paths":[1]}`),
	} {
		if effects := ProjectResourceEffects(contract, result); effects != nil {
			t.Fatalf("expected invalid identities to fail closed for %s, got %+v", result, effects)
		}
	}
}

func TestToolFunctionValidatesInputAndMarshalsOutput(t *testing.T) {
	toolSet := NewToolSet([]string{"echo.tool"})
	RegisterToolFunction(toolSet, ToolFunction[echoToolInput, echoToolOutput]{
		Definition: ToolDefinition{
			Name:           "echo.tool",
			Visibility:     ToolVisibilityModel,
			ResultContract: &ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`)},
		},
		Handler: func(_ context.Context, input echoToolInput) (echoToolOutput, error) {
			return echoToolOutput{Message: input.Message}, nil
		},
		Result: func(output echoToolOutput) ToolResult {
			data := json.RawMessage(marshalTypedToolOutput(output))
			return ToolSuccessData(string(data), data)
		},
	})

	malformedResult, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo.tool", Input: []byte(`{`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !malformedResult.Failed() || !strings.Contains(malformedResult.ContentText(), "tool input is not valid json") {
		t.Fatalf("expected malformed input error, got %+v", malformedResult)
	}

	unknownFieldResult, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo.tool", Input: []byte(`{"message":"hello","operation":"task.add"}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !unknownFieldResult.Failed() || !strings.Contains(unknownFieldResult.ContentText(), "unknown field") {
		t.Fatalf("expected unknown input field error, got %+v", unknownFieldResult)
	}

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo.tool", Input: MarshalToolInput(echoToolInput{Message: "hello"})})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.ContentText() != `{"message":"hello"}` {
		t.Fatalf("expected structured output json, got %+v", result)
	}
}
