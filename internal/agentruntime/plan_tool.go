package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/Dawn-kim-official/blueclaw/internal/agent"
)

var planUpdateInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["steps"],
	"properties":{
		"goal":{"type":"string"},
		"steps":{
			"type":"array",
			"items":{
				"type":"object",
				"additionalProperties":false,
				"required":["title","status"],
				"properties":{
					"title":{"type":"string"},
					"status":{"type":"string","enum":["pending","in_progress","done","skipped"]}
				}
			}
		}
	}
}`)

var planUpdateResultSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["steps"],
	"properties":{
		"goal":{"type":"string"},
		"steps":{
			"type":"array",
			"items":{
				"type":"object",
				"additionalProperties":false,
				"required":["title","status"],
				"properties":{
					"title":{"type":"string"},
					"status":{"type":"string","enum":["pending","in_progress","done","skipped"]}
				}
			}
		}
	}
}`)

type planUpdateToolInput struct {
	Goal  string           `json:"goal"`
	Steps []agent.PlanStep `json:"steps"`
}

type planUpdateToolOutput struct {
	Goal  string           `json:"goal,omitempty"`
	Steps []agent.PlanStep `json:"steps"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerPlanUpdateTool(toolRegistry *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[planUpdateToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        agent.PlanUpdateToolName,
			Description: "Record or update your goal and step plan for this task. Send the FULL current list every time (it replaces the previous plan). Keep statuses current as you work; revising the plan is normal and never an error.",
			InputSchema: planUpdateInputSchema,
		},
		Handler: func(_ context.Context, input planUpdateToolInput) (agent.ToolResult, error) {
			goal, steps := agent.NormalizePlan(input.Goal, input.Steps)
			document := json.RawMessage(marshalToolResult(planUpdateToolOutput{Goal: goal, Steps: steps}))
			return agent.ToolSuccessData(string(document), document), nil
		},
		Result: agent.IdentityToolResult,
	})
}
