package agent

import "testing"

func TestCalendarFullCrudFinishIsBackedByObservedFacts(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"calendar.add", "calendar.update", "calendar.delete"})},
		[]turnObservation{
			newContentObservation("obs-001", "continue", "calendar.add", `{"id":"tool-a","title":"인턴테스트일정"}`),
			newContentObservation("obs-002", "continue", "calendar.update", `{"id":"tool-a","title":"인턴테스트일정"}`),
			newContentObservation("obs-003", "continue", "calendar.delete", `{"eventID":"tool-a","deleted":true}`),
		},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "일정을 생성하고 수정한 뒤 삭제했습니다.",
			GoalSatisfied: &goalSatisfied,
		},
	)
	if len(projection.MissingRequirements) != 0 {
		t.Fatalf("expected calendar add+update+delete to satisfy all claims, got missing %+v; facts %+v", projection.MissingRequirements, projection.ObservedFacts)
	}
}
