package agent

import (
	"context"
	"testing"
)

func newHybridKernelCapabilityToolSet(kernelToolNames []string, operationNames []string) *ToolSet {
	toolNames := append(append([]string{}, kernelToolNames...), operationNames...)
	toolSet := NewToolSet(toolNames)
	toolSet.allowsTestReplacement = true
	for _, toolName := range toolNames {
		toolSet.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	return toolSet
}

func TestPlannedToolsDropRepeatedFileRead(t *testing.T) {
	observations := []turnObservation{
		newFailureObservation("obs-001", "policy", "file.read", "Already read tmp/deck/presentation.md lines 1-400.", FailurePolicyBlocked, FailureCodes.PolicyBlocked, "file_read_repeat"),
	}

	toolNames := filterExhaustedRecoveryToolNames([]string{"file.read", "terminal.run", "file.deliver"}, observations)

	if stringSliceContains(toolNames, "file.read") {
		t.Fatalf("expected repeated file.read to be removed, got %+v", toolNames)
	}
	for _, toolName := range []string{"terminal.run", "file.deliver"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, toolNames)
		}
	}
}

func TestSelectedSkillExposesDirectTools(t *testing.T) {
	toolSet := testToolSet(append(KernelToolNames(), TaskHistoryToolName, "task.add", "task.list"))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task.add", "task.list"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{}, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range []string{"task.add", "task.list"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed(SkillSearchToolName) {
		t.Fatalf("expected loaded skill instructions to hide skill.search, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed(TaskHistoryToolName) {
		t.Fatalf("expected internal tool %s to stay hidden, got %+v", TaskHistoryToolName, filteredToolSet.ListToolNames())
	}
	if !sameStringSet(event.SelectedSkillToolIDs, []string{"task.add", "task.list"}) {
		t.Fatalf("expected selected skill event, got %+v", event)
	}
	if event.SelectionSource != "selected_skills" {
		t.Fatalf("expected selected skill source, got %+v", event)
	}
}

func TestAuthoritativeContractExposesSelectedTaskDomain(t *testing.T) {
	flowToolNames := []string{"task.add", "task.list", "task.update", "task.delete"}
	toolSet := testToolSet(append(KernelToolNames(), flowToolNames...))
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "internkim-flow", ToolReferences: flowToolNames}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredNextTools:           []string{"task.add"},
		RequiredEvidenceTools:       []string{"task.add"},
		HasContractSkillArbitration: true,
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{PinnedToolNames: []string{"task.add"}},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
		ToolExposureEvent{},
	)

	expectedToolNames := append(kernelToolNamesForInstructionBundle(instructionBundle), flowToolNames...)
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected selected task domain, got %+v", filteredToolSet.ListToolNames())
	}
	if event.SelectionSource != "contract_arbitration" {
		t.Fatalf("expected contract arbitration source, got %+v", event)
	}
	if !sameStringSet(event.SelectedSkillToolIDs, flowToolNames) {
		t.Fatalf("expected selected task tools, got %+v", event)
	}
}

func TestAuthoritativeContractPreservesCompoundWorkflow(t *testing.T) {
	flowToolNames := []string{"task.add", "task.list", "task.update", "task.delete"}
	calendarToolNames := []string{"calendar.add", "calendar.list", "calendar.update", "calendar.delete"}
	toolSet := testToolSet(append(append(KernelToolNames(), flowToolNames...), calendarToolNames...))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "internkim-flow", ToolReferences: flowToolNames},
			{Name: "calendar", ToolReferences: calendarToolNames},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "internkim-flow", Status: "selected"},
			{Name: "calendar", Status: "selected"},
		},
		RequiredNextTools:           []string{"task.add", "calendar.add"},
		RequiredEvidenceTools:       []string{"task.add", "calendar.add"},
		HasContractSkillArbitration: true,
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task.add", "calendar.add"}},
		ToolExposureEvent{},
	)

	expectedToolNames := append(append(kernelToolNamesForInstructionBundle(instructionBundle), flowToolNames...), calendarToolNames...)
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected selected domain tools, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAuthoritativeContractPreservesTypedRecoveryTool(t *testing.T) {
	flowToolNames := []string{"task.add", "task.update"}
	toolSet := testToolSet(append(KernelToolNames(), flowToolNames...))
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "internkim-flow", ToolReferences: flowToolNames}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredNextTools:           []string{"task.add"},
		HasContractSkillArbitration: true,
	}
	observation := newFailureObservation("obs-001", "continue", "task.add", "retry with an existing task", FailureInvalidInput, FailureCodes.InvalidInput, "invoke")
	observation.ToolInputKey = "task.add\x00{}"
	observation.Failure.RecoveryHints = []RecoveryHint{{ToolNames: []string{"task.update"}}}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task.add"}},
		ToolExposureEvent{},
		[]turnObservation{observation},
	)

	expectedToolNames := append(kernelToolNamesForInstructionBundle(instructionBundle), "task.add", "task.update")
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected contract and recovery working set, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestImmediateReplyWithoutToolIntentExposesNoTools(t *testing.T) {
	toolSet := testToolSet(KernelToolNames())

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{TaskShape: TaskShapeImmediateReply},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if len(filteredToolSet.ListToolNames()) != 0 {
		t.Fatalf("expected pure reply to expose no tools, got %+v", filteredToolSet.ListToolNames())
	}
	if len(event.ExposedToolIDs) != 0 {
		t.Fatalf("expected pure reply event to contain no tools, got %+v", event)
	}
}

func TestImmediateReplyWithPinnedToolExposesFullKernel(t *testing.T) {
	toolSet := testToolSet(append(KernelToolNames(), "math.calculate"))

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{TaskShape: TaskShapeImmediateReply, PinnedToolNames: []string{"math.calculate"}},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	expectedToolNames := append(append([]string{}, KernelToolNames()...), "math.calculate")
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected full kernel with the pinned tool, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestEmptyArbitrationWorkingSetPreservesDocumentKernel(t *testing.T) {
	toolSet := testToolSet(KernelToolNames())
	instructionBundle := InstructionBundle{
		Skills:                      []SkillInstruction{{Name: "document"}},
		SkillDecisions:              []SkillSelectionDecision{{Name: "document", Status: "selected"}},
		HasContractSkillArbitration: true,
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{FileDeliverToolName}},
		ToolExposureEvent{},
	)

	if !sameStringSet(filteredToolSet.ListToolNames(), kernelToolNamesForInstructionBundle(instructionBundle)) {
		t.Fatalf("expected document kernel fallback, got %+v", filteredToolSet.ListToolNames())
	}
	if event.SelectionSource != "fixed_kernel" {
		t.Fatalf("expected fixed kernel source, got %+v", event)
	}
}

func TestSelectedSkillRankingControlsToolBudget(t *testing.T) {
	secondaryToolNames := []string{
		"calendar.add", "calendar.list", "calendar.update", "calendar.delete",
		"company.info.get", "company.info.set", "company.metric.list", "company.metric.record",
		"company.record.list", "company.record.add", "company.record.update", "company.record.delete",
		"company.document.list", "company.document.search", "company.document.register",
	}
	flowToolNames := []string{"task.add", "task.list", "task.update", "task.delete"}
	toolSet := testToolSet(append(append(KernelToolNames(), secondaryToolNames...), flowToolNames...))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "secondary", ToolReferences: secondaryToolNames},
			{Name: "internkim-flow", ToolReferences: flowToolNames},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "internkim-flow", Status: "selected"},
			{Name: "secondary", Status: "selected"},
		},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{}, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range flowToolNames {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected first-ranked skill tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestPinnedDirectToolWinsSelectedSkillBudget(t *testing.T) {
	selectedToolNames := []string{
		"site.create", "site.preview", "artifact.review", "site.publish",
		"site.status", "site.history", "site.diff", "site.logs",
		"site.rollback", "site.unpublish", "site.restore", "site.delete",
		"file.read", "file.write", "file.edit", "terminal.run",
	}
	toolSet := testToolSet(append(KernelToolNames(), selectedToolNames...))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "website", ToolReferences: selectedToolNames}},
		SkillDecisions: []SkillSelectionDecision{{Name: "website", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{PinnedToolNames: []string{"terminal.run"}},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed("terminal.run") {
		t.Fatalf("expected pinned direct tool inside budget, got %+v", filteredToolSet.ListToolNames())
	}
	expectedToolCount := len(kernelToolNamesForInstructionBundle(instructionBundle)) + maxExtensionCallableToolCount
	if len(filteredToolSet.ListToolNames()) != expectedToolCount {
		t.Fatalf("expected %d tools, got %+v", expectedToolCount, filteredToolSet.ListToolNames())
	}
	if len(event.DroppedGroups) == 0 {
		t.Fatalf("expected oversized selected skill to report dropped tools, got %+v", event)
	}
	for _, toolName := range event.SelectedSkillToolIDs {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill metadata to contain only exposed tools, got %+v", event)
		}
	}
}

func TestRequiredEvidenceWinsToolBudget(t *testing.T) {
	selectedToolNames := []string{
		"site.create", "site.preview", "artifact.review", "site.publish",
		"site.status", "site.history", "site.diff", "site.logs",
		"site.rollback", "site.unpublish", "site.restore", "site.delete",
		"file.read", "file.write", "file.edit", "terminal.run",
	}
	toolSet := testToolSet(append(append(KernelToolNames(), selectedToolNames...), "task.update"))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "website", ToolReferences: selectedToolNames}},
		SkillDecisions: []SkillSelectionDecision{{Name: "website", Status: "selected"}},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceTools: []string{"task.update"}},
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed("task.update") {
		t.Fatalf("expected required evidence inside budget, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestEachRequiredEvidenceAlternativeGroupKeepsOneTool(t *testing.T) {
	firstGroup := []string{
		"tool.01", "tool.02", "tool.03", "tool.04", "tool.05",
		"tool.06", "tool.07", "tool.08", "tool.09", "tool.10",
		"tool.11", "tool.12", "tool.13", "tool.14", "tool.15",
	}
	secondGroup := []string{"task.update"}
	toolSet := testToolSet(append(append(KernelToolNames(), firstGroup...), secondGroup...))

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{RequiredEvidenceAnyOf: [][]string{firstGroup, secondGroup}},
		ToolExposureEvent{},
	)

	for _, toolName := range []string{firstGroup[0], secondGroup[0]} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected one tool from every evidence group, got %+v", filteredToolSet.ListToolNames())
		}
	}
}
