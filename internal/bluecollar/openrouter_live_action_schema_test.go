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

func TestOpenRouterLiveLowTierCurrentAgentActionSchemaFromEnv(t *testing.T) {
	if os.Getenv("BLUECLAW_LIVE_LLM_TEST") != "1" {
		t.Skip("set BLUECLAW_LIVE_LLM_TEST=1 to run the low-tier action schema test")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the low-tier action schema test")
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
		Prompt:  "Do not finish. Choose continue, call terminal_run, and set command to printf low-tier-schema-ok.",
		ToolSet: toolSet,
	}})
	modelName := llm.DefaultModelTierNames().XLow
	client := llm.OpenRouterClient{
		APIKey:       apiKey,
		BaseURL:      llm.DefaultOpenRouterChatCompletionsURL,
		ModelName:    modelName,
		AttemptCount: 1,
	}
	response, errorValue := client.GenerateStructuredResponse(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected low-tier response for current action schema: %v", errorValue)
	}
	action, errorValue := ParseAgentActionResponse(response)
	if errorValue != nil {
		t.Fatalf("expected parsable agent action, got %q: %v", response.Content, errorValue)
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
