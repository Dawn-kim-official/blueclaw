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

