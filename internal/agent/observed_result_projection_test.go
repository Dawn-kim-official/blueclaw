package agent

import "testing"

func TestObservedResultProjectionReportsCalendarClaimWithoutCalendarFact(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"calendar.event.add"})},
		[]turnObservation{newContentObservation("obs-001", "continue", "ask.input", "시간을 알려주세요.")},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "7월 13일 미팅을 오전 10시~11시로 등록했습니다.",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if len(projection.MissingRequirements) != 1 {
		t.Fatalf("expected one missing requirement, got %+v", projection.MissingRequirements)
	}
	requirement := projection.MissingRequirements[0]
	if requirement.ObjectType != "calendar_event" || requirement.Effect != "scheduled" {
		t.Fatalf("expected missing calendar scheduled fact, got %+v", requirement)
	}
	if len(requirement.SuggestedNextTools) != 1 || requirement.SuggestedNextTools[0] != "calendar.event.add" {
		t.Fatalf("expected calendar add suggestion, got %+v", requirement.SuggestedNextTools)
	}
}

func TestObservedResultProjectionAcceptsCalendarClaimWithCalendarFact(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"calendar.event.add"})},
		[]turnObservation{newContentObservation("obs-001", "continue", "calendar.event.add", `{"id":"event-1","title":"미팅"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "7월 13일 미팅을 오전 10시~11시로 등록했습니다.",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if len(projection.MissingRequirements) != 0 {
		t.Fatalf("expected no missing requirements, got %+v", projection.MissingRequirements)
	}
	if !projectionHasObservedFact(projection.ObservedFacts, "calendar_event", "scheduled") {
		t.Fatalf("expected calendar scheduled fact, got %+v", projection.ObservedFacts)
	}
}

func TestObservedResultProjectionReportsWebsitePublishClaimWithoutPublishFact(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"site.app.publish"})},
		[]turnObservation{newContentObservation("obs-001", "continue", "site.app.create", `{"siteID":"site-1","title":"Portfolio"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "웹사이트를 배포했습니다: https://portfolio.example",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if len(projection.MissingRequirements) != 1 {
		t.Fatalf("expected one missing requirement, got %+v", projection.MissingRequirements)
	}
	requirement := projection.MissingRequirements[0]
	if requirement.ObjectType != "website" || requirement.Effect != "published" {
		t.Fatalf("expected missing website publish fact, got %+v", requirement)
	}
}

func TestObservedResultProjectionDoesNotTreatUnpublishedStatusAsPublished(t *testing.T) {
	facts := factsFromObservation(newContentObservation("obs-001", "continue", "site.app.status", `{"siteID":"site-1","status":"unpublished"}`))

	if projectionHasObservedFact(facts, "website", "published") {
		t.Fatalf("expected unpublished status not to satisfy published fact, got %+v", facts)
	}
}
