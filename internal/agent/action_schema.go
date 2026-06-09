package agent

import (
	"encoding/json"
	"sort"
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
	return buildActionSchemaFromToolDefinitionsWithPolicy(toolDefinitions, blockedToolNames, hasFailureDebt, actionSchemaPolicy(allowQualityCriteria, allowFail, true))
}

func buildActionSchemaFromToolDefinitionsWithPolicy(toolDefinitions []ToolDefinition, blockedToolNames map[string]bool, hasFailureDebt bool, policy ActionPolicy) string {
	var variants []any
	if shouldExposeFinishActionForPolicy(policy) {
		variants = append(variants, finishActionSchema(hasFailureDebt))
	}
	if policy.CanFail {
		variants = append(variants, failActionSchema(hasFailureDebt))
	}
	if policy.CanSetQualityCriteria {
		variants = append(variants, setQualityCriteriaActionSchema())
	}
	if policy.CanSelectTools {
		variants = append(variants, selectToolsActionSchema())
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

func actionSchemaPolicy(allowQualityCriteria bool, allowFail bool, allowFinish bool) ActionPolicy {
	finishExposure := FinishExposureWhenReady
	if allowFinish {
		finishExposure = FinishExposureAlways
	}
	return ActionPolicy{
		CanSelectTools:        true,
		CanSetQualityCriteria: allowQualityCriteria,
		CanFail:               allowFail,
		FinishExposure:        finishExposure,
	}
}

func shouldExposeFinishActionForPolicy(policy ActionPolicy) bool {
	return policy.FinishExposure != FinishExposureWhenReady
}

func selectToolsActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":               enumStringSchema("select_tools"),
			"toolNames":            stringArraySchema(0),
			"skillNames":           stringArraySchema(0),
			"reason":               stringSchema(),
			"goalStatus":           enumValuesStringSchema([]string{"in_progress"}),
			"goalSatisfied":        booleanSchema(),
			"remainingWork":        stringSchema(),
			"executionStateUpdate": executionStateSchema(),
		},
		"required": []string{"action", "toolNames", "skillNames", "executionStateUpdate"},
	}
}

func finishActionSchema(hasFailureDebt bool) map[string]any {
	failureResolutionValues := []string{"none", "recovered_with_success", "no_tool_fallback"}
	requiredFields := []string{"action", "message", "goalStatus", "goalSatisfied", "completionEvidence", "qualityReview", "executionStateUpdate"}
	if hasFailureDebt {
		failureResolutionValues = []string{"recovered_with_success", "no_tool_fallback"}
		requiredFields = append(requiredFields, "failureResolution")
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":               enumStringSchema("finish"),
			"message":              stringSchema(),
			"replyParts":           agentPartArraySchema(),
			"completionSummary":    stringSchema(),
			"failureResolution":    enumValuesStringSchema(failureResolutionValues),
			"goalStatus":           enumValuesStringSchema([]string{"satisfied"}),
			"goalSatisfied":        booleanSchema(),
			"completionEvidence":   completionEvidenceSchema(),
			"qualityReview":        qualityReviewSchema(),
			"remainingWork":        stringSchema(),
			"executionStateUpdate": executionStateSchema(),
		},
		"required": requiredFields,
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
	return map[string]any{
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
			"nextStepPlan":         nextStepPlanSchema(),
		},
		"required": []string{"action", "toolName", "toolInput", "executionStateUpdate", "nextStepPlan"},
	}
}

func nextStepPlanSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"objective":           stringSchema(),
			"expectedTools":       stringArraySchema(0),
			"expectedNextResults": stringArraySchema(0),
			"doneCriteria":        stringArraySchema(0),
			"risk":                stringSchema(),
			"workingSetReason":    stringSchema(),
		},
	}
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
		requiredFieldNames := stringSet(stringSliceFromAnySchema(document["required"]))
		for fieldName, fieldValue := range document {
			if fieldName == "required" {
				continue
			}
			if fieldName == "type" && fieldValue == "integer" {
				clone[fieldName] = "number"
				continue
			}
			if fieldName == "properties" {
				clone[fieldName] = portableSchemaProperties(fieldValue, requiredFieldNames)
				continue
			}
			clone[fieldName] = portableNestedSchema(fieldValue)
		}
		if clone["type"] == "object" {
			properties := mapFromAnySchema(clone["properties"])
			clone["properties"] = properties
			clone["required"] = sortedSchemaPropertyNames(properties)
			if len(properties) == 0 {
				clone["required"] = []string{}
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

func portableSchemaProperties(value any, requiredFieldNames map[string]bool) map[string]any {
	properties := mapFromAnySchema(value)
	clone := map[string]any{}
	for fieldName, propertySchema := range properties {
		normalizedPropertySchema := portableNestedSchema(propertySchema)
		if !requiredFieldNames[fieldName] {
			normalizedPropertySchema = nullableSchemaValue(normalizedPropertySchema)
		}
		clone[fieldName] = normalizedPropertySchema
	}
	return clone
}

func nullableSchemaValue(value any) any {
	document, isDocument := value.(map[string]any)
	if !isDocument {
		return value
	}
	clone := map[string]any{}
	for fieldName, fieldValue := range document {
		clone[fieldName] = fieldValue
	}
	clone["type"] = nullableSchemaType(clone["type"])
	if enumValues, ok := clone["enum"].([]any); ok && !schemaEnumContainsNull(enumValues) {
		clone["enum"] = append(enumValues, nil)
	}
	return clone
}

func nullableSchemaType(value any) any {
	values, isValues := value.([]any)
	if isValues {
		for _, item := range values {
			if item == nil || item == "null" {
				return values
			}
		}
		return append(values, "null")
	}
	if strings.TrimSpace(stringFromAnySchema(value)) == "" {
		return value
	}
	return []any{value, "null"}
}

func schemaEnumContainsNull(values []any) bool {
	for _, value := range values {
		if value == nil {
			return true
		}
	}
	return false
}

func sortedSchemaPropertyNames(properties map[string]any) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func stringSliceFromAnySchema(value any) []string {
	stringValues, isStringValues := value.([]string)
	if isStringValues {
		return append([]string{}, stringValues...)
	}
	values, isValues := value.([]any)
	if !isValues {
		return nil
	}
	result := []string{}
	for _, item := range values {
		stringValue := strings.TrimSpace(stringFromAnySchema(item))
		if stringValue != "" {
			result = append(result, stringValue)
		}
	}
	return result
}

func mapFromAnySchema(value any) map[string]any {
	document, isDocument := value.(map[string]any)
	if isDocument {
		return document
	}
	return map[string]any{}
}

func stringFromAnySchema(value any) string {
	stringValue, _ := value.(string)
	return stringValue
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
	case "flow.task.add":
		return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"targetPersonHint":{"type":"string"},"weekCode":{"type":"string"}},"required":["prompt"]}`)
	case "flow.task.list":
		return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"targetPersonHint":{"type":"string"},"weekCode":{"type":"string"},"status":{"type":"string"},"limit":{"type":"number"}}}`)
	case "flow.task.update":
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
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"observationID":   stringSchema(),
				"toolName":        stringSchema(),
				"attachmentIndex": map[string]any{"type": "number"},
			},
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
	return `{"oneOf":[{"type":"object","properties":{"action":{"type":"string","enum":["finish"]},"message":{"type":"string"},"failureResolution":{"type":"string","enum":["none","recovered_with_success","no_tool_fallback"]},"goalStatus":{"type":"string","enum":["satisfied"]},"goalSatisfied":{"type":"boolean"},"completionEvidence":{"type":"array"},"qualityReview":{"type":"array"},"remainingWork":{"type":"string"}},"required":["action","message","goalStatus","goalSatisfied","completionEvidence","qualityReview"]},{"type":"object","properties":{"action":{"type":"string","enum":["fail"]},"reason":{"type":"string"},"goalStatus":{"type":"string","enum":["blocked"]},"goalSatisfied":{"type":"boolean"},"remainingWork":{"type":"string"}},"required":["action","reason","goalStatus","goalSatisfied"]}]}`
}

func finalizerActionSchema() string {
	document, errorValue := json.Marshal(map[string]any{"oneOf": []any{finishActionSchema(false), failActionSchema(false)}})
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
