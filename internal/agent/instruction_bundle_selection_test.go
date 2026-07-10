package agent

import "testing"

func presentationSkillBundle() InstructionBundle {
	return InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "presentation",
			Description:  "Create presentation slides.",
			WhenToUse:    "Use for slide deck requests.",
			Prompt:       "Create and attach deck files.",
			AllowedTools: []string{"terminal.run", "file.write", "file.deliver"},
			Completion:   SkillCompletion{RequiredEvidenceTools: []string{"file.deliver"}},
			Source:       InstructionSource{Path: "skills/presentation/SKILL.md", SkillName: "presentation"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}
}

func TestSkillTaskLevelFloorRaisesBoundedTaskLevel(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeResearchTask,
		TaskLevel:      TaskLevelLow,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, presentationSkillBundle(), IntakeOptions{
		DefaultTaskLevel:    TaskLevelLow,
		SkillTaskLevelFloor: TaskLevelMedium,
	})
	if promoted.TaskLevel != TaskLevelMedium {
		t.Fatalf("expected medium task level for evidence-requiring skill, got %q", promoted.TaskLevel)
	}
}

func TestSkillTaskLevelFloorKeepsHigherTaskLevel(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskLevel:      TaskLevelHigh,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, presentationSkillBundle(), IntakeOptions{
		SkillTaskLevelFloor: TaskLevelMedium,
	})
	if promoted.TaskLevel != TaskLevelHigh {
		t.Fatalf("expected high task level to be kept, got %q", promoted.TaskLevel)
	}
}

func TestSkillTaskLevelFloorIgnoresSkillsWithoutCompletionContract(t *testing.T) {
	bundle := presentationSkillBundle()
	bundle.Skills[0].Completion = SkillCompletion{}
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskLevel:      TaskLevelLow,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, bundle, IntakeOptions{
		SkillTaskLevelFloor: TaskLevelMedium,
	})
	if promoted.TaskLevel != TaskLevelLow {
		t.Fatalf("expected low task level without completion contract, got %q", promoted.TaskLevel)
	}
}

func TestSkillTaskLevelFloorDisabledWhenUnset(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskLevel:      TaskLevelLow,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, presentationSkillBundle(), IntakeOptions{})
	if promoted.TaskLevel != TaskLevelLow {
		t.Fatalf("expected low task level without a floor, got %q", promoted.TaskLevel)
	}
}
