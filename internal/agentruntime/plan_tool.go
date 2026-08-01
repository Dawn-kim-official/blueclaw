package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
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
	Goal  string                `json:"goal"`
	Steps []bluecollar.PlanStep `json:"steps"`
}

type planUpdateToolOutput struct {
	Goal  string                `json:"goal,omitempty"`
	Steps []bluecollar.PlanStep `json:"steps"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerPlanUpdateTool(toolRegistry *bluecollar.ToolSet) {
	bluecollar.RegisterToolFunction(toolRegistry, bluecollar.ToolFunction[planUpdateToolInput, bluecollar.ToolResult]{
		Definition: bluecollar.ToolDefinition{
			Name:        bluecollar.PlanUpdateToolName,
			Description: "Record or update your goal and step plan for this task. Send the FULL current list every time (it replaces the previous plan). Keep statuses current as you work; revising the plan is normal and never an error.",
			InputSchema: planUpdateInputSchema,
		},
		Handler: func(_ context.Context, input planUpdateToolInput) (bluecollar.ToolResult, error) {
			goal, steps := bluecollar.NormalizePlan(input.Goal, input.Steps)
			document := json.RawMessage(marshalToolResult(planUpdateToolOutput{Goal: goal, Steps: steps}))
			return bluecollar.ToolSuccessData(string(document), document), nil
		},
		Result: bluecollar.IdentityToolResult,
	})
}
