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
			Description: "Final message draft: " + compactObservedResultText(finishMessage),
		})
	}
	return deduplicateObservedResults(results)
}

func observedResultsFromObservation(observation turnObservation) []ObservedResult {
	if observation.Failed() {
		return nil
	}
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
	case "site.app.create":
		return false
	case "site.app.status":
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
	hasLinkResult := observedResultsContainType(observedResults, ExpectedResultTypeLink)
	hasFileResult := observedResultsContainType(observedResults, ExpectedResultTypeFile)
	for index, item := range verification.Results {
		expectedResult := expectedResultByID(expectedResults, item.ID)
		if !expectedResult.Required {
			continue
		}
		if expectedResult.Type == ExpectedResultTypeLink && !hasLinkResult {
			verification.Results[index] = missingObservedResultItem(item, "No link result was observed.")
		}
		if expectedResult.Type == ExpectedResultTypeLink && hasLinkResult && !finishMessageContainsObservedLink(finishMessage, observedResults) {
			verification.Results[index] = missingObservedResultItem(item, "Final message does not include an exact observed link URL.")
		}
		if expectedResult.Type == ExpectedResultTypeFile && !hasFileResult {
			verification.Results[index] = missingObservedResultItem(item, "No file result was observed.")
		}
	}
	verification.OverallStatus = normalizeResultVerificationOverallStatus(verification.OverallStatus, verification.Results)
	return verification
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
	for _, result := range observedResults {
		if normalizeExpectedResultType(result.Type) == resultType {
			return true
		}
	}
	return false
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
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if result.Required {
			requiredResultByID[result.ID] = true
		}
	}
	missingResults := []ResultVerificationItem{}
	for _, item := range verification.Results {
		if !requiredResultByID[item.ID] {
			continue
		}
		if item.Status == "missing" || item.Status == "uncertain" && !priorUncertainResultWasReported(item.ID, observations) {
			missingResults = append(missingResults, item)
		}
	}
	return missingResults
}

func priorUncertainResultWasReported(resultID string, observations []turnObservation) bool {
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
