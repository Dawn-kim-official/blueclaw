package bluecollar

import (
	"context"
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"os"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
)

func TestLLMDLiveXLowCurrentAgentActionSchemaFromEnv(t *testing.T) {
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_LLMD_LIVE_SOCKET"))
	authKey := strings.TrimSpace(os.Getenv("BLUECLAW_LLMD_LIVE_AUTH_KEY"))
	if socketPath == "" || authKey == "" {
		t.Skip("BLUECLAW_LLMD_LIVE_SOCKET and BLUECLAW_LLMD_LIVE_AUTH_KEY are required")
	}
	toolSet := toolcontract.NewToolSet([]string{toolcontract.TerminalRunToolName})
	registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        toolcontract.TerminalRunToolName,
		Description: "Run a terminal command.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("not executed"), nil
	})
	request := BuildAgentActionRequest(agentTaskState{Request: AgentTurnRequest{
		Prompt:  "Do not finish. Choose continue, call terminal_run, and set command to printf llmd-schema-ok.",
		ToolSet: toolSet,
	}})
	client := llm.NewLLMDClient(llm.LLMDClientConfiguration{
		UnixSocketPath: socketPath,
		AuthKey:        authKey,
		ModelName:      llm.DefaultModelTierNames().XLow,
		ExecutionMode:  "remote",
	})
	response, errorValue := client.GenerateStructuredResponse(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected llmd xlow response for current action schema: %v", errorValue)
	}
	action, errorValue := ParseAgentActionResponse(response)
	if errorValue != nil {
		t.Fatalf("expected parsable llmd agent action, got %q: %v", response.Content, errorValue)
	}
	if action.Action != "continue" || action.ToolName != toolcontract.TerminalRunToolName {
		t.Fatalf("expected llmd terminal_run continue action, got %+v", action)
	}
}
