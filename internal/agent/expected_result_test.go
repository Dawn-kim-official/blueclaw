package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObservedResultsIgnoreURLsWithoutCanonicalEffects(t *testing.T) {
	results := buildObservedResults(newTestToolSet([]string{"external.publish"}), []turnObservation{{
		ObservationID: "obs-001",
		Tool:          "external.publish",
		Output: ToolOutput{
			Content: `{"publicURL":"https://portfolio.example"}`,
			Data:    json.RawMessage(`{}`),
		},
	}}, nil, "초안을 만들었습니다: https://portfolio.example")

	if observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("uncontracted URL must not count as delivered link: %+v", results)
	}
}

func TestObservedResultsUseCanonicalURLEffectsWithoutToolNameInference(t *testing.T) {
	toolSet, observation := canonicalLinkObservation("external.publish", "https://portfolio.example")
	results := buildObservedResults(toolSet, []turnObservation{observation}, nil, "")

	if !observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("canonical URL effect should count as delivered link: %+v", results)
	}
	if results[0].ToolName != "external.publish" || results[0].URL != "https://portfolio.example" {
		t.Fatalf("expected exact canonical URL identity, got %+v", results)
	}
}

func TestObservedResultsRejectEffectsThatDriftFromTheirContract(t *testing.T) {
	toolSet, observation := canonicalLinkObservation("external.publish", "https://portfolio.example")
	observation.Effects[0].URL = "https://different.example"
	results := buildObservedResults(toolSet, []turnObservation{observation}, nil, "")

	if observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("mismatched effect identity must not count as delivered link: %+v", results)
	}
}

func TestObservedResultsPreserveTypedFileAttachment(t *testing.T) {
	results := buildObservedResults(
		nil,
		nil,
		[]FileAttachment{{Filename: "quarterly.docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}},
		"",
	)

	if !observedResultsContainType(results, ExpectedResultTypeFile) {
		t.Fatalf("typed attachment should count as a file result: %+v", results)
	}
	if results[0].Filename != "quarterly.docx" {
		t.Fatalf("expected exact attachment filename, got %+v", results[0])
	}
}

func canonicalLinkObservation(toolName string, publicURL string) (*ToolSet, turnObservation) {
	descriptor := canonicalLinkToolDefinition(toolName)
	result := canonicalLinkToolResult(publicURL)
	observation := turnObservation{
		ObservationID: "obs-001",
		Tool:          toolName,
		Output:        result.Output,
		Effects:       result.Effects,
	}
	return newTestToolSetWithDefinitions([]ToolDefinition{descriptor}), observation
}

func canonicalLinkToolDefinition(toolName string) ToolDefinition {
	descriptor := testToolDescriptor(toolName)
	descriptor.OutputSchema = json.RawMessage(`{"type":"object","properties":{"publicURL":{"type":"string"}},"required":["publicURL"],"additionalProperties":false}`)
	descriptor.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"publicURL":{"type":"string"}},"required":["publicURL"],"additionalProperties":false}`),
		Effects: []ResourceEffectContract{{
			ObjectType:     "website",
			Effect:         "published",
			ResultField:    "publicURL",
			EffectIdentity: "url",
		}},
	}
	return descriptor
}

func canonicalLinkToolResult(publicURL string) ToolResult {
	outputData := json.RawMessage(marshalEventBody(map[string]string{"publicURL": publicURL}))
	return ToolResult{
		Output:  ToolOutput{Content: string(outputData), Data: outputData},
		Effects: []ResourceEffect{{ObjectType: "website", Effect: "published", URL: publicURL}},
	}
}

func TestRequiredLinkVerificationRequiresObservedLinkResult(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:          "site-public-link",
		Type:        ExpectedResultTypeLink,
		Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
		Required:    true,
	}}
	verification := ResultVerification{
		OverallStatus: "satisfied",
		Results: []ResultVerificationItem{{
			ID:                  "site-public-link",
			Status:              "satisfied",
			Reason:              "The final message contains a URL.",
			CitedObservationIDs: []string{"obs-001"},
		}},
	}

	verification = enforceObservedResultRequirements(expectedResults, []ObservedResult{{
		Type:          ExpectedResultTypeMessage,
		Description:   "Final message draft: https://portfolio.example",
		ObservationID: "obs-001",
	}}, "https://portfolio.example", verification)

	if verification.OverallStatus != "missing" {
		t.Fatalf("expected missing verification without observed link, got %+v", verification)
	}
	if verification.Results[0].Status != "missing" {
		t.Fatalf("expected link result to be missing, got %+v", verification.Results[0])
	}
}

func TestRequiredLinkVerificationDoesNotInferProvenanceFromToolNames(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:              "site-public-link",
		Type:            ExpectedResultTypeLink,
		Description:     "public website URL",
		Required:        true,
		AcceptanceHints: []string{"site public URL deliverable"},
	}}
	verification := ResultVerification{
		OverallStatus: "satisfied",
		Results: []ResultVerificationItem{{
			ID:                  "site-public-link",
			Status:              "satisfied",
			Reason:              "A URL exists.",
			CitedObservationIDs: []string{"obs-002"},
		}},
	}
	observedResults := []ObservedResult{{
		Type:          ExpectedResultTypeMessage,
		Description:   "site.build result: build completed",
		ObservationID: "obs-001",
		ToolName:      "site.build",
	}, {
		Type:          ExpectedResultTypeLink,
		Description:   "web.search result: URL: https://example.com/reference",
		ObservationID: "obs-002",
		ToolName:      "web.search",
		URL:           "https://example.com/reference",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "https://example.com/reference", verification)

	if verification.OverallStatus != "satisfied" {
		t.Fatalf("expected canonical link result to avoid tool-name provenance inference, got %+v", verification)
	}
}

func TestRequiredGenericLinkVerificationAcceptsGenericURLWithoutSiteTool(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:          "reference-link",
		Type:        ExpectedResultTypeLink,
		Description: "reference URL",
		Required:    true,
	}}
	verification := ResultVerification{
		OverallStatus: "satisfied",
		Results: []ResultVerificationItem{{
			ID:                  "reference-link",
			Status:              "satisfied",
			Reason:              "A URL exists.",
			CitedObservationIDs: []string{"obs-001"},
		}},
	}
	observedResults := []ObservedResult{{
		Type:          ExpectedResultTypeLink,
		Description:   "web.search result: URL: https://example.com/reference",
		ObservationID: "obs-001",
		ToolName:      "web.search",
		URL:           "https://example.com/reference",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "https://example.com/reference", verification)

	if verification.OverallStatus != "satisfied" {
		t.Fatalf("expected generic URL to satisfy generic link result, got %+v", verification)
	}
}

func TestCanonicalFinalMessageUsesNonEmptyFinishDraftAsDeliveryEvidence(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:          "final-message",
		Type:        ExpectedResultTypeMessage,
		Description: "final reply to the user",
		Required:    true,
	}}
	verification := ResultVerification{
		OverallStatus: "missing",
		Results: []ResultVerificationItem{{
			ID:                 "final-message",
			Status:             "missing",
			Reason:             "The reply has not been delivered yet.",
			MissingDescription: "A final reply is missing.",
			SuggestedNextTools: []string{"finish"},
		}},
	}

	verification = enforceObservedResultRequirements(expectedResults, nil, "업무 상태를 진행으로 변경했습니다.", verification)

	if verification.OverallStatus != "satisfied" {
		t.Fatalf("expected ready final message to satisfy verification, got %+v", verification)
	}
	result := verification.Results[0]
	if result.Status != "satisfied" || result.MissingDescription != "" || len(result.SuggestedNextTools) != 0 {
		t.Fatalf("expected final message result to be satisfied without recovery, got %+v", result)
	}
}

func TestCanonicalFinalMessageStillRequiresNonEmptyFinishDraft(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:       "final-message",
		Type:     ExpectedResultTypeMessage,
		Required: true,
	}}
	verification := ResultVerification{
		OverallStatus: "missing",
		Results: []ResultVerificationItem{{
			ID:     "final-message",
			Status: "missing",
		}},
	}

	verification = enforceObservedResultRequirements(expectedResults, nil, "  ", verification)

	if verification.Results[0].Status != "missing" {
		t.Fatalf("expected empty final message to remain missing, got %+v", verification.Results[0])
	}
}

func TestRequiredLinkVerificationRequiresFinalMessageToUseObservedURL(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:          "site-public-link",
		Type:        ExpectedResultTypeLink,
		Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
		Required:    true,
	}}
	verification := ResultVerification{
		OverallStatus: "satisfied",
		Results: []ResultVerificationItem{{
			ID:                  "site-public-link",
			Status:              "satisfied",
			Reason:              "A published URL exists.",
			CitedObservationIDs: []string{"obs-001"},
		}},
	}
	observedResults := []ObservedResult{{
		Type:          ExpectedResultTypeLink,
		Description:   "site.publish result: URL: https://portfolio-probe.device.intern.kim",
		ObservationID: "obs-001",
		ToolName:      "site.publish",
		URL:           "https://portfolio-probe.device.intern.kim",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfoli-device.intern.kim", verification)

	if verification.OverallStatus != "missing" {
		t.Fatalf("expected missing verification with wrong final URL, got %+v", verification)
	}
	if !strings.Contains(verification.Results[0].Reason, "https://portfolio-probe.device.intern.kim") {
		t.Fatalf("missing reason must name the exact observed URL: %+v", verification.Results[0])
	}
}

func TestRequiredLinkVerificationAcceptsExactObservedURLInFinalMessage(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:          "site-public-link",
		Type:        ExpectedResultTypeLink,
		Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
		Required:    true,
	}}
	verification := ResultVerification{
		OverallStatus: "satisfied",
		Results: []ResultVerificationItem{{
			ID:                  "site-public-link",
			Status:              "satisfied",
			Reason:              "A published URL exists.",
			CitedObservationIDs: []string{"obs-001"},
		}},
	}
	observedResults := []ObservedResult{{
		Type:          ExpectedResultTypeLink,
		Description:   "site.publish result: URL: https://portfolio-probe.device.intern.kim",
		ObservationID: "obs-001",
		ToolName:      "site.publish",
		URL:           "https://portfolio-probe.device.intern.kim",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfolio-probe.device.intern.kim", verification)

	if verification.OverallStatus != "satisfied" {
		t.Fatalf("expected satisfied verification with exact final URL, got %+v", verification)
	}
}

func TestRequiredLinkVerificationAcceptsTrailingSlashDifference(t *testing.T) {
	expectedResults := []ExpectedResult{{
		ID:          "site-public-link",
		Type:        ExpectedResultTypeLink,
		Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
		Required:    true,
	}}
	verification := ResultVerification{
		OverallStatus: "satisfied",
		Results: []ResultVerificationItem{{
			ID:                  "site-public-link",
			Status:              "satisfied",
			Reason:              "A published URL exists.",
			CitedObservationIDs: []string{"obs-001"},
		}},
	}
	observedResults := []ObservedResult{{
		Type:          ExpectedResultTypeLink,
		Description:   "site.publish result: URL: https://portfolio-probe.device.intern.kim",
		ObservationID: "obs-001",
		ToolName:      "site.publish",
		URL:           "https://portfolio-probe.device.intern.kim",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfolio-probe.device.intern.kim/", verification)

	if verification.OverallStatus != "satisfied" {
		t.Fatalf("expected satisfied verification with trailing slash difference, got %+v", verification)
	}
}

func TestMessageResultBlocksOnceThenDelivers(t *testing.T) {
	contract := OutcomeContract{ExpectedResults: []ExpectedResult{
		{ID: "result-1", Type: ExpectedResultTypeMessage, Description: "self-intro text", Required: true},
	}}
	verification := ResultVerification{Results: []ResultVerificationItem{
		{ID: "result-1", Status: "missing"},
	}}

	firstPass := blockingExpectedResultItems(contract, verification, nil)
	if len(firstPass) != 1 {
		t.Fatalf("message result should block on first verdict, got %d", len(firstPass))
	}

	priorFlag := []turnObservation{{
		Action:           "evidence_missing",
		PolicyCode:       evidenceKindExpectedResult,
		RelatedResultIDs: []string{"result-1"},
	}}
	secondPass := blockingExpectedResultItems(contract, verification, priorFlag)
	if len(secondPass) != 0 {
		t.Fatalf("message result should deliver after one advisory round, got %d", len(secondPass))
	}
}

func TestFileResultBlocksEveryTime(t *testing.T) {
	contract := OutcomeContract{ExpectedResults: []ExpectedResult{
		{ID: "doc-1", Type: ExpectedResultTypeFile, Description: "the report file", Required: true},
	}}
	verification := ResultVerification{Results: []ResultVerificationItem{
		{ID: "doc-1", Status: "missing"},
	}}
	priorFlag := []turnObservation{{
		Action: "evidence_missing",
		Output: ToolOutput{Content: "missing required expected result: doc-1"},
	}}
	if len(blockingExpectedResultItems(contract, verification, priorFlag)) != 1 {
		t.Fatal("checkable file result must keep blocking until observed")
	}
}
