package mcp

import (
	"context"
	"errors"
	"sync"

	"blueclaw/internal/config"
)

type McpRegistry struct {
	mutex            sync.RWMutex
	serverClient     ServerClient
	serverDefinition map[string]ServerDefinition
}

func NewMcpRegistry() *McpRegistry {
	return &McpRegistry{
		serverClient:     ServerClient{},
		serverDefinition: map[string]ServerDefinition{},
	}
}

func (mcpRegistry *McpRegistry) LoadServerDefinition(configurations []config.MCPServerConfiguration) {
	mcpRegistry.mutex.Lock()
	defer mcpRegistry.mutex.Unlock()

	for _, configuration := range configurations {
		mcpRegistry.serverDefinition[configuration.Name] = ServerDefinition{
			Name:      configuration.Name,
			Transport: configuration.Transport,
			Command:   configuration.Command,
			Arguments: configuration.Arguments,
			Endpoint:  configuration.Endpoint,
		}
	}
}

func (mcpRegistry *McpRegistry) ListTool() []ToolDefinition {
	mcpRegistry.mutex.RLock()
	defer mcpRegistry.mutex.RUnlock()

	toolDefinitions := []ToolDefinition{}
	for _, serverDefinition := range mcpRegistry.serverDefinition {
		for _, toolName := range serverDefinition.ToolNames {
			toolDefinitions = append(toolDefinitions, ToolDefinition{
				Name:       toolName,
				ServerName: serverDefinition.Name,
			})
		}
	}

	return toolDefinitions
}

func (mcpRegistry *McpRegistry) InvokeTool(ctx context.Context, invocation Invocation) (string, error) {
	mcpRegistry.mutex.RLock()
	serverDefinition, isFound := mcpRegistry.serverDefinition[invocation.ServerName]
	mcpRegistry.mutex.RUnlock()
	if !isFound {
		return "", errors.New("mcp server definition not found")
	}

	return mcpRegistry.serverClient.InvokeTool(ctx, serverDefinition, invocation)
}
