package bluecollarharness

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"github.com/Dawn-kim-official/bluecollar"
)

func newTerminalRunProbeRequest(t *testing.T, prompt string) agentcontract.AgentTurnRequest {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{toolcontract.TerminalRunToolName})
	definition := toolcontract.ToolDefinition{
		Name:        toolcontract.TerminalRunToolName,
		Description: "Run a terminal command.",
		Visibility:  toolcontract.ToolVisibilityModel,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
	}
	registerErrorValue := toolSet.RegisterTool(definition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("not executed", json.RawMessage(`{}`)), nil
	})
	if registerErrorValue != nil {
		t.Fatalf("expected the probe tool to register: %v", registerErrorValue)
	}
	return agentcontract.AgentTurnRequest{Prompt: prompt, ToolSet: toolSet}
}

func TestLLMDLiveXLowCurrentAgentActionSchemaFromEnv(t *testing.T) {
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_LLMD_LIVE_SOCKET"))
	authKey := strings.TrimSpace(os.Getenv("BLUECLAW_LLMD_LIVE_AUTH_KEY"))
	if socketPath == "" || authKey == "" {
		t.Skip("BLUECLAW_LLMD_LIVE_SOCKET and BLUECLAW_LLMD_LIVE_AUTH_KEY are required")
	}

	request := newTerminalRunProbeRequest(t, "Do not finish. Choose continue, call terminal_run, and set command to printf llmd-schema-ok.")
	client := llm.NewLLMDClient(llm.LLMDClientConfiguration{
		UnixSocketPath: socketPath,
		AuthKey:        authKey,
		ModelName:      llm.DefaultModelTierNames().XLow,
		ExecutionMode:  "remote",
	})

	action, errorValue := bluecollar.ProbeAgentActionSchema(context.Background(), client, request)
	if errorValue != nil {
		t.Fatalf("expected llmd xlow response for current action schema: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != toolcontract.TerminalRunToolName {
		t.Fatalf("expected llmd terminal_run continue action, got %+v", action)
	}
}

func TestOpenRouterLiveLowTierCurrentAgentActionSchemaFromEnv(t *testing.T) {
	if os.Getenv("BLUECLAW_LIVE_LLM_TEST") != "1" {
		t.Skip("set BLUECLAW_LIVE_LLM_TEST=1 to run the low-tier action schema test")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the low-tier action schema test")
	}

	request := newTerminalRunProbeRequest(t, "Do not finish. Choose continue, call terminal_run, and set command to printf low-tier-schema-ok.")
	client := llm.OpenRouterClient{
		APIKey:       apiKey,
		BaseURL:      llm.DefaultOpenRouterChatCompletionsURL,
		ModelName:    llm.DefaultModelTierNames().XLow,
		AttemptCount: 1,
	}

	action, errorValue := bluecollar.ProbeAgentActionSchema(context.Background(), client, request)
	if errorValue != nil {
		t.Fatalf("expected low-tier response for current action schema: %v", errorValue)
	}
	if action.Action != "continue" || action.ToolName != toolcontract.TerminalRunToolName {
		t.Fatalf("expected terminal_run continue action, got %+v", action)
	}
	var toolInput struct {
		Command string `json:"command"`
	}
	if errorValue := json.Unmarshal(action.ToolInput, &toolInput); errorValue != nil {
		t.Fatalf("expected terminal input, got %s: %v", action.ToolInput, errorValue)
	}
	if strings.TrimSpace(toolInput.Command) == "" {
		t.Fatalf("expected non-empty terminal command, got %s", action.ToolInput)
	}
}
