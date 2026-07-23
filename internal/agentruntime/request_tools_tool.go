package agentruntime

import (
	"context"
	"encoding/json"

	"blueclaw/internal/agent"
)

var requestToolsInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["toolNames"],
	"properties":{
		"toolNames":{
			"type":"array",
			"items":{"type":"string"},
			"description":"Exact tool names to load, taken from the additional-available-tools list"
		}
	}
}`)

var requestToolsResultSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["requestedToolNames"],
	"properties":{
		"requestedToolNames":{"type":"array","items":{"type":"string"}}
	}
}`)

type requestToolsToolInput struct {
	ToolNames []string `json:"toolNames"`
}

type requestToolsToolOutput struct {
	RequestedToolNames []string `json:"requestedToolNames"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerRequestToolsTool(toolRegistry *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[requestToolsToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        agent.RequestToolsToolName,
			Description: "Load additional tools into your palette by exact name. Use only names from the additional-available-tools list in your instructions; the loaded tools become callable on your next step.",
			InputSchema: requestToolsInputSchema,
		},
		Handler: func(_ context.Context, input requestToolsToolInput) (agent.ToolResult, error) {
			document := json.RawMessage(marshalToolResult(requestToolsToolOutput{RequestedToolNames: input.ToolNames}))
			return agent.ToolSuccessData(string(document), document), nil
		},
		Result: agent.IdentityToolResult,
	})
}
