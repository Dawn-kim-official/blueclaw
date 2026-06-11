package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
)

func TestToolDescribeReturnsHiddenRegisteredToolSchema(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:        "site.app.create",
		Description: "Create a site.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "tool.describe",
		Input: agent.MarshalToolInput(map[string]any{
			"toolName": "site.app.create",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected tool.describe success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "site.app.create") || !strings.Contains(result.ContentText(), "slug") {
		t.Fatalf("expected site tool schema in result, got %s", result.ContentText())
	}
}

func TestToolDescribeFindsHiddenToolByRecoveryCardTokens(t *testing.T) {
	toolSet := agent.NewToolSet([]string{"tool.describe"})
	toolSet.RegisterTool(agent.ToolDefinition{
		Name:        "image.read",
		Description: "Read one referenced image material.",
		RecoveryCard: agent.ToolRecoveryCard{
			Does:    "Inspects visual image attachments by materialID.",
			UseWhen: "The user asks to analyze or describe a previously uploaded picture.",
		},
		InputSchema: json.RawMessage(`{"type":"object","properties":{"materialID":{"type":"string"}}}`),
	}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess(""), nil
	})
	toolSet.RegisterTool(agent.ToolDefinition{
		Name:        "tool.describe",
		Description: "Search registered tools.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
	}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess(marshalToolResult(describeTools(toolDescribeToolInput{Query: "visual analyze"}, toolSet))), nil
	})

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "tool.describe",
		Input: agent.MarshalToolInput(map[string]any{
			"query": "visual analyze",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(result.ContentText(), "image.read") || !strings.Contains(result.ContentText(), "query_tokens") {
		t.Fatalf("expected recovery card token search to find image.read, got %s", result.ContentText())
	}
}
