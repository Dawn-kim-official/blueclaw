package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type askChoiceToolInput struct {
	Question             string                `json:"question"`
	Options              []askChoiceToolOption `json:"options"`
	RecommendedOptionKey string                `json:"recommendedOptionKey"`
	SelectionMode        string                `json:"selectionMode"`
}

type askChoiceToolOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel,omitempty"`
	Value      string `json:"value,omitempty"`
}

type askChoiceOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel,omitempty"`
	Value      string `json:"value,omitempty"`
}

type askInputToolInput struct {
	Question string `json:"question"`
}

type askConfirmToolInput struct {
	Question   string `json:"question"`
	ReasonCode string `json:"reasonCode"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askChoiceToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.choice",
			Description: "Pause the current task and ask the user to choose from explicit options. Put the question shown to the user in the action message field. Always include exactly one recommendedOptionKey. Use selectionMode single or multiple.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"options":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"label":{"type":"string"},"shortLabel":{"type":"string","description":"버튼에 표시할 1~3단어 단답; label은 본문에 길게 설명 가능"}},"required":["key","label"]}},"recommendedOptionKey":{"type":"string"},"selectionMode":{"type":"string","enum":["single","multiple"]}},"required":["options","recommendedOptionKey"]}`),
		},
		Handler: toolCatalogBuilder.askChoiceTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askInputToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.input",
			Description: "Pause the current task and ask the user for free-form input needed to continue. Put the question shown to the user in the action message field.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}}}`),
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

func (toolCatalogBuilder *ToolCatalogBuilder) askChoiceTool(toolContext context.Context, input askChoiceToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", "ask.choice requires an active task run"), nil
	}
	choiceRequest, errorValue := normalizeAskChoiceRequest(input, strings.TrimSpace(agent.UserFacingMessageFromContext(toolContext)), agent.ResponseLanguageFromContext(toolContext))
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", errorValue.Error()), nil
	}
	_, errorValue = toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, choiceRequest.Question)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "ask_choice", errorValue.Error()), nil
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(choiceRequest))
	return agent.ToolSuccess(marshalToolResult(map[string]any{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": choiceRequest.Question, "kind": choiceRequest.Kind})), nil
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
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(map[string]string{
		"kind":             "input",
		"question":         question,
		"message":          question,
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}))
	return agent.ToolSuccess(marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": question, "kind": "input"})), nil
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

type normalizedAskChoiceRequest struct {
	Kind                 string            `json:"kind"`
	Question             string            `json:"question"`
	Options              []askChoiceOption `json:"options"`
	RecommendedOptionKey string            `json:"recommendedOptionKey"`
	SelectionMode        string            `json:"selectionMode"`
	ResponseLanguage     string            `json:"responseLanguage"`
}

func normalizeAskChoiceRequest(input askChoiceToolInput, question string, responseLanguage string) (normalizedAskChoiceRequest, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice requires a question in the action message")
	}
	selectionMode := strings.TrimSpace(input.SelectionMode)
	if selectionMode == "" {
		selectionMode = "single"
	}
	if selectionMode != "single" && selectionMode != "multiple" {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice selectionMode must be single or multiple")
	}
	options := normalizedAskChoiceOptions(input.Options)
	if len(options) < 2 {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice requires at least two options")
	}
	recommendedOptionKey := strings.TrimSpace(input.RecommendedOptionKey)
	if !askChoiceOptionKeyExists(options, recommendedOptionKey) {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice recommendedOptionKey must match an option key")
	}
	kind := "choice_single"
	if selectionMode == "multiple" {
		kind = "choice_multiple"
	}
	return normalizedAskChoiceRequest{
		Kind:                 kind,
		Question:             question,
		Options:              options,
		RecommendedOptionKey: recommendedOptionKey,
		SelectionMode:        selectionMode,
		ResponseLanguage:     responseLanguage,
	}, nil
}

func normalizedAskChoiceOptions(options []askChoiceToolOption) []askChoiceOption {
	normalizedOptions := []askChoiceOption{}
	for index, option := range options {
		key := strings.TrimSpace(option.Key)
		if key == "" {
			key = askChoiceKey(index)
		}
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = label
		}
		normalizedOptions = append(normalizedOptions, askChoiceOption{
			Key:        key,
			Label:      label,
			ShortLabel: strings.TrimSpace(option.ShortLabel),
			Value:      value,
		})
	}
	return normalizedOptions
}

func (option *askChoiceToolOption) UnmarshalJSON(document []byte) error {
	var label string
	if errorValue := json.Unmarshal(document, &label); errorValue == nil {
		option.Label = strings.TrimSpace(label)
		option.Value = strings.TrimSpace(label)
		return nil
	}
	var structuredOption struct {
		Key        string `json:"key"`
		Label      string `json:"label"`
		ShortLabel string `json:"shortLabel"`
		Value      string `json:"value"`
	}
	if errorValue := json.Unmarshal(document, &structuredOption); errorValue != nil {
		return errorValue
	}
	option.Key = strings.TrimSpace(structuredOption.Key)
	option.Label = strings.TrimSpace(structuredOption.Label)
	option.ShortLabel = strings.TrimSpace(structuredOption.ShortLabel)
	option.Value = strings.TrimSpace(structuredOption.Value)
	return nil
}

func askChoiceKey(index int) string {
	return fmt.Sprintf("%d", index+1)
}

func askChoiceOptionKeyExists(options []askChoiceOption, key string) bool {
	for _, option := range options {
		if option.Key == key {
			return true
		}
	}
	return false
}
