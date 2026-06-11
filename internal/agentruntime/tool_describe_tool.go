package agentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"blueclaw/internal/agent"
)

type toolDescribeToolInput struct {
	Query    string `json:"query"`
	ToolName string `json:"toolName"`
	Prefix   string `json:"prefix"`
	Limit    int    `json:"limit"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerToolDescribeTool(toolRegistry *agent.ToolSet, availableToolSet *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[toolDescribeToolInput, map[string]any]{
		Definition: agent.ToolDefinition{
			Name:        "tool.describe",
			Description: "Search or inspect available Blueclaw tools by exact name, prefix, or text query before selecting or calling them.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"toolName":{"type":"string"},"prefix":{"type":"string"},"limit":{"type":"number"}}}`),
		},
		Handler: func(_ context.Context, input toolDescribeToolInput) (map[string]any, error) {
			return describeTools(input, availableToolSet), nil
		},
	})
}

func describeTools(input toolDescribeToolInput, toolSet *agent.ToolSet) map[string]any {
	if toolSet == nil {
		return map[string]any{"tools": []map[string]any{}}
	}
	limit := input.Limit
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	items := []map[string]any{}
	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		toolName := strings.TrimSpace(toolDefinition.Name)
		matchReason := toolDescriptionMatchReason(input, toolDefinition)
		if matchReason == "" {
			continue
		}
		availability, _ := toolSet.ToolAvailability(toolName)
		if strings.TrimSpace(availability.Status) == agent.ToolAvailabilityDenied {
			continue
		}
		items = append(items, map[string]any{
			"name":         toolName,
			"description":  firstNonEmptyString(toolDefinition.Description, agentSpecificToolDescription(toolName)),
			"inputSchema":  toolDefinition.InputSchema,
			"availability": availability,
			"matchReason":  matchReason,
		})
		if len(items) >= limit {
			break
		}
	}
	return map[string]any{"tools": items}
}

func toolDescriptionMatches(input toolDescribeToolInput, toolDefinition agent.ToolDefinition) bool {
	return toolDescriptionMatchReason(input, toolDefinition) != ""
}

func toolDescriptionMatchReason(input toolDescribeToolInput, toolDefinition agent.ToolDefinition) string {
	toolName := strings.TrimSpace(toolDefinition.Name)
	if expectedToolName := strings.TrimSpace(input.ToolName); expectedToolName != "" {
		if toolName == expectedToolName {
			return "exact_name"
		}
		return ""
	}
	if prefix := strings.TrimSpace(input.Prefix); prefix != "" && !strings.HasPrefix(toolName, prefix) {
		return ""
	} else if prefix != "" {
		return "prefix"
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	if query == "" {
		return "all"
	}
	searchText := toolDescriptionSearchText(toolDefinition)
	if strings.Contains(searchText, query) {
		return "query"
	}
	if toolDescriptionContainsQueryTokens(searchText, query) {
		return "query_tokens"
	}
	return ""
}

func toolDescriptionSearchText(toolDefinition agent.ToolDefinition) string {
	values := []string{
		toolDefinition.Name,
		toolDefinition.Description,
		agentSpecificToolDescription(toolDefinition.Name),
		toolDefinition.RecoveryCard.Does,
		toolDefinition.RecoveryCard.Produces,
		toolDefinition.RecoveryCard.SideEffect,
		toolDefinition.RecoveryCard.UseWhen,
		toolDefinition.RecoveryCard.AvoidWhen,
	}
	return strings.ToLower(strings.Join(values, " "))
}

func toolDescriptionContainsQueryTokens(searchText string, query string) bool {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(tokens) == 0 {
		return true
	}
	for _, token := range tokens {
		if !strings.Contains(searchText, token) {
			return false
		}
	}
	return true
}

func agentSpecificToolDescription(toolName string) string {
	toolSet := agent.NewToolSet([]string{toolName})
	toolSet.RegisterTool(agent.ToolDefinition{Name: toolName}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess(""), nil
	})
	for _, line := range strings.Split(toolSet.Descriptions(), "\n") {
		if strings.HasPrefix(line, "- "+toolName+": ") {
			return strings.TrimPrefix(line, "- "+toolName+": ")
		}
	}
	return ""
}
