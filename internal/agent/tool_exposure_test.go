package agent

import (
	"context"
	"encoding/json"
)

func newExposureTestToolSet(operationSchemas map[string]json.RawMessage) *ToolSet {
	toolSet := NewToolSet(KernelToolNames())
	for _, kernelToolName := range KernelToolNames() {
		toolSet.RegisterBoundTool(BoundTool{
			Definition: ToolDefinition{Name: kernelToolName},
			Handler:    func(context.Context, ToolInvocation) (ToolResult, error) { return ToolSuccess("ok"), nil },
		})
	}
	for operationName, inputSchema := range operationSchemas {
		toolSet.RegisterBoundTool(BoundTool{
			Definition: ToolDefinition{
				Name:        operationName,
				InputSchema: inputSchema,
			},
			Handler: func(context.Context, ToolInvocation) (ToolResult, error) { return ToolSuccess("ok"), nil },
		})
	}
	return toolSet
}
