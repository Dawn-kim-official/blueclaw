package agent

import "testing"

func TestQualityReviewGuidanceDoesNotBlockCompletion(t *testing.T) {
	actionDocument := turnActionDocument{
		Action:             "finish",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
	}
	result := validateCompletionGateForRequest(AgentTurnRequest{
		QualityAcceptanceGuidance: []string{"declare task quality criteria"},
	}, nil, nil, nil, actionDocument)

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
		Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.attach"}},
	}}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.attach",
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
		CompletionEvidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "site.app.create"}},
		QualityReview: []qualityReviewItem{{
			ID:       "business-plan-sample-is-complete",
			Passed:   false,
			Evidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "site.app.create"}},
		}},
		Message: "완료했습니다.",
	}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site.app.create",
		Output:        ToolOutput{Content: `{"siteID":"site-1"}`},
	}}

	result := validateCompletionGateForRequest(AgentTurnRequest{}, nil, observations, criteria, actionDocument)

	if !result.IsSatisfied {
		t.Fatalf("expected failed declared quality criterion to stay a review hint, got %+v", result)
	}
}

func TestCompletionGateRejectsSandboxArtifactLocator(t *testing.T) {
	criteria := normalizeQualityCriteria([]string{"HTML artifact is attached."})
	evidence := []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.attach", AttachmentIndex: intPointer(0)}}
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
		Message: "HTML 파일을 만들었습니다: sandbox:/mnt/data/Hermes_Agent_Analysis.html",
	}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.attach",
		Output:        ToolOutput{Content: "file attached"},
		Attachments: []FileAttachment{{
			Filename:   "hermes-analysis.html",
			DevicePath: "/root/.blueclaw/workspace/hermes-analysis.html",
		}},
	}}

	result := validateCompletionGateForRequest(AgentTurnRequest{
		QualityAcceptanceGuidance: []string{"deliver the requested HTML"},
	}, []toolUseRequirement{{
		ToolName:           "file.attach",
		RequiresAttachment: true,
		AttachmentSuffixes: []string{".html"},
	}}, observations, criteria, actionDocument)

	if result.IsSatisfied {
		t.Fatal("expected sandbox artifact locator to be rejected")
	}
	if result.Message == "" {
		t.Fatal("expected rejection message")
	}
}

func TestCompletionGateRejectsUnattachedArtifactFilename(t *testing.T) {
	actionDocument := turnActionDocument{
		Action:             "finish",
		GoalStatus:         "satisfied",
		GoalSatisfied:      boolPointer(true),
		CompletionEvidence: []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "file.attach", AttachmentIndex: intPointer(0)}},
		Message:            "요청하신 파일을 생성해 첨부했습니다: Hermes_Agent_Analysis.html",
	}
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.attach",
		Output:        ToolOutput{Content: "file attached"},
		Attachments: []FileAttachment{{
			Filename:   "hermes-analysis.html",
			DevicePath: "/root/.blueclaw/workspace/hermes-analysis.html",
		}},
	}}

	result := validateCompletionGateForRequest(AgentTurnRequest{}, []toolUseRequirement{{
		ToolName:           "file.attach",
		RequiresAttachment: true,
		AttachmentSuffixes: []string{".html"},
	}}, observations, nil, actionDocument)

	if result.IsSatisfied {
		t.Fatal("expected unattached artifact filename to be rejected")
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
