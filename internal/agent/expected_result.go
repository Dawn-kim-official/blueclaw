package agent

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"blueclaw/internal/llm"
)

type ObservedResult struct {
	Type          string `json:"type,omitempty"`
	Description   string `json:"description,omitempty"`
	ObservationID string `json:"observationID,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
	URL           string `json:"url,omitempty"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
}

type ResultVerification struct {
	Results       []ResultVerificationItem `json:"results"`
	OverallStatus string                   `json:"overallStatus"`
	Summary       string                   `json:"summary"`
}

type ResultVerificationItem struct {
	ID                  string   `json:"id"`
	Status              string   `json:"status"`
	Reason              string   `json:"reason"`
	CitedObservationIDs []string `json:"citedObservationIDs"`
	MissingDescription  string   `json:"missingDescription,omitempty"`
	SuggestedNextTools  []string `json:"suggestedNextTools,omitempty"`
}

type ContractSatisfactionVerification struct {
	Satisfied          bool     `json:"satisfied"`
	Reason             string   `json:"reason"`
	MissingDescription string   `json:"missingDescription"`
	SuggestedNextTools []string `json:"suggestedNextTools"`
}

var observedURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

func buildObservedResults(observations []turnObservation, attachments []FileAttachment, finishMessage string) []ObservedResult {
	results := []ObservedResult{}
	for _, observation := range observations {
		results = append(results, observedResultsFromObservation(observation)...)
	}
	for _, attachment := range attachments {
		results = append(results, observedResultFromAttachment("", "", attachment))
	}
	if strings.TrimSpace(finishMessage) != "" {
		results = append(results, ObservedResult{
			Type:        ExpectedResultTypeMessage,
			Description: "Final message draft: " + compactFinishDraftText(finishMessage),
		})
	}
	return deduplicateObservedResults(results)
}

func observedResultsFromObservation(observation turnObservation) []ObservedResult {
	if observation.Failed() {
		return nil
	}
	if results := observedResultsFromTerminalCapabilityObservation(observation); len(results) > 0 {
		return results
	}
	return observedResultsFromSuccessfulObservation(observation)
}

func observedResultsFromTerminalCapabilityObservation(observation turnObservation) []ObservedResult {
	toolName, resultDocument, isFound := terminalCapabilityResponse(observation)
	if !isFound {
		return nil
	}
	syntheticObservation := observation
	syntheticObservation.Tool = toolName
	if content, errorValue := json.Marshal(resultDocument); errorValue == nil {
		syntheticObservation.Output.Content = string(content)
	}
	return observedResultsFromSuccessfulObservation(syntheticObservation)
}

func observedResultsFromSuccessfulObservation(observation turnObservation) []ObservedResult {
	results := []ObservedResult{}
	content := observation.ContentText()
	if strings.TrimSpace(content) != "" {
		results = append(results, ObservedResult{
			Type:          ExpectedResultTypeMessage,
			Description:   observedResultDescription(observation, content),
			ObservationID: observation.ObservationID,
			ToolName:      observation.Tool,
		})
		if urlValue := firstObservedURL(content); urlValue != "" && observationCanDeliverLink(observation, content) {
			results = append(results, ObservedResult{
				Type:          ExpectedResultTypeLink,
				Description:   observedResultDescription(observation, "URL: "+urlValue),
				ObservationID: observation.ObservationID,
				ToolName:      observation.Tool,
				URL:           urlValue,
			})
		}
	}
	for _, attachment := range observation.Attachments {
		results = append(results, observedResultFromAttachment(observation.ObservationID, observation.Tool, attachment))
	}
	return results
}

func observationCanDeliverLink(observation turnObservation, content string) bool {
	switch strings.TrimSpace(observation.Tool) {
	case "site.create":
		return false
	case "site.publish", "site.status":
		return siteObservationHasPublishedStatus(content)
	default:
		return true
	}
}

func siteObservationHasPublishedStatus(content string) bool {
	var document map[string]any
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		return false
	}
	status, _ := document["status"].(string)
	return strings.TrimSpace(status) == "published"
}

func observedResultFromAttachment(observationID string, toolName string, attachment FileAttachment) ObservedResult {
	filename := strings.TrimSpace(attachment.Filename)
	description := "File available"
	if filename != "" {
		description = "File available: " + filename
	}
	return ObservedResult{
		Type:          ExpectedResultTypeFile,
		Description:   description,
		ObservationID: strings.TrimSpace(observationID),
		ToolName:      strings.TrimSpace(toolName),
		Filename:      filename,
		ContentType:   strings.TrimSpace(attachment.ContentType),
	}
}

func observedResultDescription(observation turnObservation, content string) string {
	toolName := strings.TrimSpace(observation.Tool)
	if toolName == "" {
		return compactObservedResultText(content)
	}
	return toolName + " result: " + compactObservedResultText(content)
}

func compactObservedResultText(value string) string {
	text := compactWhitespace(value)
	if len([]rune(text)) <= 500 {
		return text
	}
	return string([]rune(text)[:500])
}

func compactFinishDraftText(value string) string {
	text := compactWhitespace(value)
	if len([]rune(text)) <= 4000 {
		return text
	}
	return string([]rune(text)[:4000])
}

func firstObservedURL(value string) string {
	match := observedURLPattern.FindString(value)
	return strings.TrimRight(match, ".,)")
}

func deduplicateObservedResults(results []ObservedResult) []ObservedResult {
	deduplicatedResults := []ObservedResult{}
	seenResults := map[string]bool{}
	for _, result := range results {
		result.Type = normalizeExpectedResultType(result.Type)
		result.Description = strings.TrimSpace(result.Description)
		key := result.Type + "\x00" + result.Description + "\x00" + result.ObservationID + "\x00" + result.URL + "\x00" + result.Filename
		if result.Description == "" || seenResults[key] {
			continue
		}
		seenResults[key] = true
		deduplicatedResults = append(deduplicatedResults, result)
	}
	return deduplicatedResults
}

func verifyExpectedResults(ctx context.Context, languageModel llm.LanguageModelProvider, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, actionDocument turnActionDocument) (ResultVerification, error) {
	expectedResults := normalizeExpectedResults(request.OutcomeContract.ExpectedResults)
	if len(expectedResults) == 0 {
		return ResultVerification{OverallStatus: "satisfied"}, nil
	}
	if languageModel == nil {
		return ResultVerification{}, errors.New("result verifier language model is not configured")
	}
	observedResults := buildObservedResults(observations, attachments, finishActionMessage(actionDocument))
	response, errorValue := languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: resultVerifierMessages(request, expectedResults, observedResults),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_result_verifier",
			Document:           resultVerifierSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return ResultVerification{}, errorValue
	}
	var verification ResultVerification
	if errorValue := json.Unmarshal([]byte(response.Content), &verification); errorValue != nil {
		return ResultVerification{}, errorValue
	}
	observedResults = deduplicateObservedResults(observedResults)
	return enforceObservedResultRequirements(expectedResults, observedResults, finishActionMessage(actionDocument), normalizeResultVerification(expectedResults, verification)), nil
}

func verifyContractSatisfaction(ctx context.Context, languageModel llm.LanguageModelProvider, request AgentTurnRequest, observations []turnObservation, attachments []FileAttachment, actionDocument turnActionDocument) (ContractSatisfactionVerification, error) {
	if languageModel == nil {
		return ContractSatisfactionVerification{}, errors.New("contract verifier language model is not configured")
	}
	observedResults := buildObservedResults(observations, attachments, finishActionMessage(actionDocument))
	response, errorValue := languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: contractVerifierMessages(request, observedResults, finishActionMessage(actionDocument)),
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_contract_verifier",
			Document:           contractVerifierSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return ContractSatisfactionVerification{}, errorValue
	}
	var verification ContractSatisfactionVerification
	if errorValue := json.Unmarshal([]byte(response.Content), &verification); errorValue != nil {
		return ContractSatisfactionVerification{}, errorValue
	}
	return normalizeContractSatisfactionVerification(request, verification), nil
}

func contractVerifierMessages(request AgentTurnRequest, observedResults []ObservedResult, finishMessage string) []llm.Message {
	contractDocument, _ := json.Marshal(request.OutcomeContract)
	observedDocument, _ := json.Marshal(deduplicateObservedResults(observedResults))
	return []llm.Message{
		{
			Role:    "system",
			Content: "You verify whether a Blueclaw task is actually complete. Return satisfied=false when the final reply claims a file, attachment, URL, message send, or other deliverable that is not backed by observed evidence. Do not count a final reply promise, a proposed reply part, or a plain path string as delivered evidence. A file is delivered only when observed results include a file/attachment produced by a successful tool result.",
		},
		{
			Role:    "system",
			Content: "Outcome contract:\n" + string(contractDocument),
		},
		{
			Role:    "system",
			Content: "Observed evidence:\n" + string(observedDocument),
		},
		{
			Role:    "system",
			Content: "Final reply draft:\n" + finishMessage,
		},
		{
			Role:    "user",
			Content: request.Prompt,
		},
	}
}

func contractVerifierSchema() string {
	return `{"type":"object","properties":{"satisfied":{"type":"boolean"},"reason":{"type":"string"},"missingDescription":{"type":"string"},"suggestedNextTools":{"type":"array","items":{"type":"string"}}},"required":["satisfied","reason","missingDescription","suggestedNextTools"],"additionalProperties":false}`
}

func normalizeContractSatisfactionVerification(request AgentTurnRequest, verification ContractSatisfactionVerification) ContractSatisfactionVerification {
	verification.Reason = strings.TrimSpace(verification.Reason)
	verification.MissingDescription = strings.TrimSpace(verification.MissingDescription)
	verification.SuggestedNextTools = registeredToolNamesOnly(request.ToolSet, appendUniqueStrings(verification.SuggestedNextTools))
	if verification.Satisfied {
		verification.MissingDescription = ""
		verification.SuggestedNextTools = nil
	}
	if verification.Reason == "" {
		if verification.Satisfied {
			verification.Reason = "The requested outcome is backed by observed evidence."
		} else {
			verification.Reason = "The requested outcome is not backed by observed evidence."
		}
	}
	return verification
}

func resultVerifierMessages(request AgentTurnRequest, expectedResults []ExpectedResult, observedResults []ObservedResult) []llm.Message {
	expectedDocument, _ := json.Marshal(expectedResults)
	observedDocument, _ := json.Marshal(observedResults)
	return []llm.Message{
		{
			Role:    "system",
			Content: "You verify whether a Blueclaw task produced the user's expected results. Expected results are natural-language deliverables. Types are only delivery shapes: message, file, link. Judge flexibly from observed results, not from tool names alone. Return missing when a required result is clearly absent, uncertain when evidence is ambiguous, and satisfied when the result is present. For required link results, the final message must include an exact observed URL so the user can open it.",
		},
		{
			Role:    "system",
			Content: "Expected results:\n" + string(expectedDocument),
		},
		{
			Role:    "system",
			Content: "Observed results:\n" + string(observedDocument),
		},
		{
			Role:    "user",
			Content: request.Prompt,
		},
	}
}

func resultVerifierSchema() string {
	return `{"type":"object","properties":{"overallStatus":{"type":"string","enum":["satisfied","missing","uncertain"]},"summary":{"type":"string"},"results":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"status":{"type":"string","enum":["satisfied","missing","uncertain"]},"reason":{"type":"string"},"citedObservationIDs":{"type":"array","items":{"type":"string"}},"missingDescription":{"type":"string"},"suggestedNextTools":{"type":"array","items":{"type":"string"}}},"required":["id","status","reason","citedObservationIDs","missingDescription","suggestedNextTools"],"additionalProperties":false}}},"required":["overallStatus","summary","results"],"additionalProperties":false}`
}

func normalizeResultVerification(expectedResults []ExpectedResult, verification ResultVerification) ResultVerification {
	itemByID := map[string]ResultVerificationItem{}
	for _, item := range verification.Results {
		normalizedItem := normalizeResultVerificationItem(item)
		if normalizedItem.ID != "" {
			itemByID[normalizedItem.ID] = normalizedItem
		}
	}
	results := []ResultVerificationItem{}
	for _, expectedResult := range expectedResults {
		item, isFound := itemByID[expectedResult.ID]
		if !isFound {
			item = ResultVerificationItem{
				ID:                 expectedResult.ID,
				Status:             "uncertain",
				Reason:             "The verifier did not evaluate this expected result.",
				MissingDescription: expectedResult.Description,
			}
		}
		results = append(results, item)
	}
	verification.Results = results
	verification.OverallStatus = normalizeResultVerificationOverallStatus(verification.OverallStatus, results)
	verification.Summary = strings.TrimSpace(verification.Summary)
	return verification
}

func normalizeResultVerificationItem(item ResultVerificationItem) ResultVerificationItem {
	item.ID = strings.TrimSpace(item.ID)
	item.Status = normalizeResultVerificationStatus(item.Status)
	item.Reason = strings.TrimSpace(item.Reason)
	item.MissingDescription = strings.TrimSpace(item.MissingDescription)
	item.CitedObservationIDs = appendUniqueStrings(item.CitedObservationIDs)
	item.SuggestedNextTools = appendUniqueStrings(item.SuggestedNextTools)
	return item
}

func enforceObservedResultRequirements(expectedResults []ExpectedResult, observedResults []ObservedResult, finishMessage string, verification ResultVerification) ResultVerification {
	hasFileResult := observedResultsContainType(observedResults, ExpectedResultTypeFile)
	for index, item := range verification.Results {
		expectedResult := expectedResultByID(expectedResults, item.ID)
		if !expectedResult.Required {
			continue
		}
		if canonicalFinalMessageIsReady(expectedResult, finishMessage) {
			verification.Results[index] = satisfiedFinalMessageResult(item)
			continue
		}
		linkResults := observedLinkResultsForExpectedResult(expectedResult, observedResults)
		if expectedResult.Type == ExpectedResultTypeLink && len(linkResults) == 0 {
			verification.Results[index] = missingObservedResultItem(item, "No link result was observed.")
		}
		if expectedResult.Type == ExpectedResultTypeLink && len(linkResults) > 0 && !finishMessageContainsObservedLink(finishMessage, linkResults) {
			verification.Results[index] = missingObservedResultItem(item, missingObservedLinkReason(linkResults))
		}
		if expectedResult.Type == ExpectedResultTypeFile && !hasFileResult {
			verification.Results[index] = missingObservedResultItem(item, "No file result was observed.")
		}
	}
	verification.OverallStatus = normalizeResultVerificationOverallStatus("satisfied", verification.Results)
	return verification
}

func canonicalFinalMessageIsReady(expectedResult ExpectedResult, finishMessage string) bool {
	return expectedResult.ID == "final-message" &&
		expectedResult.Type == ExpectedResultTypeMessage &&
		strings.TrimSpace(finishMessage) != ""
}

func satisfiedFinalMessageResult(item ResultVerificationItem) ResultVerificationItem {
	item.Status = "satisfied"
	item.Reason = "A non-empty final message is ready for delivery."
	item.CitedObservationIDs = nil
	item.MissingDescription = ""
	item.SuggestedNextTools = nil
	return item
}

func observedLinkResultsForExpectedResult(expectedResult ExpectedResult, observedResults []ObservedResult) []ObservedResult {
	linkResults := observedResultsByType(observedResults, ExpectedResultTypeLink)
	if len(linkResults) == 0 {
		return nil
	}
	if !expectedResultNeedsSitePublicURL(expectedResult) || !observedResultsContainSiteAppTool(observedResults) {
		return linkResults
	}
	siteLinkResults := []ObservedResult{}
	for _, observedResult := range linkResults {
		if siteToolCanSatisfyLinkResult(observedResult.ToolName) {
			siteLinkResults = append(siteLinkResults, observedResult)
		}
	}
	return siteLinkResults
}

func expectedResultNeedsSitePublicURL(expectedResult ExpectedResult) bool {
	values := append([]string{expectedResult.Description}, expectedResult.AcceptanceHints...)
	text := strings.ToLower(strings.Join(values, " "))
	if strings.Contains(text, "site.app") {
		return true
	}
	hasSiteReference := strings.Contains(text, "site") || strings.Contains(text, "website") || strings.Contains(text, "웹사이트")
	hasPublicReference := strings.Contains(text, "public") || strings.Contains(text, "published") || strings.Contains(text, "공개")
	return hasSiteReference && hasPublicReference
}

func observedResultsContainSiteAppTool(observedResults []ObservedResult) bool {
	for _, observedResult := range observedResults {
		if strings.HasPrefix(strings.TrimSpace(observedResult.ToolName), "site.") {
			return true
		}
	}
	return false
}

func siteToolCanSatisfyLinkResult(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "site.publish", "site.status":
		return true
	default:
		return false
	}
}

func missingObservedLinkReason(linkResults []ObservedResult) string {
	observedURLs := []string{}
	for _, observedResult := range linkResults {
		observedURL := normalizeObservedURL(observedResult.URL)
		if observedURL != "" {
			observedURLs = appendUniqueStrings(observedURLs, observedURL)
		}
	}
	if len(observedURLs) == 0 {
		return "Final message does not include an exact observed link URL."
	}
	return "Final message must include the published link verbatim. Copy this exact URL into your reply, character for character: " + strings.Join(observedURLs, " ")
}

func finishMessageContainsObservedLink(finishMessage string, observedResults []ObservedResult) bool {
	messageURLs := observedURLsFromText(finishMessage)
	if len(messageURLs) == 0 {
		return false
	}
	for _, observedResult := range observedResults {
		if normalizeExpectedResultType(observedResult.Type) != ExpectedResultTypeLink {
			continue
		}
		observedURL := normalizeObservedURL(observedResult.URL)
		if observedURL == "" {
			continue
		}
		if stringSliceContains(messageURLs, observedURL) {
			return true
		}
	}
	return false
}

func observedURLsFromText(value string) []string {
	urls := []string{}
	for _, match := range observedURLPattern.FindAllString(value, -1) {
		urls = appendUniqueStrings(urls, normalizeObservedURL(match))
	}
	return urls
}

func normalizeObservedURL(value string) string {
	normalizedURL := strings.TrimRight(strings.TrimSpace(value), ".,);:!?")
	return strings.TrimRight(normalizedURL, "/")
}

func expectedResultByID(expectedResults []ExpectedResult, id string) ExpectedResult {
	for _, expectedResult := range expectedResults {
		if expectedResult.ID == id {
			return expectedResult
		}
	}
	return ExpectedResult{}
}

func observedResultsContainType(observedResults []ObservedResult, resultType string) bool {
	return len(observedResultsByType(observedResults, resultType)) > 0
}

func observedResultsByType(observedResults []ObservedResult, resultType string) []ObservedResult {
	results := []ObservedResult{}
	for _, result := range observedResults {
		if normalizeExpectedResultType(result.Type) == resultType {
			results = append(results, result)
		}
	}
	return results
}

func missingObservedResultItem(item ResultVerificationItem, reason string) ResultVerificationItem {
	item.Status = "missing"
	item.Reason = reason
	item.CitedObservationIDs = nil
	if item.MissingDescription == "" {
		item.MissingDescription = reason
	}
	return item
}

func normalizeResultVerificationStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "satisfied", "missing", "uncertain":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "uncertain"
	}
}

func normalizeResultVerificationOverallStatus(value string, items []ResultVerificationItem) string {
	if strings.ToLower(strings.TrimSpace(value)) == "missing" {
		return "missing"
	}
	hasUncertainResult := false
	for _, item := range items {
		if item.Status == "missing" {
			return "missing"
		}
		if item.Status == "uncertain" {
			hasUncertainResult = true
		}
	}
	if hasUncertainResult {
		return "uncertain"
	}
	return "satisfied"
}

func blockingExpectedResultItems(contract OutcomeContract, verification ResultVerification, observations []turnObservation) []ResultVerificationItem {
	requiredResultByID := map[string]bool{}
	resultTypeByID := map[string]string{}
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if result.Required {
			requiredResultByID[result.ID] = true
		}
		resultTypeByID[result.ID] = result.Type
	}
	missingResults := []ResultVerificationItem{}
	for _, item := range verification.Results {
		if !requiredResultByID[item.ID] {
			continue
		}
		if expectedResultVerdictBlocksFinish(item, resultTypeByID[item.ID], observations) {
			missingResults = append(missingResults, item)
		}
	}
	return missingResults
}

func expectedResultVerdictBlocksFinish(item ResultVerificationItem, resultType string, observations []turnObservation) bool {
	if item.Status != "missing" && item.Status != "uncertain" {
		return false
	}
	if resultType == ExpectedResultTypeMessage || item.Status == "uncertain" {
		return !expectedResultWasPreviouslyFlagged(item.ID, observations)
	}
	return true
}

func expectedResultWasPreviouslyFlagged(resultID string, observations []turnObservation) bool {
	trimmedResultID := strings.TrimSpace(resultID)
	if trimmedResultID == "" {
		return false
	}
	for _, observation := range observations {
		if observation.Action != "evidence_missing" && observation.Action != "expected_result_missing" {
			continue
		}
		if strings.Contains(observation.ContentText(), trimmedResultID) {
			return true
		}
	}
	return false
}

func suggestedNextToolsForResultVerification(items []ResultVerificationItem) []string {
	toolNames := []string{}
	for _, item := range items {
		toolNames = appendUniqueStrings(toolNames, item.SuggestedNextTools...)
	}
	return toolNames
}
