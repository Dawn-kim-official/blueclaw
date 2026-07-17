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
			Completion:   SkillCompletion{RequiredEvidenceTools: []string{"file.deliver"}},
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

func TestSelectedSkillRequirementsPreserveRouterEvidenceWhenArbitrationHasNoEvidence(t *testing.T) {
	decision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		RequiredEvidenceTools: []string{"task.add"},
	}
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.add"}}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		HasContractSkillArbitration: true,
	}

	result := applySelectedSkillCompletionRequirements(decision, instructionBundle)

	if !reflect.DeepEqual(result.RequiredEvidenceTools, []string{"task.add"}) {
		t.Fatalf("expected explicit router evidence to survive empty arbitration, got %v", result.RequiredEvidenceTools)
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
	arbitration := contractSkillArbitration{
		ExpectedEvidence:  []string{"unknown.operation"},
		RequiredNextTools: []string{"task.update"},
	}

	result := validatedContractEvidenceTools(arbitration, selectedSkills, request)

	if len(result) != 0 {
		t.Fatalf("expected next tools to remain execution hints, got evidence %v", result)
	}
	candidates := unresolvedContractEvidenceCandidates(arbitration, selectedSkills, request, result)
	if !reflect.DeepEqual(candidates, []string{"task.update"}) {
		t.Fatalf("expected typed side-effect candidate for evidence re-ask, got %v", candidates)
	}
}

func TestContractEvidenceDoesNotReaskForReadOnlyNextTool(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.list"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.list"})}
	arbitration := contractSkillArbitration{RequiredNextTools: []string{"task.list"}}

	candidates := unresolvedContractEvidenceCandidates(arbitration, selectedSkills, request, nil)

	if len(candidates) != 0 {
		t.Fatalf("expected read-only next tool not to trigger evidence re-ask, got %v", candidates)
	}
}

func TestContractEvidenceDoesNotReaskForExactActiveEvidence(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.delete"}}}
	request := AgentRequest{
		ToolSet: newTestToolSet([]string{"task.delete"}),
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task.delete"},
		}},
	}
	arbitration := contractSkillArbitration{RequiredNextTools: []string{"task.delete"}}

	candidates := unresolvedContractEvidenceCandidates(arbitration, selectedSkills, request, nil)

	if len(candidates) != 0 {
		t.Fatalf("expected exact active evidence to resolve the candidate, got %v", candidates)
	}
}

func TestContractEvidenceReasksForDifferentActiveEvidence(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.update"}}}
	for _, requiredEvidence := range []string{"task.list", "file.edit"} {
		request := AgentRequest{
			ToolSet: newTestToolSet([]string{"task.list", "task.update", "file.edit"}),
			ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{requiredEvidence},
			}},
		}
		arbitration := contractSkillArbitration{RequiredNextTools: []string{"task.update"}}

		candidates := unresolvedContractEvidenceCandidates(arbitration, selectedSkills, request, nil)

		if !reflect.DeepEqual(candidates, []string{"task.update"}) {
			t.Fatalf("expected %s not to satisfy task.update, got %v", requiredEvidence, candidates)
		}
	}
}

func TestContractEvidenceReasksForUnavailableActiveEvidence(t *testing.T) {
	toolSet := newTestToolSet([]string{"task.delete"})
	boundTool := toolSet.boundToolByName["task.delete"]
	boundTool.Availability = ToolAvailability{Status: ToolAvailabilityDenied}
	toolSet.boundToolByName["task.delete"] = boundTool
	request := AgentRequest{
		ToolSet: toolSet,
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task.delete"},
		}},
	}

	if activeContractRequiresTool(request, "task.delete") {
		t.Fatal("expected unavailable evidence not to resolve a candidate")
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

func TestContractEvidenceRejectsReadWhenNextToolChangesState(t *testing.T) {
	selectedSkills := []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.list", "task.update"}}}
	request := AgentRequest{ToolSet: newTestToolSet([]string{"task.list", "task.update"})}
	arbitration := contractSkillArbitration{
		ExpectedEvidence:  []string{"task.list"},
		RequiredNextTools: []string{"task.update"},
	}

	result := validatedContractEvidenceTools(arbitration, selectedSkills, request)
	candidates := unresolvedContractEvidenceCandidates(arbitration, selectedSkills, request, result)

	if len(result) != 0 || !reflect.DeepEqual(candidates, []string{"task.update"}) {
		t.Fatalf("expected state-changing next tool to force exact evidence re-ask, got evidence=%v candidates=%v", result, candidates)
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
