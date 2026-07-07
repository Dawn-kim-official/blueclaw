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

func TestSkillEffortFloorRaisesBoundedTaskEffort(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeResearchTask,
		EffortLevel:    EffortLevelStandard,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, presentationSkillBundle(), IntakeOptions{
		DefaultEffortLevel: EffortLevelStandard,
		SkillEffortFloor:   EffortLevelDeep,
	})
	if promoted.EffortLevel != EffortLevelDeep {
		t.Fatalf("expected deep effort for evidence-requiring skill, got %q", promoted.EffortLevel)
	}
}

func TestSkillEffortFloorKeepsHigherModelEffort(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		EffortLevel:    EffortLevelExtended,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, presentationSkillBundle(), IntakeOptions{
		SkillEffortFloor: EffortLevelDeep,
	})
	if promoted.EffortLevel != EffortLevelExtended {
		t.Fatalf("expected extended effort to be kept, got %q", promoted.EffortLevel)
	}
}

func TestSkillEffortFloorIgnoresSkillsWithoutCompletionContract(t *testing.T) {
	bundle := presentationSkillBundle()
	bundle.Skills[0].Completion = SkillCompletion{}
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		EffortLevel:    EffortLevelStandard,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, bundle, IntakeOptions{
		SkillEffortFloor: EffortLevelDeep,
	})
	if promoted.EffortLevel != EffortLevelStandard {
		t.Fatalf("expected standard effort without completion contract, got %q", promoted.EffortLevel)
	}
}

func TestSkillEffortFloorDisabledWhenUnset(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		EffortLevel:    EffortLevelStandard,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, presentationSkillBundle(), IntakeOptions{})
	if promoted.EffortLevel != EffortLevelStandard {
		t.Fatalf("expected standard effort without a floor, got %q", promoted.EffortLevel)
	}
}
