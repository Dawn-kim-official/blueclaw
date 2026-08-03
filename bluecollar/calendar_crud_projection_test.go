package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

import "testing"

func TestCalendarFullCrudFinishIsBackedByObservedFacts(t *testing.T) {
	goalSatisfied := true
	addDescriptor, addObservation := calendarEffectObservation("calendar_add", "scheduled")
	updateDescriptor, updateObservation := calendarEffectObservation("calendar_update", "updated")
	deleteDescriptor, deleteObservation := calendarEffectObservation("calendar_delete", "deleted")
	projection := buildObservedResultProjection(
		AgentTurnRequest{
			ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{addDescriptor, updateDescriptor, deleteDescriptor}),
			OutcomeContract: OutcomeContract{RequiredEffects: []OutcomeEffect{
				{ObjectType: "calendar_event", Effect: "scheduled"},
				{ObjectType: "calendar_event", Effect: "updated"},
				{ObjectType: "calendar_event", Effect: "deleted"},
			}},
		},
		[]turnObservation{addObservation, updateObservation, deleteObservation},
		nil,
		turnActionDocument{
			Action:        "finish",
			Message:       "Created, updated, and then deleted the event.",
			GoalSatisfied: &goalSatisfied,
		},
	)
	if len(projection.MissingRequirements) != 0 {
		t.Fatalf("expected calendar add+update+delete to satisfy all claims, got missing %+v; facts %+v", projection.MissingRequirements, projection.ObservedFacts)
	}
}

func calendarEffectObservation(toolName string, effect string) (toolcontract.ToolDefinition, turnObservation) {
	return canonicalEffectObservation(
		toolName,
		`{"eventID":"tool-a"}`,
		[]toolcontract.ResourceEffect{{ObjectType: "calendar_event", Effect: effect, ID: "tool-a"}},
		[]toolcontract.ResourceEffectContract{{ObjectType: "calendar_event", Effect: effect, ResultField: "eventID", EffectIdentity: "id"}},
	)
}
