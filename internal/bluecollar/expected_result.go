package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"regexp"
	"strings"
)

var observedURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

func validateExpectedResultDelivery(request AgentTurnRequest, observations []turnObservation, attachments []toolcontract.FileAttachment, actionDocument turnActionDocument) completionGateResult {
	expectedResults := normalizeExpectedResults(request.OutcomeContract.ExpectedResults)
	if len(expectedResults) == 0 {
		return completionGateResult{IsSatisfied: true, Attachments: attachments}
	}
	observedURLs := canonicalExpectedResultURLs(request, observations)
	finishMessage := finishActionMessage(actionDocument)
	for _, expectedResult := range expectedResults {
		if !expectedResult.Required {
			continue
		}
		if message := missingExpectedResultDelivery(expectedResult, request.ToolSet, observedURLs, attachments, finishMessage); message != "" {
			return completionGateResult{Message: message, EvidenceKind: evidenceKindExpectedResult}
		}
	}
	return completionGateResult{IsSatisfied: true, Attachments: attachments}
}

func missingExpectedResultDelivery(expectedResult ExpectedResult, toolSet *toolcontract.ToolSet, observedURLs []string, attachments []toolcontract.FileAttachment, finishMessage string) string {
	switch expectedResult.Type {
	case ExpectedResultTypeFile:
		if len(attachments) == 0 {
			return "finish requires a delivered file result"
		}
	case ExpectedResultTypeLink:
		if len(observedURLs) == 0 {
			if toolSetProducesCanonicalLinks(toolSet) {
				return "finish requires a canonical link result"
			}
			return ""
		}
		if !finishMessageContainsObservedURL(finishMessage, observedURLs) {
			return "final message must include this exact observed URL: " + strings.Join(observedURLs, " ")
		}
	case ExpectedResultTypeMessage:
		if strings.TrimSpace(finishMessage) == "" {
			return "finish requires a non-empty final message"
		}
	}
	return ""
}

func toolSetProducesCanonicalLinks(toolSet *toolcontract.ToolSet) bool {
	if toolSet == nil {
		return false
	}
	for _, toolName := range toolSet.ListToolNames() {
		definition, isFound := toolSet.ToolDefinition(toolName)
		if !isFound || definition.ResultContract == nil {
			continue
		}
		for _, effectContract := range definition.ResultContract.Effects {
			if strings.TrimSpace(effectContract.EffectIdentity) == "url" {
				return true
			}
		}
	}
	return false
}

func canonicalExpectedResultURLs(request AgentTurnRequest, observations []turnObservation) []string {
	facts := observedFactsFromObservations(request.ToolSet, observations)
	requiredEffects := normalizeOutcomeEffects(request.OutcomeContract.RequiredEffects)
	matchingURLs := observedFactURLs(facts, requiredEffects)
	if len(matchingURLs) > 0 {
		return matchingURLs
	}
	return observedFactURLs(facts, nil)
}

func observedFactURLs(facts []ObservedFact, requiredEffects []OutcomeEffect) []string {
	urls := []string{}
	for _, fact := range facts {
		if len(requiredEffects) > 0 && !factMatchesAnyRequiredEffect(fact, requiredEffects) {
			continue
		}
		if normalizedURL := normalizeObservedURL(fact.URL); normalizedURL != "" {
			urls = appendUniqueStrings(urls, normalizedURL)
		}
	}
	return urls
}

func factMatchesAnyRequiredEffect(fact ObservedFact, requiredEffects []OutcomeEffect) bool {
	for _, requiredEffect := range requiredEffects {
		if fact.ObjectType == requiredEffect.ObjectType && fact.Effect == requiredEffect.Effect {
			return true
		}
	}
	return false
}

func finishMessageContainsObservedURL(finishMessage string, observedURLs []string) bool {
	messageURLs := observedURLsFromText(finishMessage)
	for _, observedURL := range observedURLs {
		if stringSliceContains(messageURLs, normalizeObservedURL(observedURL)) {
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
