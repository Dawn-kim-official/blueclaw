package agent

import (
	"reflect"
	"testing"
)

func TestContractEvidenceUsesOnlySelectedRegisteredTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{
		Name:           "internkim-flow",
		ToolReferences: []string{"task.add", "task.list", "task.update", "task.delete"},
	}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.update", "file.edit"})}

	result := validatedContractEvidenceTools(contractSkillArbitration{
		ExpectedEvidence:  []string{"file.edit", "task.update", "task.delete"},
		RequiredNextTools: []string{"task.list", "task.update"},
	}, selectedSkills, request)

	if !reflect.DeepEqual(result, []string{"task.update"}) {
		t.Fatalf("expected selected registered evidence only, got %v", result)
	}
}

func TestContractNextToolsUseOnlySelectedRegisteredTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{
		Name:           "internkim-flow",
		ToolReferences: []string{"task.add", "task.list", "task.update", "task.delete"},
	}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.add", "task.update", "file.edit"})}

	result := validatedContractNextTools(contractSkillArbitration{
		RequiredNextTools: []string{"file.edit", "task.add", "task.update", "task.delete", "unknown.operation"},
	}, selectedSkills, request)

	if !reflect.DeepEqual(result, []string{"file.edit", "task.add", "task.update"}) {
		t.Fatalf("expected registered kernel and selected next tools only, got %v", result)
	}
}

func TestContractEvidenceDoesNotPromoteRequiredNextTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task.update"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.update"})}
	arbitration := contractSkillArbitration{
		ExpectedEvidence:  []string{"unknown.operation"},
		RequiredNextTools: []string{"task.update"},
	}

	result := validatedContractEvidenceTools(arbitration, selectedSkills, request)

	if len(result) != 0 {
		t.Fatalf("expected next tools to remain execution hints, got evidence %v", result)
	}
}

func TestContractEvidenceRejectsReadForSideEffectContract(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task.list", "task.update"}}}
	request := AgentRequest{
		ToolSet: newTestToolSet([]string{"file.edit", "task.list", "task.update"}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file.edit"},
		}},
	}

	result := validatedContractEvidenceTools(contractSkillArbitration{
		ExpectedEvidence: []string{"task.list"},
	}, selectedSkills, request)

	if len(result) != 0 {
		t.Fatalf("expected read evidence to be rejected for a side-effect contract, got %v", result)
	}
}

func TestContractEvidenceRejectsReadWhenNextToolChangesState(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task.list", "task.update"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.list", "task.update"})}
	arbitration := contractSkillArbitration{
		ExpectedEvidence:  []string{"task.list"},
		RequiredNextTools: []string{"task.update"},
	}

	result := validatedContractEvidenceTools(arbitration, selectedSkills, request)
	nextTools := validatedContractNextTools(arbitration, selectedSkills, request)

	if len(result) != 0 || !reflect.DeepEqual(nextTools, []string{"task.update"}) {
		t.Fatalf("expected next tools to remain separate from evidence, got evidence=%v next=%v", result, nextTools)
	}
}

func TestInstructionBundleWithToolOwningSkillsSelectsMissedOwner(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "web-search", ToolReferences: []string{"web.search"}},
			{Name: "website", ToolReferences: []string{"site.create", "site.preview"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "web-search", Status: "selected", Reason: "embedding_similarity"}},
	}

	amendedBundle := instructionBundleWithToolOwningSkills(instructionBundle, AgentRequest{}, []string{"site.create", "file.edit"})

	if !selectedSkillNames(amendedBundle.SkillDecisions)["website"] {
		t.Fatalf("expected the skill owning a suggested tool to be selected, got %+v", amendedBundle.SkillDecisions)
	}
	unchangedBundle := instructionBundleWithToolOwningSkills(instructionBundle, AgentRequest{}, []string{"web.search"})
	if selectedSkillNames(unchangedBundle.SkillDecisions)["website"] {
		t.Fatalf("expected no owner selection without a suggested tool match, got %+v", unchangedBundle.SkillDecisions)
	}
}
