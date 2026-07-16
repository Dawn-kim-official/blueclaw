package agent

import (
	"context"
	"strings"
)

func newTestToolSet(allowedToolNames []string) *ToolSet {
	toolSet := NewToolSet(allowedToolNames)
	for _, toolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		toolSet.RegisterBoundTool(BoundTool{
			Definition:   ToolDefinition{Name: trimmedToolName},
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
	for _, operationName := range operationNames {
		trimmedOperationName := strings.TrimSpace(operationName)
		if trimmedOperationName == "" {
			continue
		}
		toolSet.RegisterTool(ToolDefinition{Name: trimmedOperationName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	return toolSet
}
