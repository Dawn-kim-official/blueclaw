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

func TestOpenRouterLiveLowTierCurrentAgentActionSchemaFromEnv(t *testing.T) {
	if os.Getenv("BLUECLAW_LIVE_LLM_TEST") != "1" {
		t.Skip("set BLUECLAW_LIVE_LLM_TEST=1 to run the low-tier action schema test")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the low-tier action schema test")
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
		Prompt:  "Do not finish. Choose continue, call terminal.run, and set command to printf low-tier-schema-ok.",
		ToolSet: toolSet,
	}})
	modelName := llm.ResolveModelTierNames(config.RuntimeConfiguration{}).XLow
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
	if action.Action != "continue" || action.ToolName != TerminalRunToolName {
		t.Fatalf("expected terminal.run continue action, got %+v", action)
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
