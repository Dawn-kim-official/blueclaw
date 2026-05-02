package mcp

import (
	"encoding/json"
	"testing"

	"blueclaw/internal/config"
)

func TestMcpRegistryBuildsSchemaAwareToolCatalog(t *testing.T) {
	mcpRegistry := NewMcpRegistry()
	inputSchema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
	mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name:      "local-tools",
			Transport: "stdio",
			Command:   "tool-server",
			ToolNames: []string{"legacy.echo", "schema.echo"},
			Tools: []config.MCPToolConfiguration{
				{
					Name:        "schema.echo",
					Description: "Echo text with a schema",
					InputSchema: inputSchema,
				},
			},
		},
	})

	toolDefinitions := mcpRegistry.ListTool()
	schemaToolDefinition, isFound := findMcpToolDefinition(toolDefinitions, "schema.echo")
	if !isFound {
		t.Fatalf("expected schema tool definition, got %+v", toolDefinitions)
	}
	if schemaToolDefinition.ServerName != "local-tools" {
		t.Fatalf("expected server name, got %q", schemaToolDefinition.ServerName)
	}
	if schemaToolDefinition.Description != "Echo text with a schema" {
		t.Fatalf("expected description, got %q", schemaToolDefinition.Description)
	}
	if string(schemaToolDefinition.InputSchema) != string(inputSchema) {
		t.Fatalf("expected input schema, got %s", string(schemaToolDefinition.InputSchema))
	}

	legacyToolDefinition, isFound := findMcpToolDefinition(toolDefinitions, "legacy.echo")
	if !isFound {
		t.Fatalf("expected legacy tool definition, got %+v", toolDefinitions)
	}
	if legacyToolDefinition.Description != "" || len(legacyToolDefinition.InputSchema) != 0 {
		t.Fatalf("expected static legacy tool to remain minimal, got %+v", legacyToolDefinition)
	}
}

func findMcpToolDefinition(toolDefinitions []ToolDefinition, toolName string) (ToolDefinition, bool) {
	for _, toolDefinition := range toolDefinitions {
		if toolDefinition.Name == toolName {
			return toolDefinition, true
		}
	}
	return ToolDefinition{}, false
}
