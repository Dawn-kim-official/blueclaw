package bluecollar

import (
	"context"
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"testing"
)

func TestObservedResultProjectionAcceptsCalendarClaimWithCalendarFact(t *testing.T) {
	goalSatisfied := true
	descriptor, observation := canonicalEffectObservation(
		"calendar_add",
		`{"eventID":"event-1"}`,
		[]toolcontract.ResourceEffect{{ObjectType: "calendar_event", Effect: "scheduled", ID: "event-1"}},
		[]toolcontract.ResourceEffectContract{{ObjectType: "calendar_event", Effect: "scheduled", ResultField: "eventID", EffectIdentity: "id"}},
	)
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor})},
		[]turnObservation{observation},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "Registered the July 13 meeting from 10 to 11am.",
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
	descriptor.ResultContract = &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
		Effects: []toolcontract.ResourceEffectContract{{
			ObjectType:     "task",
			Effect:         "created",
			ResultField:    "taskID",
			EffectIdentity: "id",
		}},
	}
	observation := newContentObservation("obs-001", "continue", "external.tasks.create", `{"taskID":"task-1"}`)
	observation.Output.Data = json.RawMessage(`{"taskID":"task-1"}`)
	observation.Effects = []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}}

	facts := factsFromObservation(newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor}), observation)

	if len(facts) != 1 || facts[0].ObjectType != "task" || facts[0].Effect != "created" || facts[0].ID != "task-1" {
		t.Fatalf("expected canonical resource effect, got %+v", facts)
	}
}

func TestObservedResultProjectionPreservesFileDeliveryEffect(t *testing.T) {
	path := "/workspace/circles/staff/reports/quarterly.docx"
	descriptor, observation := canonicalEffectObservation(
		toolcontract.FileDeliverToolName,
		`{"deliveredPaths":["/workspace/circles/staff/reports/quarterly.docx"]}`,
		[]toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: path}},
		[]toolcontract.ResourceEffectContract{{ObjectType: "file", Effect: "attached", ResultField: "deliveredPaths", EffectIdentity: "path"}},
	)
	projection := buildObservedResultProjection(
		AgentTurnRequest{ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor})},
		[]turnObservation{observation},
		[]toolcontract.FileAttachment{{Filename: "quarterly.docx"}},
		turnActionDocument{},
	)

	if len(projection.ObservedFacts) != 1 {
		t.Fatalf("expected one canonical delivery fact, got %+v", projection.ObservedFacts)
	}
	fact := projection.ObservedFacts[0]
	if fact.ObjectType != "file" || fact.Effect != "attached" || fact.Path != path {
		t.Fatalf("expected exact file delivery identity, got %+v", fact)
	}
}

func TestObservedResultProjectionRejectsEffectsWithoutContract(t *testing.T) {
	observation := newContentObservation("obs-001", "continue", "external.tasks.create", `{"taskID":"task-1"}`)
	observation.Effects = []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "created", ID: "task-1"}}
	toolSet := toolcontract.NewToolSet([]string{"external_tasks_create"})
	if errorValue := toolSet.RegisterBoundTool(toolcontract.BoundTool{
		Definition:   toolcontract.ToolDefinition{ID: "test:external.tasks.create", Name: "external.tasks.create", Visibility: toolcontract.ToolVisibilityInternal},
		Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
		Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
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
	observation := newContentObservation("obs-001", "continue", "schedule_create", `{"scheduleID":"schedule-1"}`)
	if facts := factsFromObservation(nil, observation); len(facts) != 0 {
		t.Fatalf("expected schedule facts to require canonical effects, got %+v", facts)
	}
}

func TestObservedResultProjectionDoesNotTreatUnpublishedStatusAsPublished(t *testing.T) {
	facts := factsFromObservation(newTestToolSet([]string{"site_list"}), newContentObservation("obs-001", "continue", "site_list", `{"siteID":"site-1","status":"published"}`))

	if projectionHasObservedFact(facts, "website", "published") {
		t.Fatalf("status text must not synthesize a published fact, got %+v", facts)
	}
}

func TestObservedResultProjectionRequiresCurrentSiteModificationEffects(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"site_list", "file_edit", "site_serve"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "workspace", Effect: "modified", SuggestedNextTools: []string{"file_edit"}},
				{ObjectType: "website", Effect: "published", SuggestedNextTools: []string{"site_serve"}},
			}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site_list", `{"siteID":"site-1","status":"published","publishedURL":"https://pretty-gyul.example"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "The tangerine site is already published: https://pretty-gyul.example",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if !projectionMissingRequirementContains(projection.MissingRequirements, "workspace", "modified") {
		t.Fatalf("expected missing workspace modification, got %+v", projection.MissingRequirements)
	}
}

func TestObservedResultProjectionAcceptsCurrentSiteModificationEffects(t *testing.T) {
	goalSatisfied := true
	fileDescriptor, fileObservation := canonicalEffectObservation(
		"file_edit",
		`{"paths":["/workspace/circles/staff/sites/pretty-gyul/draft/app/src/App.tsx"]}`,
		[]toolcontract.ResourceEffect{
			{ObjectType: "file", Effect: "updated", Path: "/workspace/circles/staff/sites/pretty-gyul/draft/app/src/App.tsx"},
			{ObjectType: "workspace", Effect: "modified", Path: "/workspace/circles/staff/sites/pretty-gyul/draft/app/src/App.tsx"},
		},
		[]toolcontract.ResourceEffectContract{
			{ObjectType: "file", Effect: "updated", ResultField: "paths", EffectIdentity: "path"},
			{ObjectType: "workspace", Effect: "modified", ResultField: "paths", EffectIdentity: "path"},
		},
	)
	publishDescriptor, publishObservation := canonicalEffectObservation(
		"site_serve",
		`{"siteID":"site-1"}`,
		[]toolcontract.ResourceEffect{{ObjectType: "website", Effect: "published", ID: "site-1"}},
		[]toolcontract.ResourceEffectContract{{ObjectType: "website", Effect: "published", ResultField: "siteID", EffectIdentity: "id"}},
	)
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{fileDescriptor, publishDescriptor}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "workspace", Effect: "modified", SuggestedNextTools: []string{"file_edit"}},
				{ObjectType: "website", Effect: "published", SuggestedNextTools: []string{"site_serve"}},
			}},
		},
		[]turnObservation{fileObservation, publishObservation},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "Made it prettier and redeployed: https://pretty-gyul.example",
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

func TestObservedResultProjectionDoesNotInferSiteReadEffectFromStatus(t *testing.T) {
	goalSatisfied := true
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSet([]string{"site_list"}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{{
				ObjectType:         "website",
				Effect:             "read",
				SuggestedNextTools: []string{"site_list"},
			}}},
		},
		[]turnObservation{newContentObservation("obs-001", "continue", "site_list", `{"siteID":"site-1","status":"published"}`)},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "Checked the site status.",
			GoalSatisfied: &goalSatisfied,
		},
	)

	if !projectionMissingRequirementContains(projection.MissingRequirements, "website", "read") {
		t.Fatalf("expected status without a canonical effect to remain missing, got %+v", projection.MissingRequirements)
	}
}

func TestObservedResultProjectionAllowsSiteDeleteEffect(t *testing.T) {
	goalSatisfied := true
	descriptor, observation := canonicalEffectObservation(
		"site_unserve",
		`{"siteID":"site-1"}`,
		[]toolcontract.ResourceEffect{{ObjectType: "website", Effect: "deleted", ID: "site-1"}},
		[]toolcontract.ResourceEffectContract{{ObjectType: "website", Effect: "deleted", ResultField: "siteID", EffectIdentity: "id"}},
	)
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{{
				ObjectType:         "website",
				Effect:             "deleted",
				SuggestedNextTools: []string{"site_unserve"},
			}}},
		},
		[]turnObservation{observation},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "Deleted the site.",
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

func canonicalEffectObservation(toolName string, resultData string, effects []toolcontract.ResourceEffect, contracts []toolcontract.ResourceEffectContract) (toolcontract.ToolDefinition, turnObservation) {
	descriptor := testToolDescriptor(toolName)
	descriptor.ResultContract = &toolcontract.ToolResultContract{
		Schema:  json.RawMessage(`{"type":"object"}`),
		Effects: contracts,
	}
	return descriptor, turnObservation{
		ObservationID: "obs-" + toolName,
		Action:        "continue",
		Tool:          toolName,
		Output:        toolcontract.ToolOutput{Content: resultData, Data: json.RawMessage(resultData)},
		Effects:       effects,
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
