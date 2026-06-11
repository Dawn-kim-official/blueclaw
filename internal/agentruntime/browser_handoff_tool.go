package agentruntime

import (
	"context"
	"encoding/json"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type browserHandoffOpenURLToolInput struct {
	URL string `json:"url"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerBrowserHandoffTool(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[browserHandoffOpenURLToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "browser_handoff.openURL",
			Description: "Ask the Companion bridge to open a URL on the user's computer without running shell commands.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		},
		Handler: func(toolContext context.Context, input browserHandoffOpenURLToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.openBrowserHandoffTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) openBrowserHandoffTool(toolContext context.Context, input browserHandoffOpenURLToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.capabilityClient.HTTPClient == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "browser_handoff", "companion bridge capability client is unavailable"), nil
	}
	inputDocument, errorValue := json.Marshal(map[string]string{"url": input.URL})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "browser_handoff", errorValue.Error()), nil
	}
	requestDocument := capabilityToolRequest(toolContext, "browser.handoff", handlerContext.request, inputDocument)
	requestDocument["executionMode"] = "companion"
	requestDocument["requiresUserPresence"] = true
	requestDocument["privacyClass"] = "user_browser"
	var response struct {
		Content string          `json:"content"`
		IsError bool            `json:"isError"`
		Status  string          `json:"status"`
		Result  json.RawMessage `json:"result"`
	}
	errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/browser.handoff/invoke", requestDocument, &response)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	content := firstNonEmptyString(response.Content, string(response.Result))
	isError := response.IsError || response.Status == "error" || response.Status == "denied"
	if response.Status == "waiting_for_user" {
		if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
			_, _ = toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, content)
		}
	}
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, browserHandoffEventName(isError), marshalToolResult(map[string]string{"url": input.URL, "content": content}))
	}
	result := agent.ToolResult{
		Output:      agent.ToolOutput{Content: content, Data: response.Result},
		Attachments: capabilityAttachments(response.Result),
	}
	if isError {
		result.Failure = &agent.ToolFailure{
			Kind:            capabilityFailureKind("", "browser_handoff"),
			Code:            agent.FailureCodes.OperationFailed.String(),
			Stage:           "browser_handoff",
			UserSafeSummary: content,
		}
	}
	return result, nil
}

func browserHandoffEventName(isError bool) string {
	if isError {
		return "browser_handoff.failed"
	}
	return "browser_handoff.opened"
}
