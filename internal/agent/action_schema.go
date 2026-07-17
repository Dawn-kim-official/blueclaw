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

func (toolSet *ToolSet) ActionSchema(allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool, terminalActionValues ...bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, allowQualityCriteria, blockedToolNames, hasFailureDebt, terminalActionValues...)
	}
	return buildActionSchemaFromToolDefinitions(toolSet.ListToolDefinitions(), allowQualityCriteria, blockedToolNames, hasFailureDebt, terminalActionValues...)
}

func buildActionSchemaFromToolDefinitions(toolDefinitions []ToolDefinition, allowQualityCriteria bool, blockedToolNames map[string]bool, hasFailureDebt bool, terminalActionValues ...bool) string {
	allowFail := true
	allowFinish := true
	if len(terminalActionValues) > 0 {
		allowFail = terminalActionValues[0]
	}
	if len(terminalActionValues) > 1 {
		allowFinish = terminalActionValues[1]
	}
	var variants []any
	if allowFinish {
		variants = append(variants, finishActionSchema(hasFailureDebt))
	}
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
	requiredFields := []string{"action", "message", "goalStatus", "goalSatisfied", "hasRemainingWork", "completionEvidenceIDs", "qualityReview", "executionStateUpdate"}
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
			"hasRemainingWork":      booleanSchema(),
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
			"hasRemainingWork":     booleanSchema(),
			"remainingWork":        stringSchema(),
			"executionStateUpdate": executionStateSchema(),
		},
		"required": []string{"action", "toolName", "toolInput", "goalSatisfied", "hasRemainingWork", "executionStateUpdate"},
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
			"type":                 "object",
			"additionalProperties": false,
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
	return `{"oneOf":[{"type":"object","properties":{"action":{"type":"string","enum":["finish"]},"message":{"type":"string"},"failureResolution":{"type":"string","enum":["none","recovered_with_success","no_tool_fallback"]},"goalStatus":{"type":"string","enum":["satisfied"]},"goalSatisfied":{"type":"boolean"},"hasRemainingWork":{"type":"boolean"},"completionEvidenceIDs":{"type":"array","items":{"type":"string"}},"qualityReview":{"type":"array"},"remainingWork":{"type":"string"}},"required":["action","message","goalStatus","goalSatisfied","hasRemainingWork","completionEvidenceIDs","qualityReview"]},{"type":"object","properties":{"action":{"type":"string","enum":["fail"]},"reason":{"type":"string"},"goalStatus":{"type":"string","enum":["blocked"]},"goalSatisfied":{"type":"boolean"},"remainingWork":{"type":"string"}},"required":["action","reason","goalStatus","goalSatisfied"]}]}`
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
