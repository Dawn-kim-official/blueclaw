package agent

import (
	"context"
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
	toolSet.RegisterTool(ToolDefinition{Name: "registered.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})

	toolNames := toolSet.ListToolNames()
	if len(toolNames) != 1 || toolNames[0] != "registered.tool" {
		t.Fatalf("expected only registered exposed tool, got %+v", toolNames)
	}
	if toolSet.IsAllowed("missing.tool") {
		t.Fatal("expected unregistered allowed tool name to stay hidden")
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

func TestToolSetDescriptionsShowRegisteredCatalogWhileActionSchemaUsesExposedTools(t *testing.T) {
	toolSet := NewToolSet([]string{"visible.tool", "denied.tool"})
	toolSet.RegisterTool(ToolDefinition{Name: "visible.tool", Description: "Visible"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})
	toolSet.RegisterTool(ToolDefinition{Name: "hidden.tool", Description: "Hidden"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
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
	if !strings.Contains(descriptions, "hidden.tool") || strings.Contains(actionSchema, "hidden.tool") {
		t.Fatalf("expected hidden tool in prompt catalog but not action schema, got prompt=%s schema=%s", descriptions, actionSchema)
	}
	if !strings.Contains(descriptions, "hidden.tool: Hidden [hidden, available]") {
		t.Fatalf("expected hidden visibility marker, got prompt=%s", descriptions)
	}
	if strings.Contains(descriptions, "denied.tool") || strings.Contains(actionSchema, "denied.tool") {
		t.Fatalf("expected denied tool to stay hidden, got prompt=%s schema=%s", descriptions, actionSchema)
	}
	if got := strings.Join(toolSet.ListHiddenDescribedToolNames(), ","); got != "hidden.tool" {
		t.Fatalf("expected hidden described tool names, got %q", got)
	}
}

func TestToolSetDoesNotExposeAskConfirm(t *testing.T) {
	toolSet := NewToolSet([]string{"ask.confirm"})
	toolSet.RegisterTool(ToolDefinition{Name: "ask.confirm", Description: "Confirm"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})

	descriptions := toolSet.Descriptions()
	actionSchema := toolSet.ActionSchema(false, nil, false)

	if toolSet.IsAllowed("ask.confirm") || toolSet.CanExpose("ask.confirm") {
		t.Fatalf("expected ask.confirm not to be model-callable, names=%+v", toolSet.ListToolNames())
	}
	if strings.Contains(descriptions, "ask.confirm") || strings.Contains(actionSchema, "ask.confirm") {
		t.Fatalf("expected ask.confirm to stay out of prompt surfaces, prompt=%s schema=%s", descriptions, actionSchema)
	}
}

func TestFallbackActionSchemaDoesNotAllowToolCalls(t *testing.T) {
	actionSchema := buildActionSchemaFromToolDefinitions(nil, false, nil, false)
	if strings.Contains(actionSchema, "continue") {
		t.Fatalf("expected fallback schema to omit continue, got %s", actionSchema)
	}
}

func TestFlowTaskUpdateActionSchemaAndCompletionEvidence(t *testing.T) {
	actionSchema := buildActionSchemaFromToolDefinitions([]ToolDefinition{{Name: "task.update"}}, false, nil, false)
	for _, fragment := range []string{"task.update", "taskID", "query", "content", "status", "endDate"} {
		if !strings.Contains(actionSchema, fragment) {
			t.Fatalf("expected action schema to include %q, got %s", fragment, actionSchema)
		}
	}
	if !isOneShotCompletionEvidenceTool("task.update") {
		t.Fatal("expected task.update to count as one-shot completion evidence")
	}
}

func TestDefaultToolSideEffectClass(t *testing.T) {
	tests := []struct {
		toolName           string
		expectedSideEffect string
		requiresCompletion bool
	}{
		{toolName: "task.add", expectedSideEffect: ToolSideEffectStateChange, requiresCompletion: true},
		{toolName: "task.list", expectedSideEffect: ToolSideEffectRead, requiresCompletion: false},
		{toolName: "message.send", expectedSideEffect: ToolSideEffectExternalWrite, requiresCompletion: true},
		{toolName: "math.calculate", expectedSideEffect: ToolSideEffectComputation, requiresCompletion: false},
	}

	for _, test := range tests {
		toolDefinition := ToolDefinition{Name: test.toolName}
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
	toolSet.RegisterTool(ToolDefinition{
		Name:         "data.write",
		RecoveryCard: ToolRecoveryCard{SideEffect: ToolSideEffectRead},
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
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
	toolSet.RegisterTool(ToolDefinition{Name: "hidden.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("hidden"), nil
	})

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "hidden.tool"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected hidden tool invocation to fail, got %+v", result)
	}
}

func TestToolFunctionValidatesInputAndMarshalsOutput(t *testing.T) {
	toolSet := NewToolSet([]string{"echo.tool"})
	RegisterToolFunction(toolSet, ToolFunction[echoToolInput, echoToolOutput]{
		Definition: ToolDefinition{Name: "echo.tool"},
		Handler: func(_ context.Context, input echoToolInput) (echoToolOutput, error) {
			return echoToolOutput{Message: input.Message}, nil
		},
	})

	malformedResult, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo.tool", Input: []byte(`{`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !malformedResult.Failed() || !strings.Contains(malformedResult.ContentText(), "tool input is not valid json") {
		t.Fatalf("expected malformed input error, got %+v", malformedResult)
	}

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo.tool", Input: MarshalToolInput(echoToolInput{Message: "hello"})})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.ContentText() != `{"message":"hello"}` {
		t.Fatalf("expected structured output json, got %+v", result)
	}
}
