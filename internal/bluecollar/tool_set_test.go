package bluecollar

import (
	"context"
	"encoding/json"
	"strings"
)

func newTestToolSet(allowedToolNames []string) *ToolSet {
	toolSet := NewToolSet(allowedToolNames)
	toolSet.allowsTestReplacement = true
	for _, toolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		toolSet.RegisterBoundTool(BoundTool{
			Definition:   testToolDescriptor(trimmedToolName),
			Availability: ToolAvailability{Status: ToolAvailabilityAvailable},
			Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
				return ToolFailureResult(FailureUnknown, FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
			},
		})
	}
	return toolSet
}

func newTestCapabilityToolSet(operationNames []string) *ToolSet {
	toolSet := NewToolSet(operationNames)
	toolSet.allowsTestReplacement = true
	for _, operationName := range operationNames {
		trimmedOperationName := strings.TrimSpace(operationName)
		if trimmedOperationName == "" {
			continue
		}
		registerTestTool(toolSet, testToolDescriptor(trimmedOperationName), func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func newTestToolSetWithDefinitions(definitions []ToolDefinition) *ToolSet {
	toolNames := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		toolNames = append(toolNames, definition.Name)
	}
	toolSet := NewToolSet(toolNames)
	toolSet.allowsTestReplacement = true
	for _, definition := range definitions {
		if len(definition.InputSchema) == 0 {
			definition.InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if len(definition.OutputSchema) == 0 {
			definition.OutputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if ToolDescriptorRequiresInputIntentSchema(definition) && len(definition.InputIntentSchema) == 0 {
			definition.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		registerTestTool(toolSet, definition, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func testToolDescriptor(toolName string) ToolDefinition {
	return ToolDefinition{
		ID:                "test:" + toolName,
		Name:              toolName,
		Visibility:        ToolVisibilityModel,
		InputSchema:       json.RawMessage(`{"type":"object","properties":{}}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract:    testToolResultContract(),
		SideEffectClass:   testToolSideEffectClass(toolName),
	}
}

func testToolResultContract() *ToolResultContract {
	return &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func testToolSuccess(content string) ToolResult {
	return ToolSuccessData(content, json.RawMessage(`{}`))
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

func testExternalSendToolDefinition(toolName string) ToolDefinition {
	definition := testToolDescriptor(toolName)
	definition.SideEffectClass = ToolSideEffectExternalSend
	definition.Completion = ToolCompletion{Mode: ToolCompletionObservation}
	return definition
}

func testToolSideEffectClass(toolName string) string {
	for _, suffix := range []string{".list", ".read", ".search", ".status", ".history", ".preview", ".snapshot"} {
		if strings.HasSuffix(toolName, suffix) {
			return ToolSideEffectRead
		}
	}
	for _, suffix := range []string{".calculate", ".compare", ".classify"} {
		if strings.HasSuffix(toolName, suffix) {
			return ToolSideEffectComputation
		}
	}
	return ToolSideEffectStateChange
}
