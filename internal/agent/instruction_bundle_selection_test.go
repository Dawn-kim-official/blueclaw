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

func TestSelectedSkillRequirementsKeepOnlyOwnedRouterEvidence(t *testing.T) {
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
		t.Fatalf("expected selected skill to own completion evidence, got %v", result.RequiredEvidenceTools)
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

func TestSelectedSkillRequirementsPreserveNonBoundedDecision(t *testing.T) {
	for _, classification := range []IntakeClassification{IntakeClassificationQuickReply, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported} {
		decision := IntakeDecision{Classification: classification, TaskShape: TaskShapeImmediateReply, TaskLevel: TaskLevelXLow}
		result := applySelectedSkillCompletionRequirements(decision, namedSkillBundle("presentation"))
		if !reflect.DeepEqual(result, decision) {
			t.Fatalf("expected router decision %q to remain unchanged, got %+v", classification, result)
		}
	}
}
