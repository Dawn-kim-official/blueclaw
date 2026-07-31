package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/mcp"
	"github.com/Dawn-kim-official/blueclaw/tests/support"
)

func TestMCPStdIOToolCall(t *testing.T) {
	command, arguments := support.FakeMCPCommand()
	mcpRegistry := mcp.NewMcpRegistry()
	t.Cleanup(func() { _ = mcpRegistry.Close() })
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name:      "echo",
			Transport: "stdio",
			Command:   command,
			Arguments: arguments,
			Tools:     []config.MCPToolConfiguration{fakeMCPEchoToolConfiguration()},
		},
	})
	if len(loadReport.Quarantined) != 0 {
		t.Fatalf("expected the stdio server to load, got %+v", loadReport.Quarantined)
	}

	output, errorValue := mcpRegistry.InvokeTool(context.Background(), mcp.Invocation{
		ServerName: "echo",
		ToolName:   "fake.echo",
		Input:      `{"text":"blueclaw"}`,
	})
	if errorValue != nil {
		t.Fatalf("expected stdio tool call to succeed: %v", errorValue)
	}

	toolResult, errorValue := mcp.ParseToolResult(output)
	if errorValue != nil {
		t.Fatalf("expected a normalized tool result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected a successful tool result, got %s", output)
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("expected one content item, got %s", output)
	}
	var textContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if errorValue := json.Unmarshal(toolResult.Content[0], &textContent); errorValue != nil {
		t.Fatalf("expected text content: %v", errorValue)
	}
	if textContent.Text != "blueclaw" {
		t.Fatalf("expected echoed text, got %q", textContent.Text)
	}
}

func fakeMCPEchoToolConfiguration() config.MCPToolConfiguration {
	echoSchema := json.RawMessage(support.FakeMCPEchoSchema)
	return config.MCPToolConfiguration{
		Name:         "echo",
		Namespace:    "fake",
		Description:  support.FakeMCPEchoDescription,
		InputSchema:  echoSchema,
		OutputSchema: echoSchema,
		ResultContract: &config.MCPToolResultContract{
			Schema: echoSchema,
		},
		Policy: &config.MCPToolPolicyMetadata{
			PrivacyClass:     "workspace",
			WorksOffline:     true,
			ModelVisibility:  "visible",
			PolicyResource:   "tool:fake.echo",
			SideEffectClass:  "read",
			CompletionMode:   "none",
			Idempotency:      "supported",
			IdempotencyScope: "operation",
		},
	}
}
