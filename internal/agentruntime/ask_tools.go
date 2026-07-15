package agentruntime

import (
	"context"
	"encoding/json"
	"strconv"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type askInputToolInput struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskInputTool(toolRegistry *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askInputToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.input",
			Description: "Pause the current task only when the typed outcome contract or a structured tool failure says user input is required. Put the question shown to the user in the action message field. Use choices=[] for free-form input, or provide choices to let the user pick one of them or type a different answer.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"choices":{"type":"array","items":{"type":"string"},"description":"Empty for free-form input; non-empty to show selectable choices while allowing a custom answer."}}}`),
		},
		Handler: toolCatalogBuilder.askInputTool,
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
