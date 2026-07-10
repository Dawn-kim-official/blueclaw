package agent

import (
	"encoding/json"
	"strings"
)

func (agentTurnRunner *AgentTurnRunner) buildActionSchema(toolRegistry *ToolSet, allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool) string {
	if toolRegistry != nil {
		return toolRegistry.ActionSchema(allowQualityCriteria, blockedToolNames, hasFailureDebt)
	}
	return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames, hasFailureDebt)
}

func (toolSet *ToolSet) ActionSchema(allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool, allowFailValues ...bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames, hasFailureDebt, allowFailValues...)
	}
	return buildActionSchemaFromToolDefinitions(toolSet.ListToolDefinitions(), allowQualityCriteria, blockedToolNames, hasFailureDebt, allowFailValues...)
}

func buildActionSchemaFromToolDefinitions(toolDefinitions []ToolDefinition, allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool, allowFailValues ...bool) string {
	allowFail := true
	if len(allowFailValues) > 0 {
		allowFail = allowFailValues[0]
	}
	var variants []any
	variants = append(variants, finishActionSchema(hasFailureDebt))
	if allowFail {
		variants = append(variants, failActionSchema(hasFailureDebt))
	}
	if allowQualityCriteria {
		variants = append(variants, setQualityCriteriaActionSchema())
	}
	for _, toolDefinition := range toolDefinitions {
		if blockedToolNames[strings.TrimSpace(toolDefinition.Name)] {
			continue
		}
		variants = append(variants, continueActionSchema(toolDefinition))
	}

	document, errorValue := json.Marshal(map[string]any{"oneOf": variants})
	if errorValue != nil {
		return fallbackActionSchema()
	}
	return string(document)
}

func finishActionSchema(hasFailureDebt bool) map[string]any {
	failureResolutionValues := []string{"none", "recovered_with_success", "no_tool_fallback"}
	requiredFields := []string{"action", "message", "goalStatus", "goalSatisfied", "completionEvidenceIDs", "qualityReview", "executionStateUpdate"}
	if hasFailureDebt {
		failureResolutionValues = []string{"recovered_with_success", "no_tool_fallback"}
		requiredFields = append(requiredFields, "failureResolution")
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":                enumStringSchema("finish"),
			"message":               stringSchema(),
			"replyParts":            finishReplyPartArraySchema(),
			"completionSummary":     stringSchema(),
			"failureResolution":     enumValuesStringSchema(failureResolutionValues),
			"goalStatus":            enumValuesStringSchema([]string{"satisfied"}),
			"goalSatisfied":         booleanSchema(),
			"completionEvidenceIDs": stringArraySchema(0),
			"qualityReview":         qualityReviewSchema(),
			"remainingWork":         stringSchema(),
			"executionStateUpdate":  executionStateSchema(),
		},
		"required": requiredFields,
	}
}

func finishReplyPartArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": enumValuesStringSchema([]string{"text"}),
				"text": stringSchema(),
			},
		},
	}
}

func agentPartArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": enumValuesStringSchema([]string{"text", "image", "file"}),
				"text": stringSchema(),
				"image": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"mimeType": stringSchema(),
						"path":     stringSchema(),
						"filename": stringSchema(),
					},
				},
				"file": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        stringSchema(),
						"filename":    stringSchema(),
						"contentType": stringSchema(),
					},
				},
			},
		},
	}
}

func setQualityCriteriaActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":               enumStringSchema("set_quality_criteria"),
			"qualityCriteria":      qualityCriteriaSchema(),
			"reason":               stringSchema(),
			"goalStatus":           enumValuesStringSchema([]string{"in_progress"}),
			"goalSatisfied":        booleanSchema(),
			"remainingWork":        stringSchema(),
			"executionStateUpdate": executionStateSchema(),
		},
		"required": []string{"action", "qualityCriteria", "executionStateUpdate"},
	}
}

func failActionSchema(hasFailureDebt bool) map[string]any {
	properties := map[string]any{
		"action":               enumStringSchema("fail"),
		"reason":               stringSchema(),
		"goalStatus":           enumValuesStringSchema([]string{"blocked"}),
		"goalSatisfied":        booleanSchema(),
		"remainingWork":        stringSchema(),
		"executionStateUpdate": executionStateSchema(),
	}
	requiredFields := []string{"action", "reason", "goalStatus", "goalSatisfied", "executionStateUpdate"}
	if hasFailureDebt {
		properties["failureResolution"] = enumValuesStringSchema([]string{"failure_report"})
		properties["usedFailureFacts"] = failureReportFactsSchema()
		requiredFields = append(requiredFields, "failureResolution", "usedFailureFacts")
	}
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   requiredFields,
	}
}

func continueActionSchema(toolDefinition ToolDefinition) map[string]any {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":               enumStringSchema("continue"),
			"toolName":             enumStringSchema(toolDefinition.Name),
			"toolInput":            toolInputSchema(toolDefinition),
			"message":              stringSchema(),
			"reason":               stringSchema(),
			"goalStatus":           enumValuesStringSchema([]string{"in_progress"}),
			"goalSatisfied":        booleanSchema(),
			"remainingWork":        stringSchema(),
			"executionStateUpdate": executionStateSchema(),
		},
		"required": []string{"action", "toolName", "toolInput", "executionStateUpdate"},
	}
	if description := strings.TrimSpace(toolDefinition.Description); description != "" {
		schema["description"] = description
	}
	return schema
}

func toolInputSchema(toolDefinition ToolDefinition) any {
	if len(toolDefinition.InputSchema) > 0 {
		var schema any
		if json.Unmarshal(toolDefinition.InputSchema, &schema) == nil {
			return portableNestedSchema(schema)
		}
	}
	if schema := specificToolInputSchema(toolDefinition.Name); len(schema) > 0 {
		var document any
		if json.Unmarshal(schema, &document) == nil {
			return portableNestedSchema(document)
		}
	}
	return objectSchema()
}

func portableNestedSchema(value any) any {
	document, isDocument := value.(map[string]any)
	if isDocument {
		clone := map[string]any{}
		for fieldName, fieldValue := range document {
			if fieldName == "type" && fieldValue == "integer" {
				clone[fieldName] = "number"
				continue
			}
			clone[fieldName] = portableNestedSchema(fieldValue)
		}
		if clone["type"] == "object" {
			if _, isFound := clone["properties"]; !isFound {
				clone["properties"] = map[string]any{}
			}
		}
		return clone
	}
	values, isValues := value.([]any)
	if isValues {
		clone := make([]any, 0, len(values))
		for _, item := range values {
			clone = append(clone, portableNestedSchema(item))
		}
		return clone
	}
	return value
}

func specificToolInputSchema(toolName string) json.RawMessage {
	switch strings.TrimSpace(toolName) {
	case "browser.open":
		return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)
	case "browser.snapshot":
		return json.RawMessage(`{"type":"object","properties":{"interactive":{"type":"boolean"}}}`)
	case "browser.screenshot":
		return json.RawMessage(`{"type":"object","properties":{"ttlSeconds":{"type":"number"}}}`)
	case "browser.click":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"}}}`)
	case "browser.fill":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"text":{"type":"string"}},"required":["text"]}`)
	case "browser.select":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"value":{"type":"string"}},"required":["value"]}`)
	case "browser.press":
		return json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`)
	case "browser.wait":
		return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"ref":{"type":"string"},"selector":{"type":"string"},"milliseconds":{"type":"number"}}}`)
	case "conversation.history":
		return json.RawMessage(`{"type":"object","properties":{"historyCursor":{"type":"string"},"limit":{"type":"number"},"direction":{"type":"string"}}}`)
	case "task.add":
		return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"targetPersonHint":{"type":"string"},"weekCode":{"type":"string"}},"required":["prompt"]}`)
	case "task.list":
		return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"targetPersonHint":{"type":"string","description":"Leave EMPTY to list everyone's tasks. Set to one person's name — including the requester's own name for their own tasks — to list only that person."},"weekFrom":{"type":"number","description":"Relative week offset for the start of the range. 0 = this week (default), -1 = last week, -2 = two weeks ago. Omit for this week."},"weekTo":{"type":"number","description":"Relative week offset for the end of the range, default 0 (this week). For all time use a wide range such as weekFrom -520."},"status":{"type":"string"},"limit":{"type":"number"}}}`)
	case "task.update":
		return json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"},"query":{"type":"string"},"targetPersonHint":{"type":"string"},"weekCode":{"type":"string"},"content":{"type":"string"},"goal":{"type":"string"},"status":{"type":"string"},"size":{"type":"string"},"category":{"type":"string"},"type":{"type":"string"},"startDate":{"type":"string"},"endDate":{"type":"string"},"flag":{"type":"number"},"requestReason":{"type":"string"},"decisionReason":{"type":"string"}}}`)
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
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func completionEvidenceSchema() map[string]any {
	return stringArraySchema(0)
}

func qualityCriteriaSchema() map[string]any {
	return stringArraySchema(0)
}

func qualityReviewSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          stringSchema(),
				"passed":      booleanSchema(),
				"evidenceIDs": completionEvidenceSchema(),
				"notes":       stringSchema(),
			},
		},
	}
}

func failureReportFactsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"attempts": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"toolName":     stringSchema(),
						"inputSummary": stringSchema(),
						"errorCode":    stringSchema(),
						"failureStage": stringSchema(),
						"message":      stringSchema(),
					},
				},
			},
			"budgetState": stringSchema(),
		},
	}
}

func fallbackActionSchema() string {
	return `{"oneOf":[{"type":"object","properties":{"action":{"type":"string","enum":["finish"]},"message":{"type":"string"},"failureResolution":{"type":"string","enum":["none","recovered_with_success","no_tool_fallback"]},"goalStatus":{"type":"string","enum":["satisfied"]},"goalSatisfied":{"type":"boolean"},"completionEvidenceIDs":{"type":"array","items":{"type":"string"}},"qualityReview":{"type":"array"},"remainingWork":{"type":"string"}},"required":["action","message","goalStatus","goalSatisfied","completionEvidenceIDs","qualityReview"]},{"type":"object","properties":{"action":{"type":"string","enum":["fail"]},"reason":{"type":"string"},"goalStatus":{"type":"string","enum":["blocked"]},"goalSatisfied":{"type":"boolean"},"remainingWork":{"type":"string"}},"required":["action","reason","goalStatus","goalSatisfied"]}]}`
}

func finalizerActionSchema() string {
	document, errorValue := json.Marshal(map[string]any{"oneOf": []any{finishActionSchema(false), failActionSchema(false)}})
	if errorValue != nil {
		return fallbackActionSchema()
	}
	return string(document)
}

func terminalNoToolsActionSchema() string {
	document, errorValue := json.Marshal(map[string]any{"oneOf": []any{finishActionSchema(true), failActionSchema(true)}})
	if errorValue != nil {
		return fallbackActionSchema()
	}
	return string(document)
}

func recoveryDecisionSchema() string {
	document, errorValue := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"whatFailed":      stringSchema(),
			"whatWasKnown":    stringSchema(),
			"nextAction":      stringSchema(),
			"userReplyIntent": stringSchema(),
		},
		"required": []string{"whatFailed", "whatWasKnown", "nextAction", "userReplyIntent"},
	})
	if errorValue != nil {
		return `{"type":"object"}`
	}
	return string(document)
}
