package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/mcp"
)

type mcpToolProviderTestInvoker struct {
	output string
	error  error
}

func (invoker mcpToolProviderTestInvoker) InvokeTool(context.Context, mcp.Invocation) (string, error) {
	return invoker.output, invoker.error
}

func TestMCPToolProviderMapsIsErrorToToolFailure(t *testing.T) {
	provider := mcpToolProvider{
		serverName: "workspace",
		registry: mcpToolProviderTestInvoker{
			output: `{"content":[],"isError":true}`,
		},
		definitions: []mcp.ToolDefinition{{
			Name:        "workspace.echo",
			Namespace:   "workspace",
			Description: "Echo workspace text",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:    "workspace",
				ModelVisibility: agent.ToolVisibilityModel,
				PolicyResource:  "tool:workspace.echo",
				SideEffectClass: agent.ToolSideEffectRead,
				CompletionMode:  agent.ToolCompletionNone,
				Idempotency:     agent.ToolIdempotencySupported,
			},
		}},
	}

	result, errorValue := provider.boundTool(provider.definitions[0]).Handler(context.Background(), agent.ToolInvocation{Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Code != agent.FailureCodes.OperationFailed.String() {
		t.Fatalf("expected failed MCP result, got %+v", result)
	}
}

func TestMCPToolProviderUsesCanonicalDescriptor(t *testing.T) {
	provider := mcpToolProvider{
		serverName: "workspace",
		definitions: []mcp.ToolDefinition{{
			Name:        "workspace.echo",
			Namespace:   "workspace",
			ServerName:  "workspace",
			Description: "Echo workspace text",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:    "workspace",
				ModelVisibility: agent.ToolVisibilityModel,
				PolicyResource:  "tool:workspace.echo",
				SideEffectClass: agent.ToolSideEffectRead,
				CompletionMode:  agent.ToolCompletionNone,
				Idempotency:     agent.ToolIdempotencySupported,
			},
		}},
	}
	toolSet := agent.NewToolSet([]string{"workspace.echo"})

	errorValue := toolSet.RegisterProvider(context.Background(), provider)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("workspace.echo")
	if !isFound {
		t.Fatal("expected MCP tool")
	}
	if descriptor.ID != "mcp/workspace/workspace.echo" || descriptor.ProviderID != "mcp:workspace" {
		t.Fatalf("unexpected identity: %+v", descriptor)
	}
	if len(descriptor.OutputSchema) == 0 || descriptor.Visibility != agent.ToolVisibilityModel {
		t.Fatalf("expected complete MCP descriptor: %+v", descriptor)
	}
}

func TestMCPToolProviderCollisionQuarantinesExternalServer(t *testing.T) {
	toolSet := agent.NewToolSet([]string{"file.read"})
	toolSet.RegisterTool(agent.ToolDefinition{Name: "file.read"}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess("ok"), nil
	})
	provider := mcpToolProvider{
		serverName: "collision",
		definitions: []mcp.ToolDefinition{{
			Name:        "file.read",
			Namespace:   "file",
			ServerName:  "collision",
			Description: "Colliding file reader",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:    "workspace",
				ModelVisibility: agent.ToolVisibilityModel,
				PolicyResource:  "tool:file.read",
				SideEffectClass: agent.ToolSideEffectRead,
				CompletionMode:  agent.ToolCompletionNone,
				Idempotency:     agent.ToolIdempotencySupported,
			},
		}},
	}

	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []agent.ToolProviderRegistration{{
		Provider: provider,
		Trust:    agent.ToolProviderExternal,
	}})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 1 || quarantinedProviders[0].ProviderID != "mcp:collision" {
		t.Fatalf("expected collision quarantine, got %+v", quarantinedProviders)
	}
	if len(toolSet.QuarantinedProviders()) != 1 {
		t.Fatalf("expected quarantine evidence on tool set, got %+v", toolSet.QuarantinedProviders())
	}
}
