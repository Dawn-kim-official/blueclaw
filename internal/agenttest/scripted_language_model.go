package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"blueclaw/internal/llm"
)

type ScriptedLanguageModelOptions struct {
	ActionResponses             []string
	ChatResponsesBySchema       map[string][]string
	StructuredResponsesBySchema map[string][]string
	DefaultResponsesBySchema    map[string]string
	ProviderName                string
	ModelName                   string
}

type ScriptedLanguageModel struct {
	mutex                       sync.Mutex
	actionResponses             []string
	chatResponsesBySchema       map[string][]string
	structuredResponsesBySchema map[string][]string
	defaultResponsesBySchema    map[string]string
	requests                    []llm.StructuredResponseRequest
	providerName                string
	modelName                   string
}

type scriptedChatCompleter struct {
	languageModel *ScriptedLanguageModel
}

func NewScriptedLanguageModel(options ScriptedLanguageModelOptions) *ScriptedLanguageModel {
	return &ScriptedLanguageModel{
		actionResponses:             append([]string{}, options.ActionResponses...),
		chatResponsesBySchema:       copyResponseQueues(options.ChatResponsesBySchema),
		structuredResponsesBySchema: copyResponseQueues(options.StructuredResponsesBySchema),
		defaultResponsesBySchema:    mergeDefaultResponses(options.DefaultResponsesBySchema),
		providerName:                firstNonEmpty(options.ProviderName, "test"),
		modelName:                   firstNonEmpty(options.ModelName, "scripted"),
	}
}

func (languageModel *ScriptedLanguageModel) TextChatCompleter() (llm.ChatCompleter, bool) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if len(languageModel.chatResponsesBySchema) == 0 {
		return nil, false
	}
	return scriptedChatCompleter{languageModel: languageModel}, true
}

func (completer scriptedChatCompleter) GenerateChatCompletion(_ context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	languageModel := completer.languageModel
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	schemaName := strings.TrimSpace(request.SchemaName)
	languageModel.requests = append(languageModel.requests, structuredRequestFromChat(request))
	if schemaName == "blueclaw_agent_turn_action" {
		response, errorValue := languageModel.popActionResponse()
		if errorValue != nil {
			return llm.ChatCompletionResponse{}, errorValue
		}
		return languageModel.actionChatResponse(request, response)
	}
	responses := languageModel.chatResponsesBySchema[schemaName]
	if len(responses) == 0 {
		return llm.ChatCompletionResponse{}, fmt.Errorf("scripted language model has no %s chat response", request.SchemaName)
	}
	languageModel.chatResponsesBySchema[schemaName] = responses[1:]
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    languageModel.providerName,
		ModelName:       languageModel.modelName,
		SelectedBackend: "device",
		Message: llm.ChatCompletionMessage{
			Role:    "assistant",
			Content: responses[0],
		},
	}, nil
}

func (languageModel *ScriptedLanguageModel) actionChatResponse(request llm.ChatCompletionRequest, content string) (llm.ChatCompletionResponse, error) {
	var actionDocument struct {
		Action    string          `json:"action"`
		ToolName  string          `json:"toolName"`
		ToolInput json.RawMessage `json:"toolInput"`
	}
	if errorValue := json.Unmarshal([]byte(content), &actionDocument); errorValue != nil {
		return llm.ChatCompletionResponse{}, errorValue
	}
	toolName := strings.TrimSpace(actionDocument.Action)
	arguments := json.RawMessage(content)
	if toolName == "continue" {
		toolName = strings.TrimSpace(actionDocument.ToolName)
		arguments = actionDocument.ToolInput
	}
	if toolName == "" || len(arguments) == 0 {
		return llm.ChatCompletionResponse{}, errors.New("scripted agent action is incomplete")
	}
	if !chatRequestHasTool(request, toolName) {
		return llm.ChatCompletionResponse{}, fmt.Errorf("scripted agent action tool %q is not exposed; available tools: %s", toolName, strings.Join(chatRequestToolNames(request), ", "))
	}
	arguments = removeActionDiscriminator(arguments)
	return languageModel.toolCallResponse(toolName, arguments), nil
}

func chatRequestHasTool(request llm.ChatCompletionRequest, toolName string) bool {
	for _, availableToolName := range chatRequestToolNames(request) {
		if availableToolName == toolName {
			return true
		}
	}
	return false
}

func chatRequestToolNames(request llm.ChatCompletionRequest) []string {
	toolNames := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		toolNames = append(toolNames, tool.Function.Name)
	}
	return toolNames
}

func (languageModel *ScriptedLanguageModel) toolCallResponse(toolName string, arguments json.RawMessage) llm.ChatCompletionResponse {
	return llm.ChatCompletionResponse{
		FinishReason:    "tool_calls",
		ProviderName:    languageModel.providerName,
		ModelName:       languageModel.modelName,
		SelectedBackend: "device",
		Message: llm.ChatCompletionMessage{
			Role: "assistant",
			ToolCalls: []llm.ChatCompletionToolCall{{
				ID:   "scripted-call",
				Type: "function",
				Function: llm.ChatCompletionToolCallFunction{
					Name:      toolName,
					Arguments: string(arguments),
				},
			}},
		},
	}
}

func removeActionDiscriminator(document json.RawMessage) json.RawMessage {
	var values map[string]json.RawMessage
	if json.Unmarshal(document, &values) != nil {
		return document
	}
	delete(values, "action")
	normalizedDocument, errorValue := json.Marshal(values)
	if errorValue != nil {
		return document
	}
	return normalizedDocument
}

func structuredRequestFromChat(request llm.ChatCompletionRequest) llm.StructuredResponseRequest {
	messages := make([]llm.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, llm.Message{Role: message.Role, Content: message.Content})
	}
	return llm.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name: request.SchemaName,
		},
		GenerationOptions: request.GenerationOptions,
	}
}

func NewActionScriptedLanguageModel(actionResponses ...string) *ScriptedLanguageModel {
	return NewScriptedLanguageModel(ScriptedLanguageModelOptions{ActionResponses: actionResponses})
}

func (languageModel *ScriptedLanguageModel) EnqueueActionResponses(actionResponses ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.actionResponses = append(languageModel.actionResponses, actionResponses...)
}

func (languageModel *ScriptedLanguageModel) SetActionResponses(actionResponses ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.actionResponses = append([]string{}, actionResponses...)
}

func (languageModel *ScriptedLanguageModel) EnqueueStructuredResponses(schemaName string, responses ...string) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	trimmedSchemaName := strings.TrimSpace(schemaName)
	if trimmedSchemaName == "" {
		return
	}
	languageModel.structuredResponsesBySchema[trimmedSchemaName] = append(languageModel.structuredResponsesBySchema[trimmedSchemaName], responses...)
}

func (languageModel *ScriptedLanguageModel) PendingResponseCounts() map[string]int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	pendingCounts := map[string]int{}
	if len(languageModel.actionResponses) > 0 {
		pendingCounts["blueclaw_agent_turn_action"] = len(languageModel.actionResponses)
	}
	for schemaName, responses := range languageModel.structuredResponsesBySchema {
		if len(responses) > 0 {
			pendingCounts[schemaName] += len(responses)
		}
	}
	for schemaName, responses := range languageModel.chatResponsesBySchema {
		if len(responses) > 0 {
			pendingCounts[schemaName] += len(responses)
		}
	}
	return pendingCounts
}

func (languageModel *ScriptedLanguageModel) RequestCount() int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return len(languageModel.requests)
}

func (languageModel *ScriptedLanguageModel) Requests() []llm.StructuredResponseRequest {
	return languageModel.RequestsSince(0)
}

func (languageModel *ScriptedLanguageModel) RequestsSince(startIndex int) []llm.StructuredResponseRequest {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	if startIndex < 0 || startIndex > len(languageModel.requests) {
		startIndex = 0
	}
	return append([]llm.StructuredResponseRequest{}, languageModel.requests[startIndex:]...)
}

func (languageModel *ScriptedLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *ScriptedLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.requests = append(languageModel.requests, request)
	schemaName := strings.TrimSpace(request.StructuredOutputSchema.Name)
	if response, isFound := languageModel.popStructuredResponse(schemaName); isFound {
		return languageModel.structuredResponse(response), nil
	}
	if schemaName == "blueclaw_agent_turn_action" {
		response, errorValue := languageModel.popActionResponse()
		if errorValue != nil {
			return llm.StructuredResponse{}, errorValue
		}
		return languageModel.structuredResponse(response), nil
	}
	response := strings.TrimSpace(languageModel.defaultResponsesBySchema[schemaName])
	if response == "" {
		if schemaName == "blueclaw_contract_skill_arbitration" {
			response, errorValue := defaultContractSkillArbitrationResponse(request)
			if errorValue != nil {
				return llm.StructuredResponse{}, errorValue
			}
			return languageModel.structuredResponse(response), nil
		}
		if schemaName == "blueclaw_operation_contract" {
			response, errorValue := defaultOperationContractResponse(request)
			if errorValue != nil {
				return llm.StructuredResponse{}, errorValue
			}
			return languageModel.structuredResponse(response), nil
		}
		if schemaName == "blueclaw_approval_question" {
			return languageModel.structuredResponse(defaultApprovalQuestionResponse(request)), nil
		}
		return llm.StructuredResponse{}, fmt.Errorf("scripted language model has no %s response", schemaName)
	}
	if schemaName == "blueclaw_skill_search_queries" && response == `{"queries":[]}` {
		return languageModel.structuredResponse(defaultSkillSearchQueriesResponse(request)), nil
	}
	return languageModel.structuredResponse(response), nil
}

func defaultContractSkillArbitrationResponse(request llm.StructuredResponseRequest) (string, error) {
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal([]byte(request.StructuredOutputSchema.Document), &schema); errorValue != nil {
		return "", fmt.Errorf("decode contract skill arbitration schema: %w", errorValue)
	}
	skillNames := schema.Properties["selectedSkillNames"].Items.Enum
	selectedSkillNames := append([]string{}, skillNames...)
	rejectedSkillNames := []string{}
	expectedEvidence := []string{}
	document, errorValue := json.Marshal(map[string]any{
		"selectedSkillNames":    selectedSkillNames,
		"rejectedSkillNames":    rejectedSkillNames,
		"requiredNextToolNames": []string{},
		"expectedEvidence":      expectedEvidence,
		"unmetPreconditions":    []string{},
		"reason":                "scripted test default",
	})
	if errorValue != nil {
		return "", fmt.Errorf("encode contract skill arbitration fixture: %w", errorValue)
	}
	return string(document), nil
}

func defaultOperationContractResponse(request llm.StructuredResponseRequest) (string, error) {
	var schema map[string]any
	if errorValue := json.Unmarshal([]byte(request.StructuredOutputSchema.Document), &schema); errorValue != nil {
		return "", fmt.Errorf("decode operation contract schema: %w", errorValue)
	}
	properties, _ := schema["properties"].(map[string]any)
	operationsSchema, _ := properties["operations"].(map[string]any)
	items, _ := operationsSchema["items"].(map[string]any)
	itemSchemas := []any{items}
	if oneOf, isUnion := items["oneOf"].([]any); isUnion {
		itemSchemas = oneOf
	}
	operations := make([]map[string]any, 0, len(itemSchemas))
	for _, value := range itemSchemas {
		itemSchema, _ := value.(map[string]any)
		itemProperties, _ := itemSchema["properties"].(map[string]any)
		toolNameSchema, _ := itemProperties["toolName"].(map[string]any)
		toolNames, _ := toolNameSchema["enum"].([]any)
		toolName, _ := toolNames[0].(string)
		operations = append(operations, map[string]any{"toolName": toolName, "requiredValues": map[string]any{}})
	}
	document, errorValue := json.Marshal(map[string]any{"operations": operations})
	if errorValue != nil {
		return "", fmt.Errorf("encode operation contract fixture: %w", errorValue)
	}
	return string(document), nil
}

func defaultSkillSearchQueriesResponse(request llm.StructuredResponseRequest) string {
	prompt := ""
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			prompt = strings.TrimSpace(request.Messages[index].Content)
			break
		}
	}
	if prompt == "" {
		return `{"queries":[]}`
	}
	document, errorValue := json.Marshal(map[string]any{"queries": []map[string]string{{"description": prompt}}})
	if errorValue != nil {
		return `{"queries":[]}`
	}
	return string(document)
}

func (languageModel *ScriptedLanguageModel) popStructuredResponse(schemaName string) (string, bool) {
	responses := languageModel.structuredResponsesBySchema[schemaName]
	if len(responses) == 0 {
		return "", false
	}
	response := responses[0]
	languageModel.structuredResponsesBySchema[schemaName] = responses[1:]
	return response, true
}

func (languageModel *ScriptedLanguageModel) popActionResponse() (string, error) {
	for index, response := range languageModel.actionResponses {
		if !isActionResponse(response) {
			continue
		}
		languageModel.actionResponses = append(languageModel.actionResponses[:index], languageModel.actionResponses[index+1:]...)
		return response, nil
	}
	return "", fmt.Errorf("scripted language model action response queue is empty")
}

func (languageModel *ScriptedLanguageModel) structuredResponse(content string) llm.StructuredResponse {
	return llm.StructuredResponse{ProviderName: languageModel.providerName, ModelName: languageModel.modelName, Content: content}
}

func isActionResponse(content string) bool {
	document := map[string]any{}
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		return false
	}
	return stringMapValue(document, "action") != ""
}

func copyResponseQueues(responseQueues map[string][]string) map[string][]string {
	copiedResponseQueues := map[string][]string{}
	for schemaName, responses := range responseQueues {
		copiedResponseQueues[strings.TrimSpace(schemaName)] = append([]string{}, responses...)
	}
	return copiedResponseQueues
}

func mergeDefaultResponses(defaultResponses map[string]string) map[string]string {
	mergedResponses := map[string]string{
		"blueclaw_skill_search_queries": `{"queries":[]}`,
		"blueclaw_execution_plan":       `{"originalInstruction":"scripted test request","summary":"scripted test request","targets":[],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"scripted test request"}`,
		"blueclaw_confirmation_message": `{"reply":"확인했습니다. 승인하면 진행하겠습니다."}`,
	}
	for schemaName, response := range defaultResponses {
		mergedResponses[strings.TrimSpace(schemaName)] = response
	}
	return mergedResponses
}

type approvalQuestionContextDocument struct {
	OriginalRequest string            `json:"originalRequest"`
	ModelDraft      string            `json:"modelDraft"`
	ActionDetails   map[string]string `json:"actionDetails"`
}

func defaultApprovalQuestionResponse(request llm.StructuredResponseRequest) string {
	contextDocument := approvalQuestionContextFromRequest(request)
	details := contextDocument.ActionDetails
	target := strings.TrimSpace(details["target"])
	content := strings.TrimSpace(firstNonEmpty(details["message"], details["content"], details["title"], details["reason"]))
	question := defaultApprovalQuestionFromContext(contextDocument, target, content)
	document, errorValue := json.Marshal(map[string]string{"question": question})
	if errorValue != nil {
		return `{"question":"승인할까요?"}`
	}
	return string(document)
}

func defaultApprovalQuestionFromContext(contextDocument approvalQuestionContextDocument, target string, content string) string {
	if target != "" && content != "" {
		return target + "에게 다음 내용을 보낼까요?\n\n" + content
	}
	if draftQuestion := approvalQuestionFromDraft(contextDocument.ModelDraft); draftQuestion != "" {
		return draftQuestion
	}
	if requestQuestion := approvalQuestionFromDraft(contextDocument.OriginalRequest); requestQuestion != "" {
		return requestQuestion
	}
	if content != "" {
		return content + "\n\n진행할까요?"
	}
	return "승인할까요?"
}

func approvalQuestionContextFromRequest(request llm.StructuredResponseRequest) approvalQuestionContextDocument {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		var contextDocument approvalQuestionContextDocument
		if json.Unmarshal([]byte(request.Messages[index].Content), &contextDocument) == nil {
			return contextDocument
		}
	}
	return approvalQuestionContextDocument{}
}

func approvalQuestionFromDraft(value string) string {
	trimmedValue := strings.Trim(strings.TrimSpace(value), ".。")
	if trimmedValue == "" {
		return ""
	}
	if strings.HasSuffix(trimmedValue, "?") || strings.HasSuffix(trimmedValue, "？") {
		return trimmedValue
	}
	if strings.HasSuffix(trimmedValue, "합니다") {
		return strings.TrimSuffix(trimmedValue, "합니다") + "할까요?"
	}
	if strings.HasSuffix(trimmedValue, "해줘") {
		return strings.TrimSuffix(trimmedValue, "해줘") + "할까요?"
	}
	return trimmedValue + "?"
}

func stringMapValue(document map[string]any, key string) string {
	value, isString := document[key].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
