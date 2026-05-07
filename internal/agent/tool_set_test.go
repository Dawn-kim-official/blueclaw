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
				return ToolResult{Content: "tool is not registered", IsError: true}, nil
			},
		})
	}
	return toolSet
}
