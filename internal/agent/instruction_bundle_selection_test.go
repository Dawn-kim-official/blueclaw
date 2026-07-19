package agent

import (
	"reflect"
	"testing"
)

func namedSkillBundle(skillName string) InstructionBundle {
	return InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           skillName,
			ToolReferences: []string{"file.deliver"},
		}},
		SkillDecisions:              []SkillSelectionDecision{{Name: skillName, Status: "selected"}},
		RequiredEvidenceTools:       []string{"file.deliver"},
		HasContractSkillArbitration: true,
	}
}

func TestInstructionBundleRequirementsApplyArbitratedEvidence(t *testing.T) {
	decision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskLevel: TaskLevelLow}
	result := applyInstructionBundleRequirements(decision, namedSkillBundle("presentation"))

	if !reflect.DeepEqual(result.RequiredEvidenceTools, []string{"file.deliver"}) {
		t.Fatalf("expected completion evidence, got %v", result.RequiredEvidenceTools)
	}
	if result.TaskLevel != TaskLevelLow {
		t.Fatalf("expected router task level to remain authoritative, got %q", result.TaskLevel)
	}
}

func TestSelectedSkillRequirementsUseArbitratedEvidence(t *testing.T) {
	decision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		RequiredEvidenceTools: []string{"file.edit", "task.update"},
	}
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "internkim-flow",
			ToolReferences: []string{"task.add", "task.list", "task.update", "task.delete"},
		}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredEvidenceTools:       []string{"task.update"},
		HasContractSkillArbitration: true,
	}

	result := applyInstructionBundleRequirements(decision, instructionBundle)

	if !reflect.DeepEqual(result.RequiredEvidenceTools, []string{"task.update"}) {
		t.Fatalf("expected arbitration evidence to replace router evidence, got %v", result.RequiredEvidenceTools)
	}
}

func TestSelectedSkillRequirementsPreserveRouterEvidenceWithoutArbitration(t *testing.T) {
	decision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		RequiredEvidenceTools: []string{"file.edit"},
	}
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task.update"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}

	result := applyInstructionBundleRequirements(decision, instructionBundle)

	if !reflect.DeepEqual(result.RequiredEvidenceTools, []string{"file.edit"}) {
		t.Fatalf("expected explicit router evidence to remain exact, got %v", result.RequiredEvidenceTools)
	}
}

func TestSelectedSkillRequirementsDoNotRetainRouterEvidenceAfterArbitration(t *testing.T) {
	decision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		RequiredEvidenceTools: []string{"task.add"},
	}
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task.add"}}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		HasContractSkillArbitration: true,
	}

	result := applyInstructionBundleRequirements(decision, instructionBundle)

	if len(result.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected arbitration evidence to remain authoritative, got %v", result.RequiredEvidenceTools)
	}
}

func TestFailedContractSkillArbitrationClearsRouterEvidence(t *testing.T) {
	decision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		RequiredEvidenceTools: []string{"file.write", "file.deliver"},
	}
	instructionBundle := InstructionBundle{ContractSkillArbitrationFailed: true}

	result := applyInstructionBundleRequirements(decision, instructionBundle)

	if len(result.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected failed arbitration to clear untrusted router evidence, got %v", result.RequiredEvidenceTools)
	}
}

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

func TestSelectedSkillRequirementsPreserveNonBoundedDecision(t *testing.T) {
	for _, classification := range []IntakeClassification{IntakeClassificationQuickReply, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported} {
		decision := IntakeDecision{Classification: classification, TaskShape: TaskShapeImmediateReply, TaskLevel: TaskLevelXLow}
		result := applyInstructionBundleRequirements(decision, namedSkillBundle("presentation"))
		if !reflect.DeepEqual(result, decision) {
			t.Fatalf("expected router decision %q to remain unchanged, got %+v", classification, result)
		}
	}
}
