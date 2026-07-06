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
		if schemaName == "blueclaw_result_verifier" {
			return languageModel.structuredResponse(defaultResultVerificationResponse(request)), nil
		}
		if schemaName == "blueclaw_contract_verifier" {
			return languageModel.structuredResponse(defaultContractVerificationResponse()), nil
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
		"blueclaw_skill_search_queries":        `{"queries":[]}`,
		"blueclaw_turn_router":                 `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
		"blueclaw_execution_plan":              `{"originalInstruction":"scripted test request","summary":"scripted test request","targets":[],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"scripted test request"}`,
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

func defaultContractVerificationResponse() string {
	return `{"satisfied":true,"reason":"scripted test default","missingDescription":"","suggestedNextTools":[]}`
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
