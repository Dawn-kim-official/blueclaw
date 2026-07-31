package integration

import (
	"context"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/mcp"
	"github.com/Dawn-kim-official/blueclaw/tests/support"
)

func TestMCPStdIOToolCall(t *testing.T) {
	mcpRegistry := mcp.NewMcpRegistry()
	mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name:      "echo",
			Transport: "stdio",
			Command:   support.FakeMCPCommand(),
		},
	})

	output, errorValue := mcpRegistry.InvokeTool(context.Background(), mcp.Invocation{
		ServerName: "echo",
		ToolName:   "echo",
		Input:      "blueclaw",
	})
	if errorValue != nil {
		t.Fatalf("expected stdio tool call to succeed: %v", errorValue)
	}
	if output != "blueclaw" {
		t.Fatalf("expected echoed output, got %q", output)
	}
}
