package agent

import (
	"strings"
	"testing"
)

func TestObservedResultsDoNotTreatDraftSiteCreateURLAsDeliverableLink(t *testing.T) {
	results := buildObservedResults([]turnObservation{{
		ObservationID: "obs-001",
		Tool:          "site.create",
		Output: ToolOutput{
			Content: `{"siteID":"site-1","status":"draft","publishedURL":"https://portfolio.example"}`,
		},
	}}, nil, "초안을 만들었습니다: https://portfolio.example")

	if observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("draft site create URL must not count as delivered link: %+v", results)
	}
	if !observedResultsContainType(results, ExpectedResultTypeMessage) {
		t.Fatalf("expected draft create to remain visible as a message result: %+v", results)
	}
}

func TestObservedResultsTreatPublishedSiteStatusAsDeliverableLink(t *testing.T) {
	results := buildObservedResults([]turnObservation{{
		ObservationID: "obs-001",
		Tool:          "site.status",
		Output: ToolOutput{
			Content: `{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example"}`,
		},
	}}, nil, "")

	if !observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("published site status URL should count as delivered link: %+v", results)
	}
}

func TestObservedResultsTreatPublishedSitePublishAsDeliverableLink(t *testing.T) {
	results := buildObservedResults([]turnObservation{{
		ObservationID: "obs-001",
		Tool:          "site.publish",
		Output: ToolOutput{
			Content: `{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example"}`,
		},
	}}, nil, "")

	if !observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("published site publish URL should count as delivered link: %+v", results)
	}
}

func TestObservedResultsDoNotTreatDraftSitePublishURLAsDeliverableLink(t *testing.T) {
	results := buildObservedResults([]turnObservation{{
		ObservationID: "obs-001",
		Tool:          "site.publish",
		Output: ToolOutput{
			Content: `{"siteID":"site-1","status":"draft","publishedURL":"https://portfolio.example"}`,
		},
	}}, nil, "배포했습니다: https://portfolio.example")

	if observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("draft site publish URL must not count as delivered link: %+v", results)
	}
}

func TestObservedResultsDoNotTreatFailedSiteStatusURLAsDeliverableLink(t *testing.T) {
	results := buildObservedResults([]turnObservation{{
		ObservationID: "obs-001",
		Tool:          "site.status",
		Output: ToolOutput{
			Content: `{"siteID":"site-1","status":"failed","publishedURL":"https://portfolio.example"}`,
		},
	}}, nil, "배포했습니다: https://portfolio.example")

	if observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("failed site status URL must not count as delivered link: %+v", results)
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

func TestRequiredSiteLinkVerificationRejectsGenericURLWhenSiteToolWasUsed(t *testing.T) {
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

	if verification.OverallStatus != "missing" {
		t.Fatalf("expected generic URL to miss site public link result, got %+v", verification)
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
		Description:   "site.publish result: URL: https://portfolio-probe.device.example.test",
		ObservationID: "obs-001",
		ToolName:      "site.publish",
		URL:           "https://portfolio-probe.device.example.test",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfoli-device.example.test", verification)

	if verification.OverallStatus != "missing" {
		t.Fatalf("expected missing verification with wrong final URL, got %+v", verification)
	}
	if !strings.Contains(verification.Results[0].Reason, "https://portfolio-probe.device.example.test") {
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
		Description:   "site.publish result: URL: https://portfolio-probe.device.example.test",
		ObservationID: "obs-001",
		ToolName:      "site.publish",
		URL:           "https://portfolio-probe.device.example.test",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfolio-probe.device.example.test", verification)

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
		Description:   "site.publish result: URL: https://portfolio-probe.device.example.test",
		ObservationID: "obs-001",
		ToolName:      "site.publish",
		URL:           "https://portfolio-probe.device.example.test",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfolio-probe.device.example.test/", verification)

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
