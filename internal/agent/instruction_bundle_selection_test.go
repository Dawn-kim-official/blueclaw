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

func TestSelectedSkillRequirementsPreserveNonBoundedDecision(t *testing.T) {
	for _, classification := range []IntakeClassification{IntakeClassificationQuickReply, IntakeClassificationNeedsConfirmation, IntakeClassificationUnsupported} {
		decision := IntakeDecision{Classification: classification, TaskShape: TaskShapeImmediateReply, TaskLevel: TaskLevelXLow}
		result := applySelectedSkillCompletionRequirements(decision, namedSkillBundle("presentation"))
		if !reflect.DeepEqual(result, decision) {
			t.Fatalf("expected router decision %q to remain unchanged, got %+v", classification, result)
		}
	}
}
