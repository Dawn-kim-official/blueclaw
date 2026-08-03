package bluecollar

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParseCalendarDuplicateCandidatesReadsMarkerPayload(t *testing.T) {
	observation := turnObservation{}
	observation.Output.Data = json.RawMessage(`{"status":"duplicate_candidate","candidates":[{"id":"evt-1","title":"Sera SE meeting","startISO":"2026-07-02T00:30:00Z","endISO":"2026-07-02T01:30:00Z"}]}`)
	candidates, isDuplicateCandidate := parseCalendarDuplicateCandidates(observation)
	if !isDuplicateCandidate || len(candidates) != 1 || candidates[0].ID != "evt-1" {
		t.Fatalf("expected one parsed candidate, got %v (%v)", candidates, isDuplicateCandidate)
	}

	created := turnObservation{}
	created.Output.Data = json.RawMessage(`{"id":"evt-2","title":"meeting with the Sera SE chief executive","startISO":"2026-07-02T00:30:00Z"}`)
	if _, isDuplicateCandidate := parseCalendarDuplicateCandidates(created); isDuplicateCandidate {
		t.Fatalf("a normal created event must not be read as a duplicate candidate")
	}
}

func TestToolConflictResolutionUsesInvocationContext(t *testing.T) {
	baseContext := context.Background()
	if resolution := ToolConflictResolutionFromContext(baseContext); resolution != "" {
		t.Fatalf("expected empty base resolution, got %q", resolution)
	}
	retryContext := WithToolConflictResolution(baseContext, ToolConflictResolutionAllowDuplicate)
	if resolution := ToolConflictResolutionFromContext(retryContext); resolution != ToolConflictResolutionAllowDuplicate {
		t.Fatalf("expected duplicate resolution, got %q", resolution)
	}
}
