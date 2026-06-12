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

type askConfirmToolInput struct {
	UserFacingMessage string `json:"userFacingMessage"`
	ReasonCode        string `json:"reasonCode"`
	ReasonDetail      string `json:"reasonDetail"`
}

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

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	if !handlerContext.request.IsScheduledRun {
		agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askConfirmToolInput, agent.ToolResult]{
			Definition: agent.ToolDefinition{
				Name:        "ask.confirm",
				Description: "Pause the current task while waiting for explicit user confirmation. Use only before destructive, high-risk, external-send, credential, paid-service, or capability-unlock actions. userFacingMessage is shown directly to the user and must use the same language as the original user request. reasonCode and reasonDetail are internal only.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"userFacingMessage":{"type":"string","description":"User-facing approval question shown directly to the user, written in the same language as the original user request."},"reasonCode":{"type":"string","enum":["external_send","destructive_action","credential_access","paid_action","permission_change","capability_unlock","other_sensitive_action"]},"reasonDetail":{"type":"string","description":"Optional internal diagnostic detail. Never write user-facing prose here."}},"required":["userFacingMessage","reasonCode"]}`),
			},
			Handler: toolCatalogBuilder.askConfirmTool,
			Result:  agent.IdentityToolResult,
		})
		agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askChoiceToolInput, agent.ToolResult]{
			Definition: agent.ToolDefinition{
				Name:        "ask.choice",
				Description: "Pause the current task and ask the user to choose from explicit options. Always include exactly one recommendedOptionKey. Use selectionMode single or multiple.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"label":{"type":"string"},"shortLabel":{"type":"string","description":"버튼에 표시할 1~3단어 단답; label은 본문에 길게 설명 가능"}},"required":["key","label"]}},"recommendedOptionKey":{"type":"string"},"selectionMode":{"type":"string","enum":["single","multiple"]}},"required":["question","options","recommendedOptionKey"]}`),
			},
			Handler: toolCatalogBuilder.askChoiceTool,
			Result:  agent.IdentityToolResult,
		})
		agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askInputToolInput, agent.ToolResult]{
			Definition: agent.ToolDefinition{
				Name:        "ask.input",
				Description: "Pause the current task and ask the user for free-form input needed to continue.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`),
			},
			Handler: toolCatalogBuilder.askInputTool,
			Result:  agent.IdentityToolResult,
		})
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) askConfirmTool(toolContext context.Context, input askConfirmToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_confirm", "ask.confirm requires an active task run"), nil
	}
	userFacingMessage := askConfirmUserFacingMessage(input)
	if userFacingMessage == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_confirm", "ask.confirm requires userFacingMessage"), nil
	}
	reasonCode := normalizeApprovalReasonCode(input.ReasonCode)
	reasonDetail := askConfirmReasonDetail(input)
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingApproval, approvalInternalReason(reasonCode, reasonDetail))
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "approval_request", errorValue.Error()), nil
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "confirmation.requested", marshalToolResult(map[string]string{
		"userFacingMessage": userFacingMessage,
		"message":           userFacingMessage,
		"reasonCode":        reasonCode,
		"reasonDetail":      reasonDetail,
		"responseLanguage":  agent.ResponseLanguageFromContext(toolContext),
	}))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(map[string]any{
		"kind":             "confirm",
		"message":          userFacingMessage,
		"reasonCode":       reasonCode,
		"reasonDetail":     reasonDetail,
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}))
	return agent.ToolSuccess(marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingApproval), "userFacingMessage": userFacingMessage, "message": userFacingMessage, "reasonCode": reasonCode})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) askChoiceTool(toolContext context.Context, input askChoiceToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", "ask.choice requires an active task run"), nil
	}
	choiceRequest, errorValue := normalizeAskChoiceRequest(input, agent.ResponseLanguageFromContext(toolContext))
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
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_input", "ask.input requires question"), nil
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

func askConfirmUserFacingMessage(input askConfirmToolInput) string {
	return strings.TrimSpace(input.UserFacingMessage)
}

func askConfirmReasonDetail(input askConfirmToolInput) string {
	return strings.TrimSpace(input.ReasonDetail)
}

type normalizedAskChoiceRequest struct {
	Kind                 string            `json:"kind"`
	Question             string            `json:"question"`
	Options              []askChoiceOption `json:"options"`
	RecommendedOptionKey string            `json:"recommendedOptionKey"`
	SelectionMode        string            `json:"selectionMode"`
	ResponseLanguage     string            `json:"responseLanguage"`
}

func normalizeAskChoiceRequest(input askChoiceToolInput, responseLanguage string) (normalizedAskChoiceRequest, error) {
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice requires question")
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

func approvalInternalReason(reasonCode string, reasonDetail string) string {
	if strings.TrimSpace(reasonDetail) == "" {
		return reasonCode
	}
	return reasonCode + ": " + strings.TrimSpace(reasonDetail)
}

func normalizeApprovalReasonCode(reasonCode string) string {
	switch strings.TrimSpace(reasonCode) {
	case "external_send", "destructive_action", "credential_access", "paid_action", "permission_change", "capability_unlock", "other_sensitive_action":
		return strings.TrimSpace(reasonCode)
	default:
		return "other_sensitive_action"
	}
}
