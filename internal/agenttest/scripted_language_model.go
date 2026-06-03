package agenttest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"blueclaw/internal/llm"
)

type ScriptedLanguageModelOptions struct {
	ActionResponses             []string
	StructuredResponsesBySchema map[string][]string
	DefaultResponsesBySchema    map[string]string
	ProviderName                string
	ModelName                   string
}

type ScriptedLanguageModel struct {
	mutex                       sync.Mutex
	actionResponses             []string
	structuredResponsesBySchema map[string][]string
	defaultResponsesBySchema    map[string]string
	requests                    []llm.StructuredResponseRequest
	providerName                string
	modelName                   string
}

func NewScriptedLanguageModel(options ScriptedLanguageModelOptions) *ScriptedLanguageModel {
	return &ScriptedLanguageModel{
		actionResponses:             append([]string{}, options.ActionResponses...),
		structuredResponsesBySchema: copyResponseQueues(options.StructuredResponsesBySchema),
		defaultResponsesBySchema:    mergeDefaultResponses(options.DefaultResponsesBySchema),
		providerName:                firstNonEmpty(options.ProviderName, "test"),
		modelName:                   firstNonEmpty(options.ModelName, "scripted"),
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
	if response, isFound := languageModel.popStructuredResponse(schemaName, request.StructuredOutputSchema.Document); isFound {
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
		if schemaName == "blueclaw_result_verifier" {
			return languageModel.structuredResponse(defaultResultVerificationResponse(request)), nil
		}
		return llm.StructuredResponse{}, fmt.Errorf("scripted language model has no %s response", schemaName)
	}
	return languageModel.structuredResponse(response), nil
}

func (languageModel *ScriptedLanguageModel) popStructuredResponse(schemaName string, schemaDocument string) (string, bool) {
	responses := languageModel.structuredResponsesBySchema[schemaName]
	if len(responses) == 0 {
		return languageModel.popLegacyTurnRouterResponse(schemaName, schemaDocument)
	}
	response := responses[0]
	languageModel.structuredResponsesBySchema[schemaName] = responses[1:]
	return response, true
}

func (languageModel *ScriptedLanguageModel) popLegacyTurnRouterResponse(schemaName string, schemaDocument string) (string, bool) {
	if schemaName != "blueclaw_turn_router" {
		return "", false
	}
	if strings.Contains(schemaDocument, `"approval"`) {
		if response, isFound := languageModel.popConfirmationReplyResponse(); isFound {
			return response, true
		}
	}
	if strings.Contains(schemaDocument, `"choices"`) {
		if response, isFound := languageModel.popChoiceReplyResponse(); isFound {
			return response, true
		}
	}
	responses := languageModel.structuredResponsesBySchema["blueclaw_task_intake_effort"]
	if len(responses) == 0 {
		return "", false
	}
	response := responses[0]
	languageModel.structuredResponsesBySchema["blueclaw_task_intake_effort"] = responses[1:]
	return legacyIntakeResponseAsTurnRouterResponse(response), true
}

func (languageModel *ScriptedLanguageModel) popConfirmationReplyResponse() (string, bool) {
	responses := languageModel.structuredResponsesBySchema["blueclaw_confirmation_reply_decision"]
	if len(responses) == 0 {
		return "", false
	}
	response := responses[0]
	languageModel.structuredResponsesBySchema["blueclaw_confirmation_reply_decision"] = responses[1:]
	return legacyConfirmationResponseAsTurnRouterResponse(response), true
}

func (languageModel *ScriptedLanguageModel) popChoiceReplyResponse() (string, bool) {
	responses := languageModel.structuredResponsesBySchema["blueclaw_choice_reply_decision"]
	if len(responses) == 0 {
		return "", false
	}
	response := responses[0]
	languageModel.structuredResponsesBySchema["blueclaw_choice_reply_decision"] = responses[1:]
	return legacyChoiceResponseAsTurnRouterResponse(response), true
}

func legacyIntakeResponseAsTurnRouterResponse(response string) string {
	document := map[string]any{}
	if errorValue := json.Unmarshal([]byte(response), &document); errorValue != nil {
		return response
	}
	if stringMapValue(document, "route") == "" {
		document["route"] = "start_task"
	}
	encodedResponse, errorValue := json.Marshal(document)
	if errorValue != nil {
		return response
	}
	return string(encodedResponse)
}

func legacyConfirmationResponseAsTurnRouterResponse(response string) string {
	document := map[string]any{}
	if errorValue := json.Unmarshal([]byte(response), &document); errorValue != nil {
		return response
	}
	decision := stringMapValue(document, "decision")
	approval := "unclear"
	route := "consume"
	switch decision {
	case "approved":
		approval = "approve"
		route = "continue_task"
	case "rejected":
		approval = "reject"
	case "question":
		approval = "unclear"
		route = "answer_question"
	case "modify":
		approval = "unclear"
		route = "revise_task"
	case "unrelated":
		approval = "unclear"
		route = "start_task"
	}
	return turnRouterCompatibilityResponse(route, "bounded_task", "maintenance_task", "standard", document, map[string]any{"approval": approval})
}

func legacyChoiceResponseAsTurnRouterResponse(response string) string {
	document := map[string]any{}
	if errorValue := json.Unmarshal([]byte(response), &document); errorValue != nil {
		return response
	}
	choices := []string{}
	if choice := stringMapValue(document, "choice"); choice != "" {
		choices = append(choices, choice)
	}
	if rawChoices, isFound := document["choices"].([]any); isFound {
		for _, rawChoice := range rawChoices {
			choice, isString := rawChoice.(string)
			if isString && strings.TrimSpace(choice) != "" {
				choices = append(choices, strings.TrimSpace(choice))
			}
		}
	}
	route := "continue_task"
	if stringMapValue(document, "status") != "resolved" {
		route = "clarify"
		choices = nil
	}
	return turnRouterCompatibilityResponse(route, "bounded_task", "maintenance_task", "standard", document, map[string]any{"choices": choices})
}

func turnRouterCompatibilityResponse(route string, classification string, taskShape string, effortLevel string, source map[string]any, additions map[string]any) string {
	document := map[string]any{
		"route":                  route,
		"classification":         classification,
		"taskShape":              taskShape,
		"effortLevel":            effortLevel,
		"requestedOutputFormats": nil,
		"responseLanguage":       "ko",
		"reason":                 stringMapValue(source, "reason"),
		"userFacingReply":        "",
	}
	for key, value := range additions {
		document[key] = value
	}
	encodedResponse, errorValue := json.Marshal(document)
	if errorValue != nil {
		return `{"route":"clarify","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"compatibility fallback","userFacingReply":""}`
	}
	return string(encodedResponse)
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
		"blueclaw_skill_search_queries":        `{"queries":[]}`,
		"blueclaw_turn_router":                 `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
		"blueclaw_task_intake_effort":          `{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
		"blueclaw_execution_plan":              `{"originalInstruction":"scripted test request","summary":"scripted test request","targets":[],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"scripted test request"}`,
		"blueclaw_tool_selection":              `{"selectedToolIDs":[],"reason":"scripted test default"}`,
		"blueclaw_confirmation_message":        `{"reply":"확인했습니다. 승인하면 진행하겠습니다."}`,
		"blueclaw_confirmation_reply_decision": `{"decision":"approved","reason":"scripted test default approval"}`,
	}
	for schemaName, response := range defaultResponses {
		mergedResponses[strings.TrimSpace(schemaName)] = response
	}
	return mergedResponses
}

func defaultResultVerificationResponse(request llm.StructuredResponseRequest) string {
	results := []map[string]any{}
	for _, expectedResultID := range expectedResultIDsFromRequest(request) {
		results = append(results, map[string]any{
			"id":                  expectedResultID,
			"status":              "satisfied",
			"reason":              "scripted test default",
			"citedObservationIDs": []string{},
			"missingDescription":  "",
			"suggestedNextTools":  []string{},
		})
	}
	document, errorValue := json.Marshal(map[string]any{
		"overallStatus": "satisfied",
		"summary":       "scripted test default",
		"results":       results,
	})
	if errorValue != nil {
		return `{"overallStatus":"satisfied","summary":"scripted test default","results":[]}`
	}
	return string(document)
}

func expectedResultIDsFromRequest(request llm.StructuredResponseRequest) []string {
	expectedResultDocument := expectedResultDocumentFromRequest(request)
	if expectedResultDocument == "" {
		return nil
	}
	var expectedResults []struct {
		ID string `json:"id"`
	}
	if errorValue := json.Unmarshal([]byte(expectedResultDocument), &expectedResults); errorValue != nil {
		return nil
	}
	ids := []string{}
	for _, expectedResult := range expectedResults {
		id := strings.TrimSpace(expectedResult.ID)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func expectedResultDocumentFromRequest(request llm.StructuredResponseRequest) string {
	for _, message := range request.Messages {
		content := strings.TrimSpace(message.Content)
		if strings.HasPrefix(content, "Expected results:\n") {
			return strings.TrimSpace(strings.TrimPrefix(content, "Expected results:\n"))
		}
	}
	return ""
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
