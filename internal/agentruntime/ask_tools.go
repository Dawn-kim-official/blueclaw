package agentruntime

import (
	"context"
	"encoding/json"
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

type askChoiceOptionInput struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel"`
	Value      string `json:"value"`
}

type askChoiceToolInput struct {
	Question             string                 `json:"question"`
	Options              []askChoiceOptionInput `json:"options"`
	RecommendedOptionKey string                 `json:"recommendedOptionKey"`
	SelectionMode        string                 `json:"selectionMode"`
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
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askChoiceToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.choice",
			Description: "Pause the current task and ask the user to pick from a fixed set of labeled options. Put the question shown to the user in the action message field. Use selectionMode=multiple to allow more than one option.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"label":{"type":"string"},"shortLabel":{"type":"string","description":"버튼에 표시할 1~3단어 단답; label은 본문에 길게 설명 가능"},"value":{"type":"string"}},"required":["key","label"]}},"recommendedOptionKey":{"type":"string"},"selectionMode":{"type":"string","enum":["single","multiple"]}},"required":["options"]}`),
		},
		Handler: toolCatalogBuilder.askChoiceTool,
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
	kind := "input"
	if len(choices) > 0 {
		kind = "input_choice"
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(map[string]any{
		"kind":             kind,
		"question":         question,
		"message":          question,
		"choices":          choices,
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}))
	return agent.ToolSuccess(marshalToolResult(map[string]any{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": question, "kind": kind, "choices": choices})), nil
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
		"kind":             "confirm",
		"question":         question,
		"message":          question,
		"reasonCode":       strings.TrimSpace(input.ReasonCode),
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(confirmationRequest))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "confirmation.requested", marshalToolResult(confirmationRequest))
	return agent.ToolSuccess(marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingApproval), "question": question, "kind": "confirm"})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) askChoiceTool(toolContext context.Context, input askChoiceToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", "ask.choice requires an active task run"), nil
	}
	question := firstNonEmptyString(agent.UserFacingMessageFromContext(toolContext), input.Question)
	if question == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", "ask.choice requires a question in the action message"), nil
	}
	if len(input.Options) == 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", "ask.choice requires at least one option"), nil
	}
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, question)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "ask_choice", errorValue.Error()), nil
	}
	selectionMode := firstNonEmptyString(strings.TrimSpace(input.SelectionMode), "single")
	kind := "choice_single"
	if selectionMode == "multiple" {
		kind = "choice_multiple"
	}
	recommendedOptionKey := strings.TrimSpace(input.RecommendedOptionKey)
	choiceRequest := map[string]any{
		"kind":                 kind,
		"question":             question,
		"message":              question,
		"options":              input.Options,
		"recommendedOptionKey": recommendedOptionKey,
		"selectionMode":        selectionMode,
		"responseLanguage":     agent.ResponseLanguageFromContext(toolContext),
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(choiceRequest))
	return agent.ToolSuccess(marshalToolResult(map[string]any{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": question, "kind": kind, "options": input.Options, "selectionMode": selectionMode})), nil
}
