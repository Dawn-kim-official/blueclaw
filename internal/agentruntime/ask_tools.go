package agentruntime

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type askInputToolInput struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

type askConfirmToolInput struct {
	Question   string `json:"question"`
	ReasonCode string `json:"reasonCode"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askInputToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.input",
			Description: "Pause the current task and ask the user for input needed to continue. Put the question shown to the user in the action message field. Use choices=[] for free-form input, or provide choices to let the user pick one of them or type a different answer.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"choices":{"type":"array","items":{"type":"string"},"description":"Empty for free-form input; non-empty to show selectable choices while allowing a custom answer."}}}`),
		},
		Handler: toolCatalogBuilder.askInputTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askConfirmToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.confirm",
			Description: "Pause the current task and ask the user to approve or reject a sensitive action before proceeding.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"reasonCode":{"type":"string"}}}`),
		},
		Handler: toolCatalogBuilder.askConfirmTool,
		Result:  agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) askInputTool(toolContext context.Context, input askInputToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_input", "ask.input requires an active task run"), nil
	}
	question := firstNonEmptyString(agent.UserFacingMessageFromContext(toolContext), input.Question)
	if question == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_input", "ask.input requires a question in the action message"), nil
	}
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, question)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "ask_input", errorValue.Error()), nil
	}
	choices := trimNonEmptyStrings(input.Choices)
	options := make([]map[string]string, 0, len(choices))
	for index, choice := range choices {
		options = append(options, map[string]string{
			"key":   strconv.Itoa(index + 1),
			"label": choice,
			"value": choice,
		})
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(map[string]any{
		"kind":             "ask_input",
		"question":         question,
		"message":          question,
		"options":          options,
		"selectionMode":    selectionModeForOptions(options),
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}))
	return agent.ToolSuccess(marshalToolResult(map[string]any{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": question, "kind": "ask_input", "options": options})), nil
}

func selectionModeForOptions(options []map[string]string) string {
	if len(options) == 0 {
		return ""
	}
	return "single"
}

func (toolCatalogBuilder *ToolCatalogBuilder) askConfirmTool(toolContext context.Context, input askConfirmToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_confirm", "ask.confirm requires an active task run"), nil
	}
	question := firstNonEmptyString(agent.UserFacingMessageFromContext(toolContext), input.Question)
	if question == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_confirm", "ask.confirm requires a question in the action message"), nil
	}
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingApproval, question)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "ask_confirm", errorValue.Error()), nil
	}
	confirmationRequest := map[string]string{
		"kind":             "ask_confirm",
		"question":         question,
		"message":          question,
		"reasonCode":       strings.TrimSpace(input.ReasonCode),
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(confirmationRequest))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "confirmation.requested", marshalToolResult(confirmationRequest))
	return agent.ToolSuccess(marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingApproval), "question": question, "kind": "ask_confirm"})), nil
}
