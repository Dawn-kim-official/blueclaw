package agent

import (
	"encoding/json"
	"strings"
)

func (agentTurnRunner *AgentTurnRunner) buildActionSchema(toolRegistry *ToolSet, allowQualityCriteria bool, blockedToolNames map[string]bool) string {
	if toolRegistry != nil {
		return toolRegistry.ActionSchema(allowQualityCriteria, blockedToolNames)
	}
	return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames)
}

func (toolSet *ToolSet) ActionSchema(allowQualityCriteria bool, blockedToolNames map[string]bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames)
	}
	return buildActionSchemaFromToolDefinitions(toolSet.ListToolDefinitions(), allowQualityCriteria, blockedToolNames)
}

func buildActionSchemaFromToolDefinitions(toolDefinitions []ToolDefinition, allowQualityCriteria bool, blockedToolNames map[string]bool) string {
	var variants []any
	variants = append(variants,
		finalReplyActionSchema(),
		failActionSchema(),
	)
	if allowQualityCriteria {
		variants = append(variants, setQualityCriteriaActionSchema())
	}
	for _, toolDefinition := range toolDefinitions {
		if blockedToolNames[strings.TrimSpace(toolDefinition.Name)] {
			continue
		}
		variants = append(variants, callToolActionSchema(toolDefinition))
	}

	document, errorValue := json.Marshal(map[string]any{"oneOf": variants})
	if errorValue != nil {
		return fallbackActionSchema()
	}
	return string(document)
}

func finalReplyActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":             enumStringSchema("final_reply"),
			"finalReply":         stringSchema(),
			"reply":              stringSchema(),
			"failureResolution":  enumValuesStringSchema([]string{"none", "recovered_with_success", "no_tool_fallback"}),
			"goalStatus":         enumValuesStringSchema([]string{"satisfied"}),
			"goalSatisfied":      booleanSchema(),
			"completionEvidence": completionEvidenceSchema(),
			"qualityReview":      qualityReviewSchema(),
			"remainingWork":      stringSchema(),
		},
		"required":             []string{"action", "goalStatus", "goalSatisfied", "completionEvidence", "qualityReview"},
		"additionalProperties": false,
	}
}

func setQualityCriteriaActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":          enumStringSchema("set_quality_criteria"),
			"qualityCriteria": qualityCriteriaSchema(),
			"reason":          stringSchema(),
			"goalStatus":      enumValuesStringSchema([]string{"in_progress"}),
			"goalSatisfied":   booleanSchema(),
			"remainingWork":   stringSchema(),
		},
		"required":             []string{"action", "qualityCriteria"},
		"additionalProperties": false,
	}
}

func failActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":        enumStringSchema("fail"),
			"reason":        stringSchema(),
			"goalStatus":    enumValuesStringSchema([]string{"blocked"}),
			"goalSatisfied": booleanSchema(),
			"remainingWork": stringSchema(),
		},
		"required":             []string{"action", "reason"},
		"additionalProperties": false,
	}
}

func callToolActionSchema(toolDefinition ToolDefinition) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":        enumStringSchema("call_tool"),
			"toolName":      enumStringSchema(toolDefinition.Name),
			"toolInput":     toolInputSchema(toolDefinition),
			"reason":        stringSchema(),
			"goalStatus":    enumValuesStringSchema([]string{"in_progress", "blocked"}),
			"goalSatisfied": booleanSchema(),
			"remainingWork": stringSchema(),
		},
		"required":             []string{"action", "toolName", "toolInput"},
		"additionalProperties": false,
	}
}

func toolInputSchema(toolDefinition ToolDefinition) any {
	if len(toolDefinition.InputSchema) > 0 {
		var schema any
		if json.Unmarshal(toolDefinition.InputSchema, &schema) == nil {
			return schema
		}
	}
	if schema := specificToolInputSchema(toolDefinition.Name); len(schema) > 0 {
		var document any
		if json.Unmarshal(schema, &document) == nil {
			return document
		}
	}
	return objectSchema()
}

func specificToolInputSchema(toolName string) json.RawMessage {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`)
	case "browser.snapshot":
		return json.RawMessage(`{"type":"object","properties":{"interactive":{"type":"boolean"}},"additionalProperties":false}`)
	case "browser.screenshot":
		return json.RawMessage(`{"type":"object","properties":{"ttlSeconds":{"type":"number"}},"additionalProperties":false}`)
	case "browser.click":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"}},"additionalProperties":false}`)
	case "browser.fill":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
	case "browser.select":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
	case "browser.press":
		return json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`)
	case "browser.wait":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"milliseconds":{"type":"number"}},"additionalProperties":false}`)
	case "conversation.history":
		return json.RawMessage(`{"type":"object","properties":{"historyCursor":{"type":"string"},"limit":{"type":"number"},"direction":{"type":"string"}},"additionalProperties":false}`)
	case "memory.search":
		return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"additionalProperties":false}`)
	case "flow.task.add":
		return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"targetPersonHint":{"type":"string"},"weekCode":{"type":"string"}},"required":["prompt"],"additionalProperties":false}`)
	default:
		return nil
	}
}

func enumStringSchema(value string) map[string]any {
	return map[string]any{"type": "string", "enum": []string{value}}
}

func enumValuesStringSchema(values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func booleanSchema() map[string]any {
	return map[string]any{"type": "boolean"}
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object"}
}

func completionEvidenceSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"observationID":   stringSchema(),
				"toolName":        stringSchema(),
				"attachmentIndex": map[string]any{"type": "number"},
			},
			"required":             []string{"observationID", "toolName"},
			"additionalProperties": false,
		},
	}
}

func qualityCriteriaSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          stringSchema(),
				"description": stringSchema(),
				"required":    booleanSchema(),
			},
			"required":             []string{"id", "description"},
			"additionalProperties": false,
		},
	}
}

func qualityReviewSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":       stringSchema(),
				"passed":   booleanSchema(),
				"evidence": completionEvidenceSchema(),
				"notes":    stringSchema(),
			},
			"required":             []string{"id", "passed", "evidence"},
			"additionalProperties": false,
		},
	}
}

func fallbackActionSchema() string {
	return `{"oneOf":[{"type":"object","properties":{"action":{"type":"string","enum":["final_reply"]},"finalReply":{"type":"string"},"reply":{"type":"string"},"failureResolution":{"type":"string","enum":["none","recovered_with_success","no_tool_fallback"]},"goalStatus":{"type":"string","enum":["satisfied"]},"goalSatisfied":{"type":"boolean"},"completionEvidence":{"type":"array"},"qualityReview":{"type":"array"},"remainingWork":{"type":"string"}},"required":["action","goalStatus","goalSatisfied","completionEvidence","qualityReview"],"additionalProperties":false},{"type":"object","properties":{"action":{"type":"string","enum":["fail"]},"reason":{"type":"string"},"goalStatus":{"type":"string","enum":["blocked"]},"goalSatisfied":{"type":"boolean"},"remainingWork":{"type":"string"}},"required":["action","reason"],"additionalProperties":false}]}`
}

func finalizerActionSchema() string {
	document, errorValue := json.Marshal(map[string]any{"oneOf": []any{finalReplyActionSchema(), failActionSchema()}})
	if errorValue != nil {
		return fallbackActionSchema()
	}
	return string(document)
}
