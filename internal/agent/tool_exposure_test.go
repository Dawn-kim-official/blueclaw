package agent

import (
	"context"
	"testing"
)

func newHybridKernelCapabilityToolSet(kernelToolNames []string, operationNames []string) *ToolSet {
	toolNames := append(append([]string{}, kernelToolNames...), operationNames...)
	toolSet := NewToolSet(toolNames)
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
	toolSet := testToolSet(append(KernelToolNames(), CapabilityInvokeToolName, TaskHistoryToolName, "task.add", "task.list"))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.add", "task.list"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{}, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range []string{"task.add", "task.list"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{CapabilityInvokeToolName, TaskHistoryToolName} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected internal tool %s to stay hidden, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	if !sameStringSet(event.SelectedSkillToolIDs, []string{"task.add", "task.list"}) {
		t.Fatalf("expected selected skill event, got %+v", event)
	}
	if event.SelectionSource != "selected_skills" {
		t.Fatalf("expected selected skill source, got %+v", event)
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
			{Name: "secondary", AllowedTools: secondaryToolNames},
			{Name: "internkim-flow", AllowedTools: flowToolNames},
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
		Skills:         []SkillInstruction{{Name: "website", AllowedTools: selectedToolNames}},
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
	if len(filteredToolSet.ListToolNames()) != maxSchemaCallableToolCount {
		t.Fatalf("expected %d tools, got %+v", maxSchemaCallableToolCount, filteredToolSet.ListToolNames())
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
