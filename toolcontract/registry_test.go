package toolcontract

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
	toolSet := NewToolSet([]string{"registered_tool", "missing_tool"})
	registerTestTool(toolSet, ToolDefinition{Name: "registered_tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})

	toolNames := toolSet.ListToolNames()
	if len(toolNames) != 1 || toolNames[0] != "registered_tool" {
		t.Fatalf("expected only registered exposed tool, got %+v", toolNames)
	}
	if toolSet.IsAllowed("missing.tool") {
		t.Fatal("expected unregistered allowed tool name to stay hidden")
	}
}

func TestDirectToolRegistrationIsNotModelCallableWithoutResultContract(t *testing.T) {
	toolSet := NewToolSet([]string{"internal_tool"})
	if errorValue := toolSet.RegisterTool(ToolDefinition{Name: "internal_tool", Visibility: ToolVisibilityModel}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	if !toolSet.IsRegistered("internal_tool") {
		t.Fatal("expected direct tool registration to remain available internally")
	}
	if toolSet.IsAllowed("internal_tool") || toolSet.CanExpose("internal_tool") {
		t.Fatal("expected direct tool without a result contract to stay off model surfaces")
	}
}

func TestToolSetRejectsDuplicateToolNamesWithoutReplacingTheFirstHandler(t *testing.T) {
	toolSet := NewToolSet([]string{"registered_tool"})
	if errorValue := registerTestTool(toolSet, ToolDefinition{Name: "registered_tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("first"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	errorValue := registerTestTool(toolSet, ToolDefinition{Name: "registered_tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("second"), nil
	})
	if errorValue == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	result, invocationError := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "registered_tool"})
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

func TestToolSetDescriptionsUseDescriptorDescription(t *testing.T) {
	toolSet := NewToolSet([]string{"task_update"})
	registerTestTool(toolSet, ToolDefinition{
		Name:        "task_update",
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

func TestToolSideEffectClassUsesOnlyDescriptorMetadata(t *testing.T) {
	tests := []struct {
		toolName           string
		sideEffectClass    string
		expectedSideEffect string
		requiresCompletion bool
	}{
		{toolName: "task_add", sideEffectClass: ToolSideEffectStateChange, expectedSideEffect: ToolSideEffectStateChange, requiresCompletion: true},
		{toolName: "task_list", sideEffectClass: ToolSideEffectRead, expectedSideEffect: ToolSideEffectRead, requiresCompletion: false},
		{toolName: "message_send", sideEffectClass: ToolSideEffectExternalWrite, expectedSideEffect: ToolSideEffectExternalWrite, requiresCompletion: true},
		{toolName: "llm_structured", sideEffectClass: ToolSideEffectComputation, expectedSideEffect: ToolSideEffectComputation, requiresCompletion: false},
		{toolName: "looks_like_write", expectedSideEffect: "", requiresCompletion: false},
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
	toolSet := NewToolSet([]string{"data_write"})
	registerTestTool(toolSet, ToolDefinition{
		Name:         "data_write",
		RecoveryCard: ToolRecoveryCard{SideEffect: ToolSideEffectRead},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("ok"), nil
	})

	toolDefinition, isFound := toolSet.ToolDefinition("data_write")
	if !isFound {
		t.Fatal("expected tool definition")
	}
	if actualSideEffect := ToolDefinitionSideEffectClass(toolDefinition); actualSideEffect != ToolSideEffectRead {
		t.Fatalf("expected declared side effect %s, got %s", ToolSideEffectRead, actualSideEffect)
	}
}

func TestToolSetInvokeRejectsHiddenTool(t *testing.T) {
	toolSet := NewToolSet([]string{"visible_tool"})
	registerTestTool(toolSet, ToolDefinition{Name: "hidden_tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess("hidden"), nil
	})

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "hidden_tool"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected hidden tool invocation to fail, got %+v", result)
	}
}

func TestToolSetValidatesDescriptorInputSchemaBeforeHandler(t *testing.T) {
	toolSet := NewToolSet([]string{"site_serve"})
	handlerCallCount := 0
	registerTestTool(toolSet, ToolDefinition{
		Name: "site_serve",
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
		result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "site_serve", Input: input})
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
		ToolName: "site_serve",
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
	toolSet := NewToolSet([]string{"echo_tool"})
	RegisterToolFunction(toolSet, ToolFunction[echoToolInput, echoToolOutput]{
		Definition: ToolDefinition{
			Name:           "echo_tool",
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

	malformedResult, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo_tool", Input: []byte(`{`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !malformedResult.Failed() || !strings.Contains(malformedResult.ContentText(), "tool input is not valid json") {
		t.Fatalf("expected malformed input error, got %+v", malformedResult)
	}

	unknownFieldResult, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo_tool", Input: []byte(`{"message":"hello","operation":"task_add"}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !unknownFieldResult.Failed() || !strings.Contains(unknownFieldResult.ContentText(), "unknown field") {
		t.Fatalf("expected unknown input field error, got %+v", unknownFieldResult)
	}

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo_tool", Input: MarshalToolInput(echoToolInput{Message: "hello"})})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.ContentText() != `{"message":"hello"}` {
		t.Fatalf("expected structured output json, got %+v", result)
	}
}

func registerTestTool(toolSet *ToolSet, definition ToolDefinition, handler ToolHandler) error {
	if definition.Visibility == "" {
		definition.Visibility = ToolVisibilityModel
	}
	if definition.Visibility == ToolVisibilityModel && definition.ResultContract == nil {
		definition.ResultContract = testToolResultContract()
	}
	return toolSet.RegisterTool(definition, func(toolContext context.Context, invocation ToolInvocation) (ToolResult, error) {
		result, errorValue := handler(toolContext, invocation)
		if errorValue == nil && !result.Failed() && len(result.Output.Data) == 0 {
			result.Output.Data = json.RawMessage(`{}`)
		}
		return result, errorValue
	})
}

func testToolResultContract() *ToolResultContract {
	return &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func testToolSuccess(content string) ToolResult {
	return ToolSuccessData(content, json.RawMessage(`{}`))
}
