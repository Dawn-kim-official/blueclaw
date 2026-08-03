package intake

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

func newTestToolSet(allowedToolNames []string) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet(allowedToolNames)
	toolSet.AllowTestReplacement()
	for _, toolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		toolSet.RegisterBoundTool(toolcontract.BoundTool{
			Definition:   testToolDescriptor(trimmedToolName),
			Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
			Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
			},
		})
	}
	return toolSet
}

func testToolDescriptor(toolName string) toolcontract.ToolDefinition {
	return toolcontract.ToolDefinition{
		ID:                "test:" + toolName,
		Name:              toolName,
		Visibility:        toolcontract.ToolVisibilityModel,
		InputSchema:       json.RawMessage(`{"type":"object","properties":{}}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract:    testToolResultContract(),
		SideEffectClass:   testToolSideEffectClass(toolName),
	}
}

func testToolResultContract() *toolcontract.ToolResultContract {
	return &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func registerTestTool(toolSet *toolcontract.ToolSet, definition toolcontract.ToolDefinition, handler toolcontract.ToolHandler) error {
	if definition.Visibility == "" {
		definition.Visibility = toolcontract.ToolVisibilityModel
	}
	if definition.Visibility == toolcontract.ToolVisibilityModel && definition.ResultContract == nil {
		definition.ResultContract = testToolResultContract()
	}
	return toolSet.RegisterTool(definition, func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		result, errorValue := handler(toolContext, invocation)
		if errorValue == nil && !result.Failed() && len(result.Output.Data) == 0 {
			result.Output.Data = json.RawMessage(`{}`)
		}
		return result, errorValue
	})
}

func testToolSuccess(content string) toolcontract.ToolResult {
	return toolcontract.ToolSuccessData(content, json.RawMessage(`{}`))
}

func newTestCapabilityToolSet(operationNames []string) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet(operationNames)
	toolSet.AllowTestReplacement()
	for _, operationName := range operationNames {
		trimmedOperationName := strings.TrimSpace(operationName)
		if trimmedOperationName == "" {
			continue
		}
		registerTestTool(toolSet, testToolDescriptor(trimmedOperationName), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func expectedResultIncludesType(outcomeContract agentcontract.OutcomeContract, resultType string) bool {
	for _, expectedResult := range outcomeContract.ExpectedResults {
		if strings.TrimSpace(expectedResult.Type) == resultType {
			return true
		}
	}
	return false
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func attachmentSuffixesForRequestedOutputFormats(formats []string) []string {
	suffixes := []string{}
	for _, format := range agentcontract.NormalizeRequestedOutputFormats(formats) {
		switch format {
		case "html":
			suffixes = append(suffixes, ".html")
		case "pptx":
			suffixes = append(suffixes, ".pptx")
		case "pdf":
			suffixes = append(suffixes, ".pdf")
		case "txt":
			suffixes = append(suffixes, ".txt")
		case "docx":
			suffixes = append(suffixes, ".docx")
		case "xlsx":
			suffixes = append(suffixes, ".xlsx")
		case "csv":
			suffixes = append(suffixes, ".csv")
		case "json":
			suffixes = append(suffixes, ".json")
		}
	}
	return suffixes
}

func expectedResultsContain(results []agentcontract.ExpectedResult, resultType string, descriptionFragment string) bool {
	for _, result := range results {
		if result.Type == resultType && strings.Contains(result.Description, descriptionFragment) {
			return true
		}
	}
	return false
}

func testToolSideEffectClass(toolName string) string {
	for _, suffix := range []string{"_list", "_read", "_search", "_status", "_history", "_preview", "_snapshot"} {
		if strings.HasSuffix(toolName, suffix) {
			return toolcontract.ToolSideEffectRead
		}
	}
	for _, suffix := range []string{"_calculate", "_compare", "_classify"} {
		if strings.HasSuffix(toolName, suffix) {
			return toolcontract.ToolSideEffectComputation
		}
	}
	return toolcontract.ToolSideEffectStateChange
}
