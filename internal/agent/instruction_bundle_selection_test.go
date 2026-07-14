package agent

import "testing"

func namedSkillBundle(skillName string) InstructionBundle {
	return InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         skillName,
			Description:  "Test skill.",
			WhenToUse:    "Use for test requests.",
			Prompt:       "Do the work and attach files.",
			AllowedTools: []string{"terminal.run", "file.write", "file.deliver"},
			Completion:   SkillCompletion{RequiredEvidenceTools: []string{"file.deliver"}},
			Source:       InstructionSource{Path: "skills/" + skillName + "/SKILL.md", SkillName: skillName},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: skillName, Status: "selected"}},
	}
}

func TestSelectedSkillRecommendedMinutesRaisesEstimate(t *testing.T) {
	bundle := namedSkillBundle("presentation")
	bundle.Skills[0].RecommendedMinutes = 60
	promoted := promoteIntakeDecisionForSelectedSkills(
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskLevel: TaskLevelLow, EstimatedMinutes: 2},
		bundle,
		IntakeOptions{},
	)
	if promoted.EstimatedMinutes != 60 {
		t.Fatalf("expected selected skill estimate floor, got %d", promoted.EstimatedMinutes)
	}
}

func TestSkillTaskLevelFloorRaisesBoundedTaskLevel(t *testing.T) {
	decision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeResearchTask,
		TaskLevel:      TaskLevelLow,
	}
	promoted := promoteIntakeDecisionForSelectedSkills(decision, namedSkillBundle("spreadsheet"), IntakeOptions{
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
	promoted := promoteIntakeDecisionForSelectedSkills(decision, namedSkillBundle("spreadsheet"), IntakeOptions{
		SkillTaskLevelFloor: TaskLevelMedium,
	})
	if promoted.TaskLevel != TaskLevelHigh {
		t.Fatalf("expected high task level to be kept, got %q", promoted.TaskLevel)
	}
}

func TestSkillTaskLevelFloorIgnoresSkillsWithoutCompletionContract(t *testing.T) {
	bundle := namedSkillBundle("spreadsheet")
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
	promoted := promoteIntakeDecisionForSelectedSkills(decision, namedSkillBundle("spreadsheet"), IntakeOptions{})
	if promoted.TaskLevel != TaskLevelLow {
		t.Fatalf("expected low task level without a floor, got %q", promoted.TaskLevel)
	}
}

func TestBM25FallbackSkillDoesNotPromoteQuickReply(t *testing.T) {
	bundle := namedSkillBundle("direct-message")
	bundle.SkillDecisions[0].Reason = "bm25_fallback"
	decision := IntakeDecision{
		Classification: IntakeClassificationQuickReply,
		TaskShape:      TaskShapeImmediateReply,
		TaskLevel:      TaskLevelXLow,
	}

	result := promoteIntakeDecisionForSelectedSkills(decision, bundle, IntakeOptions{
		DefaultTaskLevel:    TaskLevelHigh,
		SkillTaskLevelFloor: TaskLevelHigh,
	})

	if result.Classification != IntakeClassificationQuickReply || result.TaskShape != TaskShapeImmediateReply || result.TaskLevel != TaskLevelXLow {
		t.Fatalf("expected BM25 fallback skill to preserve quick reply, got %+v", result)
	}
	if len(result.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no promoted evidence tools, got %v", result.RequiredEvidenceTools)
	}
}

// Presentation and website work floors at xhigh whatever the output format
// (PPTX, PDF, HTML), driven by which skill was selected rather than an output
// suffix — even when the generic skill floor is lower or unset.
func TestVisualDeliverableSkillsFloorAtXHigh(t *testing.T) {
	for _, skillName := range []string{"presentation", "website"} {
		belowFloor := promoteIntakeDecisionForSelectedSkills(
			IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskLevel: TaskLevelLow},
			namedSkillBundle(skillName),
			IntakeOptions{DefaultTaskLevel: TaskLevelLow, SkillTaskLevelFloor: TaskLevelHigh},
		)
		if belowFloor.TaskLevel != TaskLevelXHigh {
			t.Fatalf("%s with a high generic floor: expected xhigh, got %q", skillName, belowFloor.TaskLevel)
		}

		noGenericFloor := promoteIntakeDecisionForSelectedSkills(
			IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskLevel: TaskLevelLow},
			namedSkillBundle(skillName),
			IntakeOptions{DefaultTaskLevel: TaskLevelLow},
		)
		if noGenericFloor.TaskLevel != TaskLevelXHigh {
			t.Fatalf("%s with no generic floor: expected xhigh, got %q", skillName, noGenericFloor.TaskLevel)
		}
	}
}
