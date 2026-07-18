package agent

import (
	"encoding/json"
	"testing"
)

func TestNormalizePersistedActiveGoalMigratesLegacyToolNames(t *testing.T) {
	activeGoal := ActiveGoal{
		SelectedToolNames: []string{"terminal.session", "site.promote"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file.attach", "artifact.deliver"},
			RequiredEvidenceAnyOf: [][]string{{"ask.choice", "terminal.session"}},
			SelectedEvidenceHints: []string{"site.promote"},
			ExpectedResults: []ExpectedResult{{
				Description:     "choice",
				Required:        true,
				AcceptanceHints: []string{"ask.choice"},
			}},
			RequiredEffects: []OutcomeEffect{{
				ObjectType:         "website",
				Effect:             "published",
				SuggestedNextTools: []string{"site.promote"},
			}},
			OperationContract: &OperationContract{
				Version: 1,
				Requirements: []OperationRequirement{{
					RequirementID: "operation-1",
					ToolID:        "capabilityd:site.publish",
					ToolName:      "site.promote",
					InputMode:     OperationInputNoExplicitValues,
					RequiredInput: json.RawMessage(`{}`),
				}},
			},
		},
	}

	normalizedGoal := normalizePersistedActiveGoal(activeGoal)

	assertSameStrings(t, normalizedGoal.SelectedToolNames, []string{TerminalRunToolName, "site.publish"})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceTools, []string{FileDeliverToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceAnyOf[0], []string{AskInputToolName, TerminalRunToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.SelectedEvidenceHints, []string{"site.publish"})
	assertSameStrings(t, normalizedGoal.OutcomeContract.ExpectedResults[0].AcceptanceHints, []string{AskInputToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEffects[0].SuggestedNextTools, []string{"site.publish"})
	if normalizedGoal.OutcomeContract.OperationContract.Requirements[0].ToolName != "site.publish" {
		t.Fatalf("expected persisted operation tool name to migrate, got %+v", normalizedGoal.OutcomeContract.OperationContract.Requirements[0])
	}
}

func TestNormalizePersistedActiveGoalDoesNotMutateSource(t *testing.T) {
	activeGoal := ActiveGoal{
		SelectedToolNames: []string{"file.attach"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.promote"},
			OperationContract: &OperationContract{Requirements: []OperationRequirement{{
				ToolName: "terminal.session",
			}}},
		},
	}

	normalizePersistedActiveGoal(activeGoal)

	assertSameStrings(t, activeGoal.SelectedToolNames, []string{"file.attach"})
	assertSameStrings(t, activeGoal.OutcomeContract.RequiredEvidenceTools, []string{"site.promote"})
	if activeGoal.OutcomeContract.OperationContract.Requirements[0].ToolName != "terminal.session" {
		t.Fatalf("expected source operation contract to remain unchanged, got %+v", activeGoal.OutcomeContract.OperationContract)
	}
}

func assertSameStrings(t *testing.T, actual []string, expected []string) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("expected %+v, got %+v", expected, actual)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("expected %+v, got %+v", expected, actual)
		}
	}
}
