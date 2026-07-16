package agent

import (
	"reflect"
	"testing"
)

func namedSkillBundle(skillName string) InstructionBundle {
	return InstructionBundle{
		Skills: []SkillInstruction{{
			Name:       skillName,
			Completion: SkillCompletion{RequiredEvidenceTools: []string{"file.deliver"}, RequiredAttachmentSuffixes: []string{".pdf"}},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: skillName, Status: "selected"}},
	}
}

func TestSelectedSkillRequirementsEnrichBoundedTask(t *testing.T) {
	decision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskLevel: TaskLevelLow}
	result := applySelectedSkillCompletionRequirements(decision, namedSkillBundle("presentation"))

	if !reflect.DeepEqual(result.RequiredEvidenceTools, []string{"file.deliver"}) {
		t.Fatalf("expected completion evidence, got %v", result.RequiredEvidenceTools)
	}
	if !reflect.DeepEqual(result.RequestedOutputFormats, []string{"pdf"}) {
		t.Fatalf("expected requested output format, got %v", result.RequestedOutputFormats)
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
			Name:         "internkim-flow",
			AllowedTools: []string{"task.add", "task.list", "task.update", "task.delete"},
		}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredEvidenceTools:       []string{"task.update"},
		HasContractSkillArbitration: true,
	}

	result := applySelectedSkillCompletionRequirements(decision, instructionBundle)

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
		Skills:         []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.update"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}

	result := applySelectedSkillCompletionRequirements(decision, instructionBundle)

	if !reflect.DeepEqual(result.RequiredEvidenceTools, []string{"file.edit"}) {
		t.Fatalf("expected explicit router evidence to remain exact, got %v", result.RequiredEvidenceTools)
	}
}

func TestContractEvidenceUsesOnlySelectedRegisteredTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{
		Name:         "internkim-flow",
		AllowedTools: []string{"task.add", "task.list", "task.update", "task.delete"},
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

func TestContractEvidenceDoesNotPromoteRequiredNextTools(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.update"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.update"})}

	result := validatedContractEvidenceTools(contractSkillArbitration{
		ExpectedEvidence:  []string{"unknown.operation"},
		RequiredNextTools: []string{"task.update"},
	}, selectedSkills, request)

	if len(result) != 0 {
		t.Fatalf("expected next tools to remain execution hints, got evidence %v", result)
	}
}

func TestContractEvidenceRejectsReadForSideEffectContract(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.list", "task.update"}}}
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

func TestSelectedSkillRequirementsPreserveNonBoundedDecision(t *testing.T) {
	for _, classification := range []IntakeClassification{IntakeClassificationQuickReply, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported} {
		decision := IntakeDecision{Classification: classification, TaskShape: TaskShapeImmediateReply, TaskLevel: TaskLevelXLow}
		result := applySelectedSkillCompletionRequirements(decision, namedSkillBundle("presentation"))
		if !reflect.DeepEqual(result, decision) {
			t.Fatalf("expected router decision %q to remain unchanged, got %+v", classification, result)
		}
	}
}
