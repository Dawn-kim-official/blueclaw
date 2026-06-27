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

func TestObservedResultProjectionRequiresCurrentSiteModificationEffects(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"site.app.status", "file.edit", "site.app.build", "site.app.publish"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "workspace", Effect: "modified", SuggestedNextTools: []string{"file.edit"}},
				{ObjectType: "website", Effect: "built", SuggestedNextTools: []string{"site.app.build"}},
				{ObjectType: "website", Effect: "published", SuggestedNextTools: []string{"site.app.publish"}},
			}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site.app.status", `{"siteID":"site-1","status":"published","publishedURL":"https://pretty-gyul.example"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "예쁜 귤 사이트는 이미 게시되어 있습니다: https://pretty-gyul.example",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if !projectionMissingRequirementContains(projection.MissingRequirements, "workspace", "modified") {
		t.Fatalf("expected missing workspace modification, got %+v", projection.MissingRequirements)
	}
	if !projectionMissingRequirementContains(projection.MissingRequirements, "website", "built") {
		t.Fatalf("expected missing website build, got %+v", projection.MissingRequirements)
	}
}

func TestObservedResultProjectionAcceptsCurrentSiteModificationEffects(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"file.edit", "site.app.build", "site.app.publish"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "workspace", Effect: "modified", SuggestedNextTools: []string{"file.edit"}},
				{ObjectType: "website", Effect: "built", SuggestedNextTools: []string{"site.app.build"}},
				{ObjectType: "website", Effect: "published", SuggestedNextTools: []string{"site.app.publish"}},
			}},
		},
		[]turnObservation{
			newContentObservation("obs-001", "continue", "file.edit", `{"path":"/workspace/circles/staff/sites/pretty-gyul/draft/app/src/App.tsx"}`),
			newContentObservation("obs-002", "continue", "site.app.build", `{"siteID":"site-1","status":"built"}`),
			newContentObservation("obs-003", "continue", "site.app.publish", `{"siteID":"site-1","status":"published","publishedURL":"https://pretty-gyul.example"}`),
		},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "더 예쁘게 수정해서 배포했습니다: https://pretty-gyul.example",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if len(projection.MissingRequirements) != 0 {
		t.Fatalf("expected no missing requirements, got %+v", projection.MissingRequirements)
	}
	if !projectionHasObservedFact(projection.ObservedFacts, "workspace", "modified") {
		t.Fatalf("expected workspace modification fact, got %+v", projection.ObservedFacts)
	}
}

func TestObservedResultProjectionAllowsSiteReadEffectFromStatus(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"site.app.status"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{{
				ObjectType:         "website",
				Effect:             "read",
				SuggestedNextTools: []string{"site.app.status"},
			}}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site.app.status", `{"siteID":"site-1","status":"published"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "사이트 상태를 확인했습니다.",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if len(projection.MissingRequirements) != 0 {
		t.Fatalf("expected no missing requirements, got %+v", projection.MissingRequirements)
	}
}

func projectionMissingRequirementContains(requirements []ProjectionMissingRequirement, objectType string, effect string) bool {
	for _, requirement := range requirements {
		if requirement.ObjectType == objectType && requirement.Effect == effect {
			return true
		}
	}
	return false
}
