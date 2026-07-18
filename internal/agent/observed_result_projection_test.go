package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestObservedResultProjectionAcceptsCalendarClaimWithCalendarFact(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSet([]string{"calendar.add"})},
		[]turnObservation{newContentObservation("obs-001", "continue", "calendar.add", `{"id":"event-1","title":"미팅"}`)},
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

func TestObservedResultProjectionUsesCanonicalEffectsWithoutToolNameInference(t *testing.T) {
	descriptor := testToolDescriptor("external.tasks.create")
	descriptor.ResultContract = &ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
		Effects: []ResourceEffectContract{{
			ObjectType:     "task",
			Effect:         "created",
			ResultField:    "taskID",
			EffectIdentity: "id",
		}},
	}
	observation := newContentObservation("obs-001", "continue", "external.tasks.create", `{"taskID":"task-1"}`)
	observation.Effects = []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}}

	facts := factsFromObservation(newTestToolSetWithDefinitions([]ToolDefinition{descriptor}), observation)

	if len(facts) != 1 || facts[0].ObjectType != "task" || facts[0].Effect != "created" || facts[0].ID != "task-1" {
		t.Fatalf("expected canonical resource effect, got %+v", facts)
	}
}

func TestObservedResultProjectionRejectsEffectsWithoutContract(t *testing.T) {
	observation := newContentObservation("obs-001", "continue", "external.tasks.create", `{"taskID":"task-1"}`)
	observation.Effects = []ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}}
	toolSet := NewToolSet([]string{"external.tasks.create"})
	if errorValue := toolSet.RegisterBoundTool(BoundTool{
		Definition:   ToolDefinition{ID: "test:external.tasks.create", Name: "external.tasks.create", Visibility: ToolVisibilityInternal},
		Availability: ToolAvailability{Status: ToolAvailabilityAvailable},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	facts := factsFromObservation(toolSet, observation)

	if len(facts) != 0 {
		t.Fatalf("expected uncontracted effects to be ignored, got %+v", facts)
	}
}

func TestObservedResultProjectionDoesNotInferScheduleFactsFromToolName(t *testing.T) {
	observation := newContentObservation("obs-001", "continue", "schedule.create", `{"scheduleID":"schedule-1"}`)
	if facts := factsFromObservation(nil, observation); len(facts) != 0 {
		t.Fatalf("expected schedule facts to require canonical effects, got %+v", facts)
	}
}

func TestObservedResultProjectionDoesNotTreatUnpublishedStatusAsPublished(t *testing.T) {
	facts := factsFromObservation(nil, newContentObservation("obs-001", "continue", "site.status", `{"siteID":"site-1","status":"unpublished"}`))

	if projectionHasObservedFact(facts, "website", "published") {
		t.Fatalf("expected unpublished status not to satisfy published fact, got %+v", facts)
	}
}

func TestObservedResultProjectionRequiresCurrentSiteModificationEffects(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"site.status", "file.edit", "site.publish"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "workspace", Effect: "modified", SuggestedNextTools: []string{"file.edit"}},
				{ObjectType: "website", Effect: "published", SuggestedNextTools: []string{"site.publish"}},
			}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site.status", `{"siteID":"site-1","status":"published","publishedURL":"https://pretty-gyul.example"}`)},
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
}

func TestObservedResultProjectionAcceptsCurrentSiteModificationEffects(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"file.edit", "site.publish"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "workspace", Effect: "modified", SuggestedNextTools: []string{"file.edit"}},
				{ObjectType: "website", Effect: "published", SuggestedNextTools: []string{"site.publish"}},
			}},
		},
		[]turnObservation{
			newContentObservation("obs-001", "continue", "file.edit", `{"path":"/workspace/circles/staff/sites/pretty-gyul/draft/app/src/App.tsx"}`),
			newContentObservation("obs-002", "continue", "site.publish", `{"siteID":"site-1","status":"published","publishedURL":"https://pretty-gyul.example"}`),
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
			ToolSet: newTestToolSet([]string{"site.status"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{{
				ObjectType:         "website",
				Effect:             "read",
				SuggestedNextTools: []string{"site.status"},
			}}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site.status", `{"siteID":"site-1","status":"published"}`)},
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

func TestObservedResultProjectionAllowsSiteDeleteEffect(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"site.delete"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{{
				ObjectType:         "website",
				Effect:             "deleted",
				SuggestedNextTools: []string{"site.delete"},
			}}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site.delete", `{"siteID":"site-1","status":"deleted"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "사이트를 삭제했습니다.",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if len(projection.MissingRequirements) != 0 {
		t.Fatalf("expected no missing requirements, got %+v", projection.MissingRequirements)
	}
	if !projectionHasObservedFact(projection.ObservedFacts, "website", "deleted") {
		t.Fatalf("expected website deleted fact, got %+v", projection.ObservedFacts)
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
