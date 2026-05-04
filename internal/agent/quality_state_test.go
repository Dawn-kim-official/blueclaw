package agent

import "testing"

func TestQualityReviewRequiresDeclaredCriteria(t *testing.T) {
	actionDocument := turnActionDocument{
		Action:             "final_reply",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
	}
	result := validateCompletionGateForRequest(AgentTurnRequest{
		QualityAcceptanceGuidance: []string{"declare task quality criteria"},
	}, nil, nil, nil, actionDocument)

	if result.IsSatisfied {
		t.Fatal("expected artifact quality guidance to require declared criteria")
	}
}

func TestQualityReviewRequiresPassingEvidence(t *testing.T) {
	criteria := normalizeQualityCriteria([]qualityCriterion{{
		ID:          "original-request",
		Description: "Original request is preserved.",
	}})
	review := []qualityReviewItem{{
		ID:       "original-request",
		Passed:   true,
		Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "terminal.run"}},
	}}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "call_tool",
		Tool:          "terminal.run",
		Content:       "ok",
	}}

	if errorValue := validateQualityReview(criteria, review, observations); errorValue != nil {
		t.Fatalf("expected quality review to pass: %v", errorValue)
	}
}

func TestQualityReviewRejectsFailedCriterion(t *testing.T) {
	criteria := normalizeQualityCriteria([]qualityCriterion{{
		ID:          "formats",
		Description: "All requested formats are attached.",
	}})
	review := []qualityReviewItem{{
		ID:       "formats",
		Passed:   false,
		Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.attach"}},
	}}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "call_tool",
		Tool:          "file.attach",
		Content:       "file attached",
	}}

	if errorValue := validateQualityReview(criteria, review, observations); errorValue == nil {
		t.Fatal("expected failed criterion to be rejected")
	}
}

func TestQualityReviewRejectsMissingEvidence(t *testing.T) {
	criteria := normalizeQualityCriteria([]qualityCriterion{{
		ID:          "design",
		Description: "DESIGN.md is reflected in final artifacts.",
	}})
	review := []qualityReviewItem{{
		ID:     "design",
		Passed: true,
	}}

	if errorValue := validateQualityReview(criteria, review, nil); errorValue == nil {
		t.Fatal("expected missing evidence to be rejected")
	}
}

func boolPointer(value bool) *bool {
	return &value
}
