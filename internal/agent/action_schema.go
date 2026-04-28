package agent

import (
	"encoding/json"
	"strings"
)

func (agentTurnRunner *AgentTurnRunner) buildActionSchema(toolRegistry *ToolRegistry) string {
	var variants []any
	variants = append(variants,
		finalReplyActionSchema(),
		fetchHistoryActionSchema(),
		searchMemoryActionSchema(),
		failActionSchema(),
	)
	if toolRegistry != nil {
		for _, toolDefinition := range toolRegistry.ListToolDefinitions() {
			variants = append(variants, callToolActionSchema(toolDefinition))
		}
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
			"action":     enumStringSchema("final_reply"),
			"finalReply": stringSchema(),
			"reply":      stringSchema(),
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
}

func fetchHistoryActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":    enumStringSchema("fetch_history"),
			"toolInput": objectSchema(),
			"reason":    stringSchema(),
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
}

func searchMemoryActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": enumStringSchema("search_memory"),
			"query":  stringSchema(),
			"reason": stringSchema(),
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
}

func failActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": enumStringSchema("fail"),
			"reason": stringSchema(),
		},
		"required":             []string{"action", "reason"},
		"additionalProperties": false,
	}
}

func callToolActionSchema(toolDefinition ToolDefinition) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":    enumStringSchema("call_tool"),
			"toolName":  enumStringSchema(toolDefinition.Name),
			"toolInput": toolInputSchema(toolDefinition),
			"reason":    stringSchema(),
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
	default:
		return nil
	}
}

func enumStringSchema(value string) map[string]any {
	return map[string]any{"type": "string", "enum": []string{value}}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object"}
}

func fallbackActionSchema() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["final_reply","call_tool","fetch_history","search_memory","fail"]},"finalReply":{"type":"string"},"toolName":{"type":"string"},"toolInput":{"type":"object"},"query":{"type":"string"},"reason":{"type":"string"},"reply":{"type":"string"}},"required":["action"],"additionalProperties":false}`
}
