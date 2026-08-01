package bluecollar

import "testing"

func TestQualityReviewGuidanceDoesNotBlockCompletion(t *testing.T) {
	actionDocument := turnActionDocument{
		Action:             "finish",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
	}
	result := validateCompletionGateForRequest(AgentTurnRequest{}, nil, nil, nil, actionDocument)

	if !result.IsSatisfied {
		t.Fatalf("expected quality guidance to stay advisory, got %s", result.Message)
	}
}

func TestQualityReviewRequiresPassingEvidence(t *testing.T) {
	criteria := normalizeQualityCriteria([]string{"Original request is preserved."})
	review := []qualityReviewItem{{
		ID:       "original-request-is-preserved",
		Passed:   true,
		Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "terminal.run"}},
	}}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "terminal.run",
		Output:        ToolOutput{Content: "ok"},
	}}

	if errorValue := validateQualityReview(criteria, review, observations); errorValue != nil {
		t.Fatalf("expected quality review to pass: %v", errorValue)
	}
}

func TestQualityReviewRejectsFailedCriterion(t *testing.T) {
	criteria := normalizeQualityCriteria([]string{"All requested formats are attached."})
	review := []qualityReviewItem{{
		ID:       "all-requested-formats-are-attached",
		Passed:   false,
		Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.deliver"}},
	}}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.deliver",
		Output:        ToolOutput{Content: "file attached"},
	}}

	if errorValue := validateQualityReview(criteria, review, observations); errorValue == nil {
		t.Fatal("expected failed criterion to be rejected")
	}
}

func TestQualityReviewRejectsMissingEvidence(t *testing.T) {
	criteria := normalizeQualityCriteria([]string{"DESIGN.md is reflected in final artifacts."})
	review := []qualityReviewItem{{
		ID:     "design-md-is-reflected-in-final-artifacts",
		Passed: true,
	}}

	if errorValue := validateQualityReview(criteria, review, nil); errorValue == nil {
		t.Fatal("expected missing evidence to be rejected")
	}
}

func TestCompletionGateTreatsFailedDeclaredQualityCriterionAsReviewHint(t *testing.T) {
	criteria := normalizeQualityCriteria([]string{"Business plan sample is complete."})
	actionDocument := turnActionDocument{
		Action:             "finish",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "site.serve"}},
		QualityReview: []qualityReviewItem{{
			ID:       "business-plan-sample-is-complete",
			Passed:   false,
			Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "site.serve"}},
		}},
		Message: "Done.",
	}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.serve",
		Output:        ToolOutput{Content: `{"siteID":"site-1"}`},
	}}

	result := validateCompletionGateForRequest(AgentTurnRequest{}, nil, observations, criteria, actionDocument)

	if !result.IsSatisfied {
		t.Fatalf("expected failed declared quality criterion to stay a review hint, got %+v", result)
	}
}

func TestCompletionGateUsesTypedEvidenceInsteadOfParsingFinishMessage(t *testing.T) {
	criteria := normalizeQualityCriteria([]string{"HTML artifact is attached."})
	evidence := []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.deliver", AttachmentIndex: intPointer(0)}}
	actionDocument := turnActionDocument{
		Action:             "finish",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: evidence,
		QualityReview: []qualityReviewItem{{
			ID:       "html-artifact-is-attached",
			Passed:   true,
			Evidence: evidence,
		}},
		Message: "Created the HTML file: sandbox:/mnt/data/Hermes_Agent_Analysis.html",
	}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.deliver",
		Output:        ToolOutput{Content: "file attached"},
		Attachments: []FileAttachment{{
			Filename:   "hermes-analysis.html",
			DevicePath: "/root/.blueclaw/workspace/hermes-analysis.html",
		}},
	}}

	result := validateCompletionGateForRequest(AgentTurnRequest{}, []toolUseRequirement{{
		ToolName:           "file.deliver",
		RequiresAttachment: true,
		AttachmentSuffixes: []string{".html"},
	}}, observations, criteria, actionDocument)

	if !result.IsSatisfied || len(result.Attachments) != 1 {
		t.Fatalf("expected typed completion evidence to satisfy the gate, got %+v", result)
	}
}

func TestCompletionGateDoesNotInferAttachmentsFromFinishMessage(t *testing.T) {
	actionDocument := turnActionDocument{
		Action:             "finish",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.deliver", AttachmentIndex: intPointer(0)}},
		Message:            "Created the requested file and attached it: Hermes_Agent_Analysis.html",
	}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.deliver",
		Output:        ToolOutput{Content: "file attached"},
		Attachments: []FileAttachment{{
			Filename:   "hermes-analysis.html",
			DevicePath: "/root/.blueclaw/workspace/hermes-analysis.html",
		}},
	}}

	result := validateCompletionGateForRequest(AgentTurnRequest{}, []toolUseRequirement{{
		ToolName:           "file.deliver",
		RequiresAttachment: true,
		AttachmentSuffixes: []string{".html"},
	}}, observations, nil, actionDocument)

	if !result.IsSatisfied || len(result.Attachments) != 1 || result.Attachments[0].Filename != "hermes-analysis.html" {
		t.Fatalf("expected cited attachment evidence to remain authoritative, got %+v", result)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
