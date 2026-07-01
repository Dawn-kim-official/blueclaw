package agent

import (
	"encoding/json"
	"testing"
)

func TestParseCalendarDuplicateCandidatesReadsMarkerPayload(t *testing.T) {
	observation := turnObservation{}
	observation.Output.Data = json.RawMessage(`{"status":"duplicate_candidate","candidates":[{"id":"evt-1","title":"세라에스이 미팅","startISO":"2026-07-02T00:30:00Z","endISO":"2026-07-02T01:30:00Z"}]}`)
	candidates, isDuplicateCandidate := parseCalendarDuplicateCandidates(observation)
	if !isDuplicateCandidate || len(candidates) != 1 || candidates[0].ID != "evt-1" {
		t.Fatalf("expected one parsed candidate, got %v (%v)", candidates, isDuplicateCandidate)
	}

	created := turnObservation{}
	created.Output.Data = json.RawMessage(`{"id":"evt-2","title":"세라에스이 사장님 미팅","startISO":"2026-07-02T00:30:00Z"}`)
	if _, isDuplicateCandidate := parseCalendarDuplicateCandidates(created); isDuplicateCandidate {
		t.Fatalf("a normal created event must not be read as a duplicate candidate")
	}
}

func TestCalendarAddInputWithAllowDuplicateInjectsFlag(t *testing.T) {
	toolInput := json.RawMessage(`{"operation":"calendar.add","input":{"title":"세라에스이 사장님 미팅","startISO":"2026-07-02T09:30:00+09:00","endISO":"2026-07-02T10:30:00+09:00"}}`)
	forced := calendarAddInputWithAllowDuplicate(toolInput)
	var wrapper struct {
		Operation string `json:"operation"`
		Input     struct {
			Title          string `json:"title"`
			AllowDuplicate bool   `json:"allowDuplicate"`
		} `json:"input"`
	}
	if json.Unmarshal(forced, &wrapper) != nil {
		t.Fatalf("forced tool input is not valid json: %s", forced)
	}
	if wrapper.Operation != "calendar.add" || !wrapper.Input.AllowDuplicate || wrapper.Input.Title != "세라에스이 사장님 미팅" {
		t.Fatalf("expected allowDuplicate injected while preserving fields, got %s", forced)
	}
}
