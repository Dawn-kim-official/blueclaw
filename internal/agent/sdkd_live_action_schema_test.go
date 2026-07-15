package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/llm"
)

func TestSDKDLiveXLowCurrentAgentActionSchemaFromEnv(t *testing.T) {
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_SDKD_LIVE_SOCKET"))
	authKey := strings.TrimSpace(os.Getenv("BLUECLAW_SDKD_LIVE_AUTH_KEY"))
	if socketPath == "" || authKey == "" {
		t.Skip("BLUECLAW_SDKD_LIVE_SOCKET and BLUECLAW_SDKD_LIVE_AUTH_KEY are required")
	}
	toolSet := NewToolSet([]string{TerminalRunToolName})
	toolSet.RegisterTool(ToolDefinition{
		Name:        TerminalRunToolName,
		Description: "Run a terminal command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("not executed"), nil
	})
	request := BuildAgentActionRequest(agentTaskState{Request: AgentTurnRequest{
		Prompt:  "Do not finish. Choose continue, call terminal.run, and set command to printf sdkd-schema-ok.",
		ToolSet: toolSet,
	}})
	client := llm.NewSDKDClient(llm.SDKDClientConfiguration{
		UnixSocketPath: socketPath,
		AuthKey:        authKey,
		ModelName:      llm.ResolveModelTierNames(config.RuntimeConfiguration{}).XLow,
		ExecutionMode:  "remote",
	})
	response, errorValue := client.GenerateStructuredResponse(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected sdkd xlow response for current action schema: %v", errorValue)
	}
	action, errorValue := ParseAgentActionResponse(response)
	if errorValue != nil {
		t.Fatalf("expected parsable sdkd agent action, got %q: %v", response.Content, errorValue)
	}
	if action.Action != "continue" || action.ToolName != TerminalRunToolName {
		t.Fatalf("expected sdkd terminal.run continue action, got %+v", action)
	}
}
