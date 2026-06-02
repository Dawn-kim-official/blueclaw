package agent

import "testing"

func TestObservedResultsDoNotTreatDraftSiteCreateURLAsDeliverableLink(t *testing.T) {
	results := buildObservedResults([]turnObservation{{
		ObservationID: "obs-001",
		Tool:          "site.app.create",
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
		Tool:          "site.app.status",
		Output: ToolOutput{
			Content: `{"siteID":"site-1","status":"published","publishedURL":"https://portfolio.example"}`,
		},
	}}, nil, "")

	if !observedResultsContainType(results, ExpectedResultTypeLink) {
		t.Fatalf("published site status URL should count as delivered link: %+v", results)
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
		Description:   "site.app.publish result: URL: https://portfolio-probe.device.example.test",
		ObservationID: "obs-001",
		ToolName:      "site.app.publish",
		URL:           "https://portfolio-probe.device.example.test",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfoli-device.example.test", verification)

	if verification.OverallStatus != "missing" {
		t.Fatalf("expected missing verification with wrong final URL, got %+v", verification)
	}
	if verification.Results[0].Reason != "Final message does not include an exact observed link URL." {
		t.Fatalf("unexpected missing reason: %+v", verification.Results[0])
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
		Description:   "site.app.publish result: URL: https://portfolio-probe.device.example.test",
		ObservationID: "obs-001",
		ToolName:      "site.app.publish",
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
		Description:   "site.app.publish result: URL: https://portfolio-probe.device.example.test",
		ObservationID: "obs-001",
		ToolName:      "site.app.publish",
		URL:           "https://portfolio-probe.device.example.test",
	}}

	verification = enforceObservedResultRequirements(expectedResults, observedResults, "배포했습니다: https://portfolio-probe.device.example.test/", verification)

	if verification.OverallStatus != "satisfied" {
		t.Fatalf("expected satisfied verification with trailing slash difference, got %+v", verification)
	}
}
