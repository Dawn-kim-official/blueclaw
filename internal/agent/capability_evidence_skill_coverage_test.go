package agent

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/task"
)

func webSearchTestSkillInstruction() SkillInstruction {
	return SkillInstruction{
		Name:         "web-search",
		Description:  "Search the public web.",
		WhenToUse:    "Use when the answer needs external or current information.",
		Prompt:       "Call web.search to search the public web and web.fetch to retrieve URLs.",
		AllowedTools: []string{"web.search", "web.fetch"},
		Completion:   SkillCompletion{RequiredEvidenceTools: []string{"web.search"}},
	}
}

func TestCapabilityEvidenceSkillCoverageFindsDocumentingSkill(t *testing.T) {
	toolSet := newHiddenTestCapabilityToolSet([]string{"web.search"})
	skillInstructions := []SkillInstruction{webSearchTestSkillInstruction()}

	coveringSkillNames, undocumentedOperationNames := requiredEvidenceSkillCoverage(toolSet, skillInstructions, []string{"web.search"})

	if len(undocumentedOperationNames) != 0 {
		t.Fatalf("expected web.search to be documented, got undocumented=%+v", undocumentedOperationNames)
	}
	if len(coveringSkillNames) != 1 || coveringSkillNames[0] != "web-search" {
		t.Fatalf("expected web-search skill to cover web.search, got %+v", coveringSkillNames)
	}
}

func TestCapabilityEvidenceSkillCoverageReportsUndocumentedOperation(t *testing.T) {
	toolSet := newHiddenTestCapabilityToolSet([]string{"site.archive"})
	skillInstructions := []SkillInstruction{webSearchTestSkillInstruction()}

	coveringSkillNames, undocumentedOperationNames := requiredEvidenceSkillCoverage(toolSet, skillInstructions, []string{"site.archive"})

	if len(coveringSkillNames) != 0 {
		t.Fatalf("expected no skill to cover site.archive, got %+v", coveringSkillNames)
	}
	if len(undocumentedOperationNames) != 1 || undocumentedOperationNames[0] != "site.archive" {
		t.Fatalf("expected site.archive reported undocumented, got %+v", undocumentedOperationNames)
	}
}

func TestRequiredEvidenceSkillCoverageKeepsConfiguredBaseToolWithoutSkill(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"message.send"})

	coveringSkillNames, undocumentedOperationNames := requiredEvidenceSkillCoverage(toolSet, nil, []string{"message.send"})

	if len(coveringSkillNames) != 0 || len(undocumentedOperationNames) != 0 {
		t.Fatalf("expected configured base evidence to remain reachable without a skill, got covering=%+v undocumented=%+v", coveringSkillNames, undocumentedOperationNames)
	}
}

func TestCapabilityEvidenceSkillCoverageIgnoresStructurallyInvalidEvidence(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar.add", CapabilityInvokeToolName})
	skillInstructions := []SkillInstruction{webSearchTestSkillInstruction()}

	coveringSkillNames, undocumentedOperationNames := requiredEvidenceSkillCoverage(toolSet, skillInstructions, []string{"calendar.create"})

	if len(coveringSkillNames) != 0 || len(undocumentedOperationNames) != 0 {
		t.Fatalf("expected a structurally invalid evidence tool to be left for the existing invalid-evidence gate, got covering=%+v undocumented=%+v", coveringSkillNames, undocumentedOperationNames)
	}
}

func TestSelectInstructionBundleForResolvedRequestForcesDocumentingSkillIntoPrompt(t *testing.T) {
	agentKernel := &AgentKernel{}
	baseInstructionBundle := InstructionBundle{Skills: []SkillInstruction{webSearchTestSkillInstruction()}}
	request := AgentRequest{
		Prompt:  "경산 스타트업월드컵 일정 알려줘",
		ToolSet: newHiddenTestCapabilityToolSet([]string{"web.search"}),
	}
	intakeDecision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeResearchTask,
		RequiredEvidenceTools: []string{"web.search"},
	}

	instructionBundle, resolvedIntakeDecision := agentKernel.selectInstructionBundleForResolvedRequest(context.Background(), baseInstructionBundle, request, intakeDecision)

	if len(resolvedIntakeDecision.RequiredEvidenceTools) != 1 || resolvedIntakeDecision.RequiredEvidenceTools[0] != "web.search" {
		t.Fatalf("expected web.search evidence to survive reconciliation, got %+v", resolvedIntakeDecision.RequiredEvidenceTools)
	}
	if !selectedSkillNames(instructionBundle.SkillDecisions)["web-search"] {
		t.Fatalf("expected web-search skill to be force-selected, got decisions %+v", instructionBundle.SkillDecisions)
	}
	if !strings.Contains(instructionBundle.Prompt, "web.search") {
		t.Fatalf("expected the documenting skill body to reach the turn prompt, got: %s", instructionBundle.Prompt)
	}
}

func TestSelectInstructionBundleForResolvedRequestPrunesUndocumentedEvidence(t *testing.T) {
	agentKernel := &AgentKernel{}
	baseInstructionBundle := InstructionBundle{Skills: []SkillInstruction{webSearchTestSkillInstruction()}}
	request := AgentRequest{
		Prompt:  "이 자료 좀 보관해줘",
		ToolSet: newHiddenTestCapabilityToolSet([]string{"site.archive"}),
	}
	intakeDecision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		RequiredEvidenceTools: []string{"site.archive"},
	}

	_, resolvedIntakeDecision := agentKernel.selectInstructionBundleForResolvedRequest(context.Background(), baseInstructionBundle, request, intakeDecision)

	if len(resolvedIntakeDecision.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected an undocumented operation with no covering skill to be pruned, got %+v", resolvedIntakeDecision.RequiredEvidenceTools)
	}
}

func TestStalledExitDirectiveNamesMissingRequiredEvidenceOperation(t *testing.T) {
	observations := []turnObservation{
		completionGateObservation(1, "finish requires successful observation for web.search"),
	}

	directive := stalledExitDirectiveObservation("obs-002", observations)

	if !strings.Contains(directive.Summary, "not yet called web.search") {
		t.Fatalf("expected the stall directive to name the missing operation concretely, got: %s", directive.Summary)
	}
}

func TestStalledExitDirectiveFallsBackToGenericMessageWithoutMissingEvidence(t *testing.T) {
	directive := stalledExitDirectiveObservation("obs-001", nil)

	if strings.Contains(directive.Summary, "not yet called") {
		t.Fatalf("expected the generic stall directive without a named operation, got: %s", directive.Summary)
	}
}

// TestResearchTaskWithWebSearchEvidenceReachesCompletionInsteadOfLooping reproduces the
// production failure: intake classifies a request as needing web.search evidence, the model
// first tries to finish from attachment content alone (rejected), and without a skill
// documenting web.search the model would keep re-emitting rejected finish actions forever.
// With the fix, the web-search skill is force-included so the model can discover and call
// the operation, and the task completes within a small, bounded number of steps.
func TestResearchTaskWithWebSearchEvidenceReachesCompletionInsteadOfLooping(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseInstructionBundleLoader(func() InstructionBundle {
		return InstructionBundle{Skills: []SkillInstruction{webSearchTestSkillInstruction()}}
	})
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:                 TurnRouteStartTask,
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeResearchTask,
		TaskLevel:             TaskLevelLow,
		ResponseLanguage:      "ko",
		Reason:                "research task requires web source evidence",
		RequiredEvidenceTools: []string{"web.search"},
		InitialToolNames:      []string{CapabilityInvokeToolName},
	}})

	webSearchCallCount := 0
	toolSet := newHiddenTestCapabilityToolSet([]string{"web.search"})
	toolSet.RegisterTool(ToolDefinition{Name: "web.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		webSearchCallCount++
		return ToolSuccess(`{"query":"경산 스타트업월드컵","answer":"일정 확인됨","results":[]}`), nil
	})

	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"finish","message":"일정은 첨부 이미지에 있는 대로입니다.","completionSummary":"첨부 이미지에서 일정 확인","replyParts":[{"type":"text","text":"일정 확인"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[]}`,
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"web.search","input":{"query":"경산 스타트업월드컵 일정"}}}`,
		finishMessageWithEvidence("일정을 확인했습니다.", "obs-002", "web.search", 0),
	}}
	agentKernel.UseLanguageModelProvider(languageModel)

	request := kernelTestRequest("경산 스타트업월드컵 일정 알려줘 (첨부 이미지 참고)")
	request.ToolSet = toolSet

	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected the research task to complete: %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected the task to complete instead of looping or blocking, got %q", result.TaskRun.Status)
	}
	if webSearchCallCount != 1 {
		t.Fatalf("expected web.search to be called exactly once once discoverable, got %d", webSearchCallCount)
	}
	if len(languageModel.requests) > 3 {
		t.Fatalf("expected completion within a bounded number of model turns, used %d", len(languageModel.requests))
	}
	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "agent.intake", "web.search") {
		t.Fatal("expected the intake event to record the web.search requirement")
	}
}
