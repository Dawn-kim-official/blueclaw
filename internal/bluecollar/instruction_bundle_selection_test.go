package bluecollar

import (
	"reflect"
	"testing"
)

func TestContractEvidenceUsesOnlySelectedRegisteredTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{
		Name:           "internkim-flow",
		ToolReferences: []string{"task_add", "task_list", "task_update", "task_delete"},
	}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task_update", "file_edit"})}

	result := validatedContractEvidenceTools(contractSkillArbitration{
		ExpectedEvidence:  []string{"file_edit", "task_update", "task_delete"},
		RequiredNextTools: []string{"task_list", "task_update"},
	}, selectedSkills, request)

	if !reflect.DeepEqual(result, []string{"task_update"}) {
		t.Fatalf("expected selected registered evidence only, got %v", result)
	}
}

func TestContractNextToolsUseOnlySelectedRegisteredTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{
		Name:           "internkim-flow",
		ToolReferences: []string{"task_add", "task_list", "task_update", "task_delete"},
	}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task_add", "task_update", "file_edit"})}

	result := validatedContractNextTools(contractSkillArbitration{
		RequiredNextTools: []string{"file_edit", "task_add", "task_update", "task_delete", "unknown.operation"},
	}, selectedSkills, request)

	if !reflect.DeepEqual(result, []string{"file_edit", "task_add", "task_update"}) {
		t.Fatalf("expected registered kernel and selected next tools only, got %v", result)
	}
}

func TestContractEvidenceDoesNotPromoteRequiredNextTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task_update"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task_update"})}
	arbitration := contractSkillArbitration{
		ExpectedEvidence:  []string{"unknown.operation"},
		RequiredNextTools: []string{"task_update"},
	}

	result := validatedContractEvidenceTools(arbitration, selectedSkills, request)

	if len(result) != 0 {
		t.Fatalf("expected next tools to remain execution hints, got evidence %v", result)
	}
}

func TestContractEvidenceRejectsReadForSideEffectContract(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task_list", "task_update"}}}
	request := AgentRequest{
		ToolSet: newTestToolSet([]string{"file_edit", "task_list", "task_update"}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file_edit"},
		}},
	}

	result := validatedContractEvidenceTools(contractSkillArbitration{
		ExpectedEvidence: []string{"task_list"},
	}, selectedSkills, request)

	if len(result) != 0 {
		t.Fatalf("expected read evidence to be rejected for a side-effect contract, got %v", result)
	}
}

func TestContractEvidenceRejectsReadWhenNextToolChangesState(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task_list", "task_update"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task_list", "task_update"})}
	arbitration := contractSkillArbitration{
		ExpectedEvidence:  []string{"task_list"},
		RequiredNextTools: []string{"task_update"},
	}

	result := validatedContractEvidenceTools(arbitration, selectedSkills, request)
	nextTools := validatedContractNextTools(arbitration, selectedSkills, request)

	if len(result) != 0 || !reflect.DeepEqual(nextTools, []string{"task_update"}) {
		t.Fatalf("expected next tools to remain separate from evidence, got evidence=%v next=%v", result, nextTools)
	}
}

func TestInstructionBundleWithToolOwningSkillsSelectsMissedOwner(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "web-search", ToolReferences: []string{"web_search"}},
			{Name: "website", ToolReferences: []string{"site_serve", "site_serve"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "web-search", Status: "selected", Reason: "embedding_similarity"}},
	}

	amendedBundle := instructionBundleWithToolOwningSkills(instructionBundle, AgentRequest{}, []string{"site_serve", "file_edit"})

	if !selectedSkillNames(amendedBundle.SkillDecisions)["website"] {
		t.Fatalf("expected the skill owning a suggested tool to be selected, got %+v", amendedBundle.SkillDecisions)
	}
	unchangedBundle := instructionBundleWithToolOwningSkills(instructionBundle, AgentRequest{}, []string{"web_search"})
	if selectedSkillNames(unchangedBundle.SkillDecisions)["website"] {
		t.Fatalf("expected no owner selection without a suggested tool match, got %+v", unchangedBundle.SkillDecisions)
	}
}
