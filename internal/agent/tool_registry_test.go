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
		return ToolResult{Content: "ok"}, nil
	})

	toolNames := toolSet.ListToolNames()
	if len(toolNames) != 1 || toolNames[0] != "registered.tool" {
		t.Fatalf("expected only registered exposed tool, got %+v", toolNames)
	}
	if toolSet.IsAllowed("missing.tool") {
		t.Fatal("expected unregistered allowed tool name to stay hidden")
	}
}

func TestToolSetDescriptionsAndActionSchemaShareExposedTools(t *testing.T) {
	toolSet := NewToolSet([]string{"visible.tool", "denied.tool"})
	toolSet.RegisterTool(ToolDefinition{Name: "visible.tool", Description: "Visible"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "ok"}, nil
	})
	toolSet.RegisterBoundTool(BoundTool{
		Definition:   ToolDefinition{Name: "denied.tool", Description: "Denied"},
		Availability: ToolAvailability{Status: ToolAvailabilityDenied, Reason: "policy"},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{Content: "denied"}, nil
		},
	})

	descriptions := toolSet.Descriptions()
	actionSchema := toolSet.ActionSchema(false, nil)
	if !strings.Contains(descriptions, "visible.tool") || !strings.Contains(actionSchema, "visible.tool") {
		t.Fatalf("expected visible tool in prompt and schema, got prompt=%s schema=%s", descriptions, actionSchema)
	}
	if strings.Contains(descriptions, "denied.tool") || strings.Contains(actionSchema, "denied.tool") {
		t.Fatalf("expected denied tool to stay hidden, got prompt=%s schema=%s", descriptions, actionSchema)
	}
}

func TestToolSetInvokeRejectsHiddenTool(t *testing.T) {
	toolSet := NewToolSet([]string{"visible.tool"})
	toolSet.RegisterTool(ToolDefinition{Name: "hidden.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "hidden"}, nil
	})

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "hidden.tool"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.IsError {
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
	if !malformedResult.IsError || !strings.Contains(malformedResult.Content, "tool input is not valid json") {
		t.Fatalf("expected malformed input error, got %+v", malformedResult)
	}

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "echo.tool", Input: MarshalToolInput(echoToolInput{Message: "hello"})})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Content != `{"message":"hello"}` {
		t.Fatalf("expected structured output json, got %+v", result)
	}
}
