package agent

import "testing"

func TestToolPalettePlanKeepsRequiredEvidenceToolUnderCap(t *testing.T) {
	toolSet := newTestToolSet([]string{
		"generic.one",
		"generic.two",
		"generic.three",
		"file.attach",
	})
	contract := normalizeExecutionContract(ExecutionContract{
		ToolPolicy: ToolPolicy{
			RequiredToolNames: []string{"file.attach"},
			MaxCallableTools:  2,
		},
	})

	plan, _ := PlanToolPalette(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "파일 첨부해줘"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		contract,
		ToolSelectionDecision{},
		ToolExposureEvent{},
	)

	if !containsString(plan.ExposedToolNames, "file.attach") {
		t.Fatalf("expected required evidence tool to be exposed under cap, got %+v", plan)
	}
}

func TestToolPalettePlanPrioritizesRecoveryTool(t *testing.T) {
	toolSet := newTestToolSet([]string{
		"backup.lookup",
		"generic.one",
		"generic.two",
	})
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "generic.one",
		ToolInputKey:  "generic.one:{}",
		Failure: &ToolFailure{
			Kind:            FailureExternalService,
			Code:            FailureCodes.OperationFailed.String(),
			Stage:           "test",
			UserSafeSummary: "failed",
		},
		RecoveryPacket: &RecoveryPacket{AllowedTools: []string{"backup.lookup"}},
	}}

	plan, _ := PlanToolPalette(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "다른 방법으로 찾아봐"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ExecutionContract{},
		ToolSelectionDecision{},
		ToolExposureEvent{},
		observations,
	)

	if !containsString(plan.ExposedToolNames, "backup.lookup") {
		t.Fatalf("expected recovery tool to be exposed, got %+v", plan)
	}
	if len(plan.Candidates) == 0 || plan.Candidates[0].Source != "G4 recovery/pinned candidates" {
		t.Fatalf("expected recovery candidate to lead candidates, got %+v", plan.Candidates)
	}
}

func TestToolPalettePlanRecordsDroppedToolReason(t *testing.T) {
	toolSet := newTestToolSet([]string{
		"file.attach",
		"site.app.publish",
		"generic.one",
	})
	contract := normalizeExecutionContract(ExecutionContract{
		ToolPolicy: ToolPolicy{
			RequiredToolNames: []string{"file.attach"},
			HintToolNames:     []string{"site.app.publish"},
			MaxCallableTools:  1,
		},
	})

	plan, _ := PlanToolPalette(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "사이트 배포하고 파일도 첨부해줘"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		contract,
		ToolSelectionDecision{},
		ToolExposureEvent{},
	)

	if len(plan.DroppedTools) == 0 {
		t.Fatalf("expected dropped tool metadata, got %+v", plan)
	}
	if plan.DroppedTools[0].Reason == "" || plan.DroppedTools[0].Source == "" {
		t.Fatalf("expected dropped tool source and reason, got %+v", plan.DroppedTools[0])
	}
}

func TestToolPalettePlanSuppressesConfirmationForReadOnlyWebLookup(t *testing.T) {
	toolSet := newTestToolSet([]string{
		"ask.confirm",
		"user.confirm",
		"skill.search",
		"web.search",
		"memory.search",
	})
	outcomeContract := OutcomeContract{RequiredEvidenceTools: []string{"web.search"}}
	executionContract := normalizeExecutionContract(ExecutionContract{
		ToolPolicy: ToolPolicy{
			RequiredToolNames: []string{"web.search"},
			MaxCallableTools:  4,
		},
	})

	plan, _ := PlanToolPalette(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "검색해봐"},
		ExecutionPlan{},
		false,
		outcomeContract,
		executionContract,
		ToolSelectionDecision{SelectedToolIDs: []string{"ask.confirm", "user.confirm"}},
		ToolExposureEvent{},
	)

	if !containsString(plan.ExposedToolNames, "web.search") {
		t.Fatalf("expected web.search to be exposed, got %+v", plan)
	}
	if len(plan.ExposedToolNames) != 1 {
		t.Fatalf("expected read-only web lookup to expose only web.search, got %+v", plan)
	}
	if containsString(plan.ExposedToolNames, "ask.confirm") {
		t.Fatalf("expected ask.confirm to be hidden for read-only web lookup, got %+v", plan)
	}
	if containsString(plan.ExposedToolNames, "user.confirm") {
		t.Fatalf("expected user.confirm to be hidden for read-only web lookup, got %+v", plan)
	}
}
