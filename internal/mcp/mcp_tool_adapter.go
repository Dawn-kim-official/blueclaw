package mcp

import "context"

type ToolAdapter struct {
	mcpRegistry *McpRegistry
}

func NewToolAdapter(mcpRegistry *McpRegistry) ToolAdapter {
	return ToolAdapter{mcpRegistry: mcpRegistry}
}

func (toolAdapter ToolAdapter) InvokeTool(ctx context.Context, serverName string, toolName string, input string) (string, error) {
	return toolAdapter.mcpRegistry.InvokeTool(ctx, Invocation{
		ServerName: serverName,
		ToolName:   toolName,
		Input:      input,
	})
}
