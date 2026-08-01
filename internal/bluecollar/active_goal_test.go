package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"testing"
)

func TestNormalizePersistedActiveGoalMigratesLegacyToolNames(t *testing.T) {
	activeGoal := ActiveGoal{
		RequiredNextTools: []string{"terminal.session", "site.promote"},
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
		},
	}

	normalizedGoal := normalizePersistedActiveGoal(activeGoal)

	assertSameStrings(t, normalizedGoal.RequiredNextTools, []string{toolcontract.TerminalRunToolName, "site.serve"})
	assertSameStrings(t, normalizedGoal.SelectedToolNames, []string{toolcontract.TerminalRunToolName, "site.serve"})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceTools, []string{toolcontract.FileDeliverToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceAnyOf[0], []string{toolcontract.AskInputToolName, toolcontract.TerminalRunToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.SelectedEvidenceHints, []string{"site.serve"})
	assertSameStrings(t, normalizedGoal.OutcomeContract.ExpectedResults[0].AcceptanceHints, []string{toolcontract.AskInputToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEffects[0].SuggestedNextTools, []string{"site.serve"})
}

func TestNormalizePersistedActiveGoalDoesNotMutateSource(t *testing.T) {
	activeGoal := ActiveGoal{
		RequiredNextTools: []string{"site.promote"},
		SelectedToolNames: []string{"file.attach"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site.promote"},
		},
	}

	normalizePersistedActiveGoal(activeGoal)

	assertSameStrings(t, activeGoal.RequiredNextTools, []string{"site.promote"})
	assertSameStrings(t, activeGoal.SelectedToolNames, []string{"file.attach"})
	assertSameStrings(t, activeGoal.OutcomeContract.RequiredEvidenceTools, []string{"site.promote"})
}

func TestNormalizeOutcomeContractRequiresDeliveryForRequiredFileResult(t *testing.T) {
	contract := normalizeOutcomeContract(OutcomeContract{
		RequiredEvidenceTools: []string{"file.write"},
		ExpectedResults: []ExpectedResult{{
			Type:        ExpectedResultTypeFile,
			Description: "attached report",
			Required:    true,
		}},
	})

	assertSameStrings(t, contract.RequiredEvidenceTools, []string{"file.write", toolcontract.FileDeliverToolName})
	if contract.ArtifactRequirement != ArtifactRequirementRequired {
		t.Fatalf("expected required artifact, got %q", contract.ArtifactRequirement)
	}
}

func TestNormalizePersistedActiveGoalRestoresFileDeliveryInvariant(t *testing.T) {
	activeGoal := normalizePersistedActiveGoal(ActiveGoal{OutcomeContract: OutcomeContract{
		ExpectedResults: []ExpectedResult{{
			Type:        ExpectedResultTypeFile,
			Description: "attached report",
			Required:    true,
		}},
	}})

	assertSameStrings(t, activeGoal.OutcomeContract.RequiredEvidenceTools, []string{toolcontract.FileDeliverToolName})
	if activeGoal.OutcomeContract.ArtifactRequirement != ArtifactRequirementRequired {
		t.Fatalf("expected persisted required artifact, got %q", activeGoal.OutcomeContract.ArtifactRequirement)
	}
}

func TestNormalizeOutcomeContractDoesNotAddDeliveryForMessageResult(t *testing.T) {
	contract := normalizeOutcomeContract(OutcomeContract{ExpectedResults: []ExpectedResult{{
		Type:        ExpectedResultTypeMessage,
		Description: "final reply",
		Required:    true,
	}}})

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no delivery requirement, got %+v", contract.RequiredEvidenceTools)
	}
	if contract.ArtifactRequirement != ArtifactRequirementNone {
		t.Fatalf("expected no artifact requirement, got %q", contract.ArtifactRequirement)
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
