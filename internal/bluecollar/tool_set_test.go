package bluecollar

import (
	"context"
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
)

func newTestToolSet(allowedToolNames []string) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet(allowedToolNames)
	toolSet.AllowTestReplacement()
	for _, toolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		toolSet.RegisterBoundTool(toolcontract.BoundTool{
			Definition:   testToolDescriptor(trimmedToolName),
			Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
			Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
			},
		})
	}
	return toolSet
}

func newTestCapabilityToolSet(operationNames []string) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet(operationNames)
	toolSet.AllowTestReplacement()
	for _, operationName := range operationNames {
		trimmedOperationName := strings.TrimSpace(operationName)
		if trimmedOperationName == "" {
			continue
		}
		registerTestTool(toolSet, testToolDescriptor(trimmedOperationName), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func newTestToolSetWithDefinitions(definitions []toolcontract.ToolDefinition) *toolcontract.ToolSet {
	toolNames := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		toolNames = append(toolNames, definition.Name)
	}
	toolSet := toolcontract.NewToolSet(toolNames)
	toolSet.AllowTestReplacement()
	for _, definition := range definitions {
		if len(definition.InputSchema) == 0 {
			definition.InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if len(definition.OutputSchema) == 0 {
			definition.OutputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if toolcontract.ToolDescriptorRequiresInputIntentSchema(definition) && len(definition.InputIntentSchema) == 0 {
			definition.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		registerTestTool(toolSet, definition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func testToolDescriptor(toolName string) toolcontract.ToolDefinition {
	return toolcontract.ToolDefinition{
		ID:                "test:" + toolName,
		Name:              toolName,
		Visibility:        toolcontract.ToolVisibilityModel,
		InputSchema:       json.RawMessage(`{"type":"object","properties":{}}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract:    testToolResultContract(),
		SideEffectClass:   testToolSideEffectClass(toolName),
	}
}

func testToolResultContract() *toolcontract.ToolResultContract {
	return &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func testToolSuccess(content string) toolcontract.ToolResult {
	return toolcontract.ToolSuccessData(content, json.RawMessage(`{}`))
}

func registerTestTool(toolSet *toolcontract.ToolSet, definition toolcontract.ToolDefinition, handler toolcontract.ToolHandler) error {
	if definition.Visibility == "" {
		definition.Visibility = toolcontract.ToolVisibilityModel
	}
	if definition.Visibility == toolcontract.ToolVisibilityModel && definition.ResultContract == nil {
		definition.ResultContract = testToolResultContract()
	}
	return toolSet.RegisterTool(definition, func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		result, errorValue := handler(toolContext, invocation)
		if errorValue == nil && !result.Failed() && len(result.Output.Data) == 0 {
			result.Output.Data = json.RawMessage(`{}`)
		}
		return result, errorValue
	})
}

func testExternalSendToolDefinition(toolName string) toolcontract.ToolDefinition {
	definition := testToolDescriptor(toolName)
	definition.SideEffectClass = toolcontract.ToolSideEffectExternalSend
	definition.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	return definition
}

func testToolSideEffectClass(toolName string) string {
	for _, suffix := range []string{"_list", "_read", "_search", "_status", "_history", "_preview", "_snapshot"} {
		if strings.HasSuffix(toolName, suffix) {
			return toolcontract.ToolSideEffectRead
		}
	}
	for _, suffix := range []string{"_calculate", "_compare", "_classify"} {
		if strings.HasSuffix(toolName, suffix) {
			return toolcontract.ToolSideEffectComputation
		}
	}
	return toolcontract.ToolSideEffectStateChange
}
