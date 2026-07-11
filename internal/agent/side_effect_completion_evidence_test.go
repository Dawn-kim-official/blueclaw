package agent

import "testing"

func TestSideEffectCompletionEvidenceRequiresSuccessfulMutationReference(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "schedule.create",
		Output:        ToolOutput{Content: `{"scheduleID":"schedule-1"}`},
	}}
	requirements := []toolUseRequirement{{ToolName: "schedule.create", RequiresSideEffectEvidence: true}}

	if _, errorValue := validateCompletionEvidence(requirements, observations, nil); errorValue == nil {
		t.Fatal("expected uncited schedule side effect to block completion")
	}
	if _, errorValue := validateCompletionEvidence(requirements, observations, []completionEvidenceReference{{ObservationID: "obs-001", ToolName: "schedule.create"}}); errorValue != nil {
		t.Fatalf("expected cited schedule side effect to satisfy completion evidence: %v", errorValue)
	}
}

func TestSideEffectCompletionEvidenceIgnoresReadOnlyObservation(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "schedule.list",
		Output:        ToolOutput{Content: `{"schedules":[]}`},
	}}
	requirements := []toolUseRequirement{{ToolName: "schedule.list"}}

	if _, errorValue := validateCompletionEvidence(requirements, observations, nil); errorValue != nil {
		t.Fatalf("read-only observations must not require completion evidence: %v", errorValue)
	}
}
